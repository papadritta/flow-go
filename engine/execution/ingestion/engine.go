package ingestion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"

	"github.com/rs/zerolog"
	"go.uber.org/atomic"

	"github.com/onflow/flow-go/consensus/hotstuff/notifications"
	"github.com/onflow/flow-go/crypto"
	"github.com/onflow/flow-go/crypto/hash"
	"github.com/onflow/flow-go/engine"
	"github.com/onflow/flow-go/engine/execution"
	"github.com/onflow/flow-go/engine/execution/computation"
	"github.com/onflow/flow-go/engine/execution/provider"
	"github.com/onflow/flow-go/engine/execution/state"
	"github.com/onflow/flow-go/engine/execution/state/delta"
	"github.com/onflow/flow-go/engine/execution/utils"
	"github.com/onflow/flow-go/model/flow"
	"github.com/onflow/flow-go/model/flow/filter"
	"github.com/onflow/flow-go/model/messages"
	"github.com/onflow/flow-go/module"
	"github.com/onflow/flow-go/module/mempool"
	"github.com/onflow/flow-go/module/mempool/entity"
	"github.com/onflow/flow-go/module/mempool/queue"
	"github.com/onflow/flow-go/module/mempool/stdmap"
	"github.com/onflow/flow-go/module/trace"
	"github.com/onflow/flow-go/network"
	"github.com/onflow/flow-go/state/protocol"
	psEvents "github.com/onflow/flow-go/state/protocol/events"
	"github.com/onflow/flow-go/storage"
	"github.com/onflow/flow-go/utils/logging"
)

// An Engine receives and saves incoming blocks.
type Engine struct {
	psEvents.Noop              // satisfy protocol events consumer interface
	notifications.NoopConsumer // satisfy the FinalizationConsumer interface

	unit               *engine.Unit
	log                zerolog.Logger
	me                 module.Local
	request            module.Requester // used to request collections
	state              protocol.State
	receiptHasher      hash.Hasher // used as hasher to sign the execution receipt
	blocks             storage.Blocks
	collections        storage.Collections
	events             storage.Events
	transactionResults storage.TransactionResults
	computationManager computation.ComputationManager
	providerEngine     provider.ProviderEngine
	mempool            *Mempool
	execState          state.ExecutionState
	metrics            module.ExecutionMetrics
	tracer             module.Tracer
	extensiveLogging   bool
	spockHasher        hash.Hasher
	// TODO: move all state syncing related logic to a separate module
	syncingHeight atomic.Uint64       // syncingHeight == 0 means not syncing, otherwise it's the target height to sync to
	syncThreshold int                 // the threshold for how many sealed unexecuted blocks to trigger state syncing.
	syncFilter    flow.IdentityFilter // specify the filter to sync state from
	syncConduit   network.Conduit     // sending state syncing requests
	syncDeltas    mempool.Deltas      // storing the synced state deltas
	syncFast      bool                // sync fast allows execution node to skip fetching collection during state syncing, and rely on state syncing to catch up
}

func New(
	logger zerolog.Logger,
	net module.Network,
	me module.Local,
	request module.Requester,
	state protocol.State,
	blocks storage.Blocks,
	collections storage.Collections,
	events storage.Events,
	transactionResults storage.TransactionResults,
	executionEngine computation.ComputationManager,
	providerEngine provider.ProviderEngine,
	execState state.ExecutionState,
	metrics module.ExecutionMetrics,
	tracer module.Tracer,
	extLog bool,
	syncFilter flow.IdentityFilter,
	syncDeltas mempool.Deltas,
	syncThreshold int,
	syncFast bool,
) (*Engine, error) {
	log := logger.With().Str("engine", "ingestion").Logger()

	mempool := newMempool()

	eng := Engine{
		unit:               engine.NewUnit(),
		log:                log,
		me:                 me,
		request:            request,
		state:              state,
		receiptHasher:      utils.NewExecutionReceiptHasher(),
		spockHasher:        utils.NewSPOCKHasher(),
		blocks:             blocks,
		collections:        collections,
		events:             events,
		transactionResults: transactionResults,
		computationManager: executionEngine,
		providerEngine:     providerEngine,
		mempool:            mempool,
		execState:          execState,
		metrics:            metrics,
		tracer:             tracer,
		extensiveLogging:   extLog,
		syncFilter:         syncFilter,
		syncThreshold:      syncThreshold,
		syncDeltas:         syncDeltas,
		syncFast:           syncFast,
	}

	// move to state syncing engine
	syncConduit, err := net.Register(engine.SyncExecution, &eng)
	if err != nil {
		return nil, fmt.Errorf("could not register execution blockSync engine: %w", err)
	}

	eng.syncConduit = syncConduit

	return &eng, nil
}

// Ready returns a channel that will close when the engine has
// successfully started.
func (e *Engine) Ready() <-chan struct{} {
	err := e.loadAllFinalizedAndUnexecutedBlocks()
	if err != nil {
		e.log.Fatal().Err(err).Msg("failed to load all unexecuted blocks")
	}

	return e.unit.Ready()
}

// Done returns a channel that will close when the engine has
// successfully stopped.
func (e *Engine) Done() <-chan struct{} {
	return e.unit.Done()
}

// SubmitLocal submits an event originating on the local node.
func (e *Engine) SubmitLocal(event interface{}) {
	e.Submit(e.me.NodeID(), event)
}

// Submit submits the given event from the node with the given origin ID
// for processing in a non-blocking manner. It returns instantly and logs
// a potential processing error internally when done.
func (e *Engine) Submit(originID flow.Identifier, event interface{}) {
	e.unit.Launch(func() {
		err := e.process(originID, event)
		if err != nil {
			engine.LogError(e.log, err)
		}
	})
}

// ProcessLocal processes an event originating on the local node.
func (e *Engine) ProcessLocal(event interface{}) error {
	return fmt.Errorf("ingestion error does not process local events")
}

func (e *Engine) Process(originID flow.Identifier, event interface{}) error {
	return e.unit.Do(func() error {
		return e.process(originID, event)
	})
}

func (e *Engine) process(originID flow.Identifier, event interface{}) error {
	switch resource := event.(type) {
	case *messages.ExecutionStateSyncRequest:
		return e.handleStateSyncRequest(originID, resource)
	case *messages.ExecutionStateDelta:
		return e.handleStateDeltaResponse(originID, resource)
	default:
		return fmt.Errorf("invalid event type (%T)", event)
	}
}

// on nodes startup, we need to load all the unexecuted blocks to the execution queues.
func (e *Engine) loadAllFinalizedAndUnexecutedBlocks() error {
	// get finalized height
	header, err := e.state.Final().Head()
	if err != nil {
		return fmt.Errorf("could not get finalized block: %w", err)
	}

	finalizedHeight := header.Height

	// get the last executed height
	lastExecutedHeight, _, err := e.execState.GetHighestExecutedBlockID(e.unit.Ctx())
	if err != nil {
		return fmt.Errorf("could not get last executed block: %w", err)
	}

	var unexecuted int64
	unexecuted = int64(finalizedHeight) - int64(lastExecutedHeight)

	e.log.Info().
		Int64("count", unexecuted).
		Msg("reloading finalized and unexecuted blocks to execution queues...")

	// log the number of unexecuted blocks
	if unexecuted <= 0 {
		return nil
	}

	count := 0
	for height := lastExecutedHeight + 1; height <= finalizedHeight; height++ {
		block, err := e.blocks.ByHeight(height)
		if err != nil {
			return fmt.Errorf("could not get block by height: %w", err)
		}

		executableBlock := &entity.ExecutableBlock{
			Block:               block,
			CompleteCollections: make(map[flow.Identifier]*entity.CompleteCollection),
		}

		blockID := executableBlock.ID()

		// acquiring the lock so that there is only one process modifying the queue
		err = e.mempool.Run(
			func(
				blockByCollection *stdmap.BlockByCollectionBackdata,
				executionQueues *stdmap.QueuesBackdata,
			) error {
				// adding the block to the queue,
				_, added := enqueue(executableBlock, executionQueues)
				if !added {
					// we started from an empty queue, and added each finalized block to the
					// queue. Each block should always be added to the queues.
					// a sanity check it must be an exception if not added.
					return fmt.Errorf("block %v is not added to the queue", blockID)
				}

				return nil
			})

		if err != nil {
			return fmt.Errorf("failed to recover block %v", err)
		}

		count++
	}

	e.log.Info().Int("count", count).
		Msg("reloaded all the finalized and unexecuted blocks to execution queues")

	return nil
}

// BlockProcessable handles the new verified blocks (blocks that
// have passed consensus validation) received from the consensus nodes
// Note: BlockProcessable might be called multiple times for the same block.
func (e *Engine) BlockProcessable(b *flow.Header) {
	blockID := b.ID()
	newBlock, err := e.blocks.ByID(blockID)
	if err != nil {
		e.log.Fatal().Err(err).Msgf("could not get incorporated block(%v): %v", blockID, err)
	}

	e.log.Debug().Hex("block_id", blockID[:]).
		Uint64("height", b.Height).
		Msg("handling new block")

	err = e.handleBlock(e.unit.Ctx(), newBlock)
	if err != nil {
		e.log.Error().Err(err).Hex("block_id", blockID[:]).Msg("failed to handle block")
	}
}

// Main handling

// handle block will process the incoming block.
// the block has passed the consensus validation.
func (e *Engine) handleBlock(ctx context.Context, block *flow.Block) error {
	blockID := block.ID()
	log := e.log.With().Hex("block_id", blockID[:]).Logger()

	_, err := e.execState.StateCommitmentByBlockID(ctx, blockID)
	if err == nil {
		// a statecommitment being stored indicates the block
		// has been executed
		log.Debug().Msg("block has been executed already")
		return nil
	}

	if !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("could not query state commitment for block: %w", err)
	}

	// unexecuted block
	e.metrics.StartBlockReceivedToExecuted(blockID)

	executableBlock := &entity.ExecutableBlock{
		Block:               block,
		CompleteCollections: make(map[flow.Identifier]*entity.CompleteCollection),
	}

	// acquiring the lock so that there is only one process modifying the queue
	return e.mempool.Run(
		func(
			blockByCollection *stdmap.BlockByCollectionBackdata,
			executionQueues *stdmap.QueuesBackdata,
		) error {
			// adding the block to the queue,
			queue, added := enqueue(executableBlock, executionQueues)

			// if it's not added, it means the block is not a new block, it already
			// exists in the queue, then bail
			if !added {
				log.Debug().Msg("block already exists in the execution queue")
				return nil
			}

			// whenever the queue grows, we need to check whether the state sync should be
			// triggered.
			firstUnexecutedHeight := queue.Head.Item.Height()
			e.unit.Launch(func() {
				e.checkStateSyncStart(firstUnexecutedHeight)
			})

			// check if a block is executable.
			// a block is executable if the following conditions are all true
			// 1) the parent state commitment is ready
			// 2) the collections for the block payload are ready
			// 3) the child block is ready for querying the randomness

			// check if the block's parent has been executed. (we can't execute the block if the parent has
			// not been executed yet)
			// check if there is a statecommitment for the parent block
			parentCommitment, err := e.execState.StateCommitmentByBlockID(ctx, block.Header.ParentID)

			// if we found the statecommitment for the parent block, then add it to the executable block.
			if err == nil {
				executableBlock.StartState = parentCommitment
			} else if errors.Is(err, storage.ErrNotFound) {
				// the parent block is an unexecuted block.
				// if the queue only has one block, and its parent doesn't
				// exist in the queue, then we need to load the block from the storage.
				log.Error().Msgf("an unexecuted parent block is missing in the queue")
			} else {
				// if there is exception, then crash
				log.Fatal().Err(err).Msg("unexpected error while accessing storage, shutting down")
			}

			// check if we have all the collections for the block, and request them if there is missing.
			err = e.matchOrRequestCollections(executableBlock, blockByCollection)
			if err != nil {
				return fmt.Errorf("cannot send collection requests: %w", err)
			}

			// execute the block if the block is ready to be executed
			e.executeBlockIfComplete(executableBlock)

			return nil
		},
	)
}

// executeBlock will execute the block.
// When finish executing, it will check if the children becomes executable and execute them if yes.
func (e *Engine) executeBlock(ctx context.Context, executableBlock *entity.ExecutableBlock) {

	e.log.Info().
		Hex("block_id", logging.Entity(executableBlock)).
		Msg("executing block")

	span, ctx := e.tracer.StartSpanFromContext(ctx, trace.EXEExecuteBlock)
	defer span.Finish()

	view := e.execState.NewView(executableBlock.StartState)

	computationResult, err := e.computationManager.ComputeBlock(ctx, executableBlock, view)
	if err != nil {
		e.log.Err(err).
			Hex("block_id", logging.Entity(executableBlock)).
			Msg("error while computing block")
		return
	}

	e.metrics.FinishBlockReceivedToExecuted(executableBlock.ID())
	e.metrics.ExecutionGasUsedPerBlock(computationResult.GasUsed)
	e.metrics.ExecutionStateReadsPerBlock(computationResult.StateReads)

	finalState, err := e.handleComputationResult(ctx, computationResult, executableBlock.StartState)
	if err != nil {
		e.log.Err(err).
			Hex("block_id", logging.Entity(executableBlock)).
			Msg("error while handing computation results")
		return
	}

	e.log.Info().
		Hex("block_id", logging.Entity(executableBlock)).
		Uint64("block_height", executableBlock.Block.Header.Height).
		Hex("final_state", finalState).
		Msg("block executed")

	err = e.onBlockExecuted(executableBlock, finalState)
	if err != nil {
		e.log.Err(err).Msg("failed in process block's children")
	}
}

// we've executed the block, now we need to check:
// 1. whether the state syncing can be turned off
// 2. whether its children can be executed
//   the executionQueues stores blocks as a tree:
//
//   10 <- 11 <- 12
//   	 ^-- 13
//   14 <- 15 <- 16
//
//   if block 10 is the one just executed, then we will remove it from the queue, and add
//   its children back, meaning the tree will become:
//
//   11 <- 12
//   13
//   14 <- 15 <- 16

func (e *Engine) onBlockExecuted(executed *entity.ExecutableBlock, finalState flow.StateCommitment) error {

	e.metrics.ExecutionStorageStateCommitment(int64(len(finalState)))
	e.metrics.ExecutionLastExecutedBlockHeight(executed.Block.Header.Height)

	e.checkStateSyncStop(executed.Block.Header.Height)

	err := e.mempool.Run(
		func(
			blockByCollection *stdmap.BlockByCollectionBackdata,
			executionQueues *stdmap.QueuesBackdata,
		) error {
			// find the block that was just executed
			executionQueue, exists := executionQueues.ByID(executed.ID())
			if !exists {
				return fmt.Errorf("fatal error - executed block not present in execution queue")
			}

			// dismount the executed block and all its children
			_, newQueues := executionQueue.Dismount()

			// go through each children, add them back to the queue, and check
			// if the children is executable
			for _, queue := range newQueues {
				added := executionQueues.Add(queue)
				if !added {
					return fmt.Errorf("fatal error - child block already in execution queue")
				}

				// the parent block has been executed, update the StartState of
				// each child block.
				child := queue.Head.Item.(*entity.ExecutableBlock)
				child.StartState = finalState

				err := e.matchOrRequestCollections(child, blockByCollection)
				if err != nil {
					return fmt.Errorf("cannot send collection requests: %w", err)
				}

				completed := e.executeBlockIfComplete(child)
				if !completed {
					e.log.Debug().
						Hex("executed_block", logging.Entity(executed)).
						Hex("child_block", logging.Entity(child)).
						Msg("child block is not ready to be executed yet")
				} else {
					e.log.Debug().
						Hex("executed_block", logging.Entity(executed)).
						Hex("child_block", logging.Entity(child)).
						Msg("child block is ready to be executed")
				}
			}

			// remove the executed block
			executionQueues.Rem(executed.ID())

			return nil
		})

	if err != nil {
		e.log.Err(err).
			Hex("block", logging.Entity(executed)).
			Msg("error while requeueing blocks after execution")
	}

	return nil
}

// executeBlockIfComplete checks whether the block is ready to be executed.
// if yes, execute the block
// return a bool indicates whether the block was completed
func (e *Engine) executeBlockIfComplete(eb *entity.ExecutableBlock) bool {
	if !eb.HasStartState() {
		return false
	}

	// if the eb has parent statecommitment, and we have the delta for this block
	// then apply the delta
	// note the block ID is the delta's ID
	delta, found := e.syncDeltas.ByBlockID(eb.Block.ID())
	if found {
		// double check before applying the state delta
		if bytes.Equal(eb.StartState, delta.ExecutableBlock.StartState) {
			e.unit.Launch(func() {
				e.applyStateDelta(delta)
			})
			return true
		}

		// if state delta is invalid, remove the delta and log error
		e.log.Error().
			Hex("block_start_state", eb.StartState).
			Hex("delta_start_state", delta.ExecutableBlock.StartState).
			Msg("can not apply the state delta, the start state does not match")

		e.syncDeltas.Rem(eb.Block.ID())
	}

	// if don't have the delta, then check if everything is ready for executing
	// the block
	if eb.IsComplete() {

		if e.extensiveLogging {
			e.logExecutableBlock(eb)
		}

		e.unit.Launch(func() {
			e.executeBlock(e.unit.Ctx(), eb)
		})
		return true
	}
	return false
}

// OnCollection is a callback for handling the collections requested by the
// collection requester.
func (e *Engine) OnCollection(originID flow.Identifier, entity flow.Entity) {
	// convert entity to strongly typed collection
	collection, ok := entity.(*flow.Collection)
	if !ok {
		e.log.Error().Msgf("invalid entity type (%T)", entity)
		return
	}

	// no need to validate the origin ID, since the collection requester has
	// checked the origin must be a collection node.

	err := e.handleCollection(originID, collection)
	if err != nil {
		e.log.Error().Err(err).Msg("could not handle collection")
	}
}

// a block can't be executed if its collection is missing.
// since a collection can belong to multiple blocks, we need to
// find all the blocks that are needing this collection, and then
// check if any of these block becomes executable and execut it if
// is.
func (e *Engine) handleCollection(originID flow.Identifier, collection *flow.Collection) error {

	collID := collection.ID()

	log := e.log.With().Hex("collection_id", collID[:]).Logger()

	log.Info().Hex("sender", originID[:]).Msg("handle collection")

	// TODO: bail if have seen this collection before.
	err := e.collections.Store(collection)
	if err != nil {
		return fmt.Errorf("cannot store collection: %w", err)
	}

	return e.mempool.BlockByCollection.Run(
		func(backdata *stdmap.BlockByCollectionBackdata) error {
			blockByCollectionID, exists := backdata.ByID(collID)

			// if we don't find any block for this collection, then
			// means we don't need this collection any more.
			// or it was ejected from the mempool when it was full.
			// either way, we will return
			if !exists {
				log.Debug().Msg("could not find block for collection")
				return nil
			}

			for _, executableBlock := range blockByCollectionID.ExecutableBlocks {
				blockID := executableBlock.ID()

				completeCollection, ok := executableBlock.CompleteCollections[collID]
				if !ok {
					return fmt.Errorf("cannot handle collection: internal inconsistency - collection pointing to block %v which does not contain said collection", blockID)
				}

				if completeCollection.IsCompleted() {
					// already received transactions for this collection
					continue
				}

				// update the transactions of the collection
				// Note: it's guaranteed the transactions are for this collection, because
				// the collection id matches with the CollectionID from the collection guarantee
				completeCollection.Transactions = collection.Transactions

				// check if the block becomes executable
				completed := e.executeBlockIfComplete(executableBlock)

				log.Debug().Hex("block_id", blockID[:]).Bool("completed", completed).Msg("collection added to block")
			}

			// since we've received this collection, remove it from the index
			backdata.Rem(collID)

			return nil
		},
	)
}

func newQueue(blockify queue.Blockify, queues *stdmap.QueuesBackdata) (*queue.Queue, bool) {
	q := queue.NewQueue(blockify)
	return q, queues.Add(q)
}

// enqueue adds a block to the queues, return the queue that includes the block and a bool
// indicating whether the block was a new block.
// queues are chained blocks. Since a block can't be executable until its parent has been
// executed, the chained structure allows us to only check the head of each queue to see if
// any block becomes executable.
// for instance we have one queue whose head is A:
// A <- B <- C
//   ^- D <- E
// If we receive E <- F, then we will add it to the queue:
// A <- B <- C
//   ^- D <- E <- F
// Even through there are 6 blocks, we only need to check if block A becomes executable.
// when the parent block isn't in the queue, we add it as a new queue. for instace, if
// we receive H <- G, then the queues will become:
// A <- B <- C
//   ^- D <- E
// G
func enqueue(blockify queue.Blockify, queues *stdmap.QueuesBackdata) (*queue.Queue, bool) {
	for _, queue := range queues.All() {
		if queue.TryAdd(blockify) {
			return queue, true
		}
	}
	return newQueue(blockify, queues)
}

// check if the block's collections have been received,
// if yes, add the collection to the executable block
// if no, fetch the collection.
// if a block has 3 collection, it would be 3 reqs to fetch them.
// mark the collection belongs to the block,
// mark the block contains this collection.
func (e *Engine) matchOrRequestCollections(
	executableBlock *entity.ExecutableBlock,
	collectionsBackdata *stdmap.BlockByCollectionBackdata,
) error {
	// if the state syncing is on, it will fetch deltas for sealed and
	// unexecuted blocks. However, for any new blocks, we are still fetching
	// collections for them, which is not necessary, because the state deltas
	// will include the collection.
	// Fetching those collections will introduce load to collection nodes,
	// and handling them would increase memory usage and network bandwidth.
	// Therefore, we introduced this "sync-fast" mode.
	// The sync-fast mode can be turned on by the `sync-fast=true` flag.
	// When it's turned on, it will skip fetching collections, and will
	// rely on the state syncing to catch up.
	if e.syncFast {
		isSyncing := e.isSyncingState()
		if isSyncing {
			return nil
		}
	}

	// make sure that the requests are dispatched immediately by the requester
	if len(executableBlock.Block.Payload.Guarantees) > 0 {
		defer e.request.Force()
		defer e.metrics.ExecutionCollectionRequestSent()
	}

	actualRequested := 0

	for _, guarantee := range executableBlock.Block.Payload.Guarantees {
		coll := &entity.CompleteCollection{
			Guarantee: guarantee,
		}
		executableBlock.CompleteCollections[guarantee.ID()] = coll

		// check if we have requested this collection before.
		// blocksNeedingCollection stores all the blocks that contain this collection

		if blocksNeedingCollection, exists := collectionsBackdata.ByID(guarantee.ID()); exists {
			// if we've requested this collection, it means other block might also contain this collection.
			// in this case, add this block to the map so that when the collection is received,
			// we could update the executable block
			blocksNeedingCollection.ExecutableBlocks[executableBlock.ID()] = executableBlock

			// since the collection is still being requested, we don't have the transactions
			// yet, so exit
			continue
		}

		// if we are not requesting this collection, then there are two cases here:
		// 1) we have never seen this collection
		// 2) we have seen this collection from some other block

		// if we've requested this collection, we will store it in the storage,
		// so check the storage to see whether we've seen it.
		collection, err := e.collections.ByID(guarantee.CollectionID)

		if err == nil {
			// we found the collection, update the transactions
			coll.Transactions = collection.Transactions
			continue
		}

		// check if there was exception
		if !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("error while querying for collection: %w", err)
		}

		// the storage doesn't have this collection, meaning this is our first time seeing this
		// collection guarantee, create an entry to store in collectionsBackdata in order to
		// update the executable blocks when the collection is received.
		blocksNeedingCollection := &entity.BlocksByCollection{
			CollectionID:     guarantee.ID(),
			ExecutableBlocks: map[flow.Identifier]*entity.ExecutableBlock{executableBlock.ID(): executableBlock},
		}

		added := collectionsBackdata.Add(blocksNeedingCollection)
		if !added {
			// sanity check, should not happen, unless mempool implementation has a bug
			return fmt.Errorf("collection already mapped to block")
		}

		e.log.Debug().
			Hex("block", logging.Entity(executableBlock)).
			Hex("collection_id", logging.ID(guarantee.ID())).
			Msg("requesting collection")

		// queue the collection to be requested from one of the guarantors
		e.request.EntityByID(guarantee.ID(), filter.HasNodeID(guarantee.SignerIDs...))
		actualRequested++
	}

	e.log.Debug().
		Hex("block", logging.Entity(executableBlock)).
		Uint64("height", executableBlock.Block.Header.Height).
		Int("num_col", len(executableBlock.Block.Payload.Guarantees)).
		Int("actual_req", actualRequested).
		Msg("requested all collections")

	return nil
}

func (e *Engine) ExecuteScriptAtBlockID(ctx context.Context, script []byte, arguments [][]byte, blockID flow.Identifier) ([]byte, error) {

	stateCommit, err := e.execState.StateCommitmentByBlockID(ctx, blockID)
	if err != nil {
		return nil, fmt.Errorf("failed to get state commitment for block (%s): %w", blockID, err)
	}

	block, err := e.state.AtBlockID(blockID).Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get block (%s): %w", blockID, err)
	}

	blockView := e.execState.NewView(stateCommit)

	return e.computationManager.ExecuteScript(script, arguments, block, blockView)
}

func (e *Engine) GetAccount(ctx context.Context, addr flow.Address, blockID flow.Identifier) (*flow.Account, error) {
	stateCommit, err := e.execState.StateCommitmentByBlockID(ctx, blockID)
	if err != nil {
		return nil, fmt.Errorf("failed to get state commitment for block (%s): %w", blockID, err)
	}

	block, err := e.state.AtBlockID(blockID).Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get block (%s): %w", blockID, err)
	}

	blockView := e.execState.NewView(stateCommit)

	return e.computationManager.GetAccount(addr, block, blockView)
}

func (e *Engine) handleComputationResult(
	ctx context.Context,
	result *execution.ComputationResult,
	startState flow.StateCommitment,
) (flow.StateCommitment, error) {

	e.log.Debug().
		Hex("block_id", logging.Entity(result.ExecutableBlock)).
		Msg("received computation result")

	// There is one result per transaction
	e.metrics.ExecutionTotalExecutedTransactions(len(result.TransactionResult))

	receipt, err := e.saveExecutionResults(
		ctx,
		result.ExecutableBlock,
		result.StateSnapshots,
		result.Events,
		result.TransactionResult,
		startState,
	)

	if err != nil {
		return nil, err
	}

	err = e.providerEngine.BroadcastExecutionReceipt(ctx, receipt)
	if err != nil {
		return nil, fmt.Errorf("could not send broadcast order: %w", err)
	}

	finalState, ok := receipt.ExecutionResult.FinalStateCommitment()
	if !ok {
		finalState = startState
	}

	return finalState, nil
}

// save the execution result of a block
func (e *Engine) saveExecutionResults(
	ctx context.Context,
	executableBlock *entity.ExecutableBlock,
	stateInteractions []*delta.Snapshot,
	events []flow.Event,
	txResults []flow.TransactionResult,
	startState flow.StateCommitment,
) (*flow.ExecutionReceipt, error) {

	span, childCtx := e.tracer.StartSpanFromContext(ctx, trace.EXESaveExecutionResults)
	defer span.Finish()

	originalState := startState
	blockID := executableBlock.ID()

	err := e.execState.PersistStateInteractions(childCtx, blockID, stateInteractions)
	if err != nil && !errors.Is(err, storage.ErrAlreadyExists) {
		return nil, err
	}

	chunks := make([]*flow.Chunk, len(stateInteractions))

	// TODO: check current state root == startState
	var endState flow.StateCommitment = startState

	for i, view := range stateInteractions {
		// TODO: deltas should be applied to a particular state
		var err error
		endState, err = e.execState.CommitDelta(childCtx, view.Delta, startState)
		if err != nil {
			return nil, fmt.Errorf("failed to apply chunk delta: %w", err)
		}

		var collectionID flow.Identifier

		// account for system chunk being last
		if i < len(stateInteractions)-1 {
			collectionGuarantee := executableBlock.Block.Payload.Guarantees[i]
			completeCollection := executableBlock.CompleteCollections[collectionGuarantee.ID()]
			collectionID = completeCollection.Collection().ID()
		} else {
			collectionID = flow.ZeroID
		}

		chunk := generateChunk(i, startState, endState, collectionID, blockID)

		// chunkDataPack
		allRegisters := view.AllRegisters()

		proof, err := e.execState.GetProof(childCtx, chunk.StartState, allRegisters)

		if err != nil {
			return nil, fmt.Errorf(
				"error reading registers with proofs for chunk number [%v] of block [%x] ", i, blockID,
			)
		}

		chdp := generateChunkDataPack(chunk, collectionID, proof)

		err = e.execState.PersistChunkDataPack(childCtx, chdp)
		if err != nil {
			return nil, fmt.Errorf("failed to save chunk data pack: %w", err)
		}

		// TODO use view.SpockSecret() as an input to spock generator
		chunks[i] = chunk
		startState = endState
	}

	err = e.execState.PersistStateCommitment(childCtx, blockID, endState)
	if err != nil {
		return nil, fmt.Errorf("failed to store state commitment: %w", err)
	}

	executionResult, err := e.generateExecutionResultForBlock(childCtx, executableBlock.Block, chunks, endState)
	if err != nil {
		return nil, fmt.Errorf("could not generate execution result: %w", err)
	}

	receipt, err := e.generateExecutionReceipt(childCtx, executionResult, stateInteractions)
	if err != nil {
		return nil, fmt.Errorf("could not generate execution receipt: %w", err)
	}

	// not update the highest executed until the result and receipts are saved.
	// TODO: better to save result, receipt and the latest height in one transaction
	err = e.execState.UpdateHighestExecutedBlockIfHigher(childCtx, executableBlock.Block.Header)
	if err != nil {
		return nil, fmt.Errorf("failed to update highest executed block: %w", err)
	}

	err = func() error {
		span, _ := e.tracer.StartSpanFromContext(childCtx, trace.EXESaveTransactionEvents)
		defer span.Finish()

		if len(events) > 0 {
			err = e.events.Store(blockID, events)
			if err != nil {
				return fmt.Errorf("failed to store events: %w", err)
			}
		}
		return nil
	}()
	if err != nil {
		return nil, err
	}

	err = func() error {
		span, _ := e.tracer.StartSpanFromContext(childCtx, trace.EXESaveTransactionResults)
		defer span.Finish()

		err = e.transactionResults.BatchStore(blockID, txResults)
		if err != nil {
			return fmt.Errorf("failed to store transaction result error: %w", err)
		}
		return nil
	}()
	if err != nil {
		return nil, err
	}

	e.log.Debug().
		Hex("block_id", logging.Entity(executableBlock)).
		Hex("start_state", originalState).
		Hex("final_state", endState).
		Msg("saved computation results")

	return receipt, nil
}

// logExecutableBlock logs all data about an executable block
// over time we should skip this
func (e *Engine) logExecutableBlock(eb *entity.ExecutableBlock) {
	// log block
	e.log.Debug().
		Hex("block_id", logging.Entity(eb)).
		Hex("prev_block_id", logging.ID(eb.Block.Header.ParentID)).
		Uint64("block_height", eb.Block.Header.Height).
		Int("number_of_collections", len(eb.Collections())).
		RawJSON("block_header", logging.AsJSON(eb.Block.Header)).
		Msg("extensive log: block header")

	// logs transactions
	for i, col := range eb.Collections() {
		for j, tx := range col.Transactions {
			e.log.Debug().
				Hex("block_id", logging.Entity(eb)).
				Int("block_height", int(eb.Block.Header.Height)).
				Hex("prev_block_id", logging.ID(eb.Block.Header.ParentID)).
				Int("collection_index", i).
				Int("tx_index", j).
				Hex("collection_id", logging.ID(col.Guarantee.CollectionID)).
				Hex("tx_hash", logging.Entity(tx)).
				Hex("start_state_commitment", eb.StartState).
				RawJSON("transaction", logging.AsJSON(tx)).
				Msg("extensive log: executed tx content")
		}
	}
}

// generateChunk creates a chunk from the provided computation data.
func generateChunk(colIndex int,
	startState, endState flow.StateCommitment,
	colID, blockID flow.Identifier) *flow.Chunk {
	return &flow.Chunk{
		ChunkBody: flow.ChunkBody{
			CollectionIndex: uint(colIndex),
			StartState:      startState,
			// TODO: include real, event collection hash, currently using the collection ID to generate a different Chunk ID
			// Otherwise, the chances of there being chunks with the same ID before all these TODOs are done is large, since
			// startState stays the same if blocks are empty
			EventCollection: colID,
			BlockID:         blockID,
			// TODO: record gas used
			TotalComputationUsed: 0,
			// TODO: record number of txs
			NumberOfTransactions: 0,
		},
		Index:    0,
		EndState: endState,
	}
}

// generateExecutionResultForBlock creates new ExecutionResult for a block from
// the provided chunk results.
func (e *Engine) generateExecutionResultForBlock(
	ctx context.Context,
	block *flow.Block,
	chunks []*flow.Chunk,
	endState flow.StateCommitment,
) (*flow.ExecutionResult, error) {

	previousErID, err := e.execState.GetExecutionResultID(ctx, block.Header.ParentID)
	if err != nil {
		return nil, fmt.Errorf("could not get execution result ID for parent block (%v): %w",
			block.Header.ParentID, err)
	}

	er := &flow.ExecutionResult{
		ExecutionResultBody: flow.ExecutionResultBody{
			PreviousResultID: previousErID,
			BlockID:          block.ID(),
			Chunks:           chunks,
		},
	}

	return er, nil
}

func (e *Engine) generateExecutionReceipt(
	ctx context.Context,
	result *flow.ExecutionResult,
	stateInteractions []*delta.Snapshot,
) (*flow.ExecutionReceipt, error) {

	spocks := make([]crypto.Signature, len(stateInteractions))

	for i, stateInteraction := range stateInteractions {
		spock, err := e.me.SignFunc(stateInteraction.SpockSecret, e.spockHasher, crypto.SPOCKProve)

		if err != nil {
			return nil, fmt.Errorf("error while generating SPoCK: %w", err)
		}
		spocks[i] = spock
	}

	receipt := &flow.ExecutionReceipt{
		ExecutionResult:   *result,
		Spocks:            spocks,
		ExecutorSignature: crypto.Signature{},
		ExecutorID:        e.me.NodeID(),
	}

	// generates a signature over the execution result
	id := receipt.ID()
	sig, err := e.me.Sign(id[:], e.receiptHasher)
	if err != nil {
		return nil, fmt.Errorf("could not sign execution result: %w", err)
	}

	receipt.ExecutorSignature = sig

	err = e.execState.PersistExecutionReceipt(ctx, receipt)
	if err != nil && !errors.Is(err, storage.ErrAlreadyExists) {
		return nil, fmt.Errorf("could not persist execution result: %w", err)
	}

	return receipt, nil
}

func (e *Engine) isSyncingState() bool {
	syncHeight := e.syncingHeight.Load()
	return syncHeight > 0
}

func (e *Engine) stopSyncing(syncingHeight uint64) bool {
	stopped := e.syncingHeight.CAS(syncingHeight, 0)
	return stopped
}

func (e *Engine) startSyncing(syncHeight uint64) bool {
	return e.syncingHeight.CAS(0, syncHeight)
}

// check whether we need to trigger state sync
// firstUnexecutedHeight - the height that is unexecuted
// we will check the state sync if the number of sealed and unexecuted blocks
// has passed a certain threshold.
// we will sync state only for sealed blocks, since that guarantees
// the consensus nodes have seen the result, and the statecommitment
// has been approved by the consensus nodes.
func (e *Engine) checkStateSyncStart(firstUnexecutedHeight uint64) {
	isSyncing := e.isSyncingState()
	if isSyncing {
		// state sync is already triggered, no need to check
		return
	}

	// getting the blocks for determining whether to trigger.
	// the queue head has the lowest height, which is also the first unexecuted block
	lastSealed, err := e.state.Sealed().Head()
	if err != nil {
		e.log.Fatal().Err(err).Msg("failed to query last sealed")
	}

	startHeight, endHeight := firstUnexecutedHeight, lastSealed.Height

	// check whether we should trigger state sync
	trigger := shouldTriggerStateSync(startHeight, endHeight, e.syncThreshold)

	if !trigger {
		return
	}

	err = e.startStateSync(startHeight, endHeight)
	if err != nil {
		e.log.Error().
			Err(err).
			Uint64("from", startHeight).
			Uint64("to", endHeight).Msg("failed to start state sync")
	}
}

// if the state sync is on, check whether it can be turned off by checking
// whether the executed block has passed the target height.
func (e *Engine) checkStateSyncStop(executedHeight uint64) {
	syncHeight := e.syncingHeight.Load()
	if syncHeight == 0 {
		// state sync was not started
		return
	}

	reachedSyncTarget := executedHeight >= syncHeight

	if !reachedSyncTarget {
		// have not reached sync target
		return
	}

	// reached the sync target, we should turn off the syncing
	stopped := e.stopSyncing(syncHeight)
	if stopped {
		e.metrics.ExecutionSync(false)
	}

	// if there is race condition that the syncState was
	// changed to a different value, this will be a noop,
	// and we will wait for the next time to call checkStateSyncStop
	// and check again.
}

// check whether state sync should be triggered by taking
// the start and end heights for sealed and unexecuted blocks,
// as well as a threshold
// if the threshold is 10, it means if there are 10 sealed but unexecuted blocks,
// the state sync will be trigger. So for instance, if the first sealed and unexecuted
// block's height is 20, then the state sync will not trigger until the last sealed and
// unexecuted block's height is higher than or equal to than 29.
func shouldTriggerStateSync(startHeight, endHeight uint64, threshold int) bool {
	return int64(endHeight)-int64(startHeight)+1 >= int64(threshold)
}

func (e *Engine) startStateSync(fromHeight, toHeight uint64) error {
	started := e.startSyncing(toHeight)
	if !started {
		// some other process has already entered the startStateSync
		return nil
	}

	e.metrics.ExecutionSync(true)

	otherNodes, err := e.state.Final().Identities(
		filter.And(filter.HasRole(flow.RoleExecution), e.me.NotMeFilter(), e.syncFilter))

	if err != nil {
		return fmt.Errorf("error while finding other execution nodes identities")
	}

	if len(otherNodes) == 0 {
		e.log.Error().Msg("no available execution node to sync state from")
		e.stopSyncing(toHeight)
		return nil
	}

	// randomly choose an execution node to sync state from,
	// use syncFilter to sync from a specific execution node
	randomExecutionNode := otherNodes[rand.Intn(len(otherNodes))]

	exeStateReq := messages.ExecutionStateSyncRequest{
		FromHeight: fromHeight,
		ToHeight:   toHeight,
	}

	e.log.Info().
		Hex("target_node", logging.Entity(randomExecutionNode)).
		Uint64("from", fromHeight).
		Uint64("to", toHeight).
		Msg("state sync triggered, requesting execution state deltas")

	// TODO: there is a chance the randomly picked execution node is also behind,
	// better to retry state syncing request with another node if we haven't
	// reached the targeted height after a while.
	// for now, we could also rely on the syncFilter to force syncing from a
	// specific node.
	err = e.syncConduit.Unicast(&exeStateReq, randomExecutionNode.NodeID)

	if err != nil {
		return fmt.Errorf("error while sending state sync req to other node (%v): %w",
			randomExecutionNode,
			err)
	}

	return nil
}

// handle the state sync request from other execution.
// the state sync requests are for sealed blocks.
// we will check if the requested heights have been sealed and
// executed, return return the state deltas as much as possible.
func (e *Engine) handleStateSyncRequest(
	originID flow.Identifier,
	req *messages.ExecutionStateSyncRequest) error {

	// the request must be from an execution node
	id, err := e.state.Final().Identity(originID)
	if err != nil {
		return fmt.Errorf("invalid origin id (%s): %w", id, err)
	}

	// TODO: restrict the sender has to be an execution node.
	// if id.Role != flow.RoleExecution {
	// 	return fmt.Errorf("invalid role for requesting state synchronization: %v, %s", originID, id.Role)
	// }

	// validate that from height must be smaller than to height
	if req.FromHeight >= req.ToHeight {
		return engine.NewInvalidInputErrorf("invalid state sync request (from: %x, to: %d)",
			req.FromHeight, req.ToHeight)
	}

	lastSealed, err := e.state.Sealed().Head()
	if err != nil {
		return fmt.Errorf("could not get last sealed: %w", err)
	}

	sealedHeight := lastSealed.Height

	log := e.log.With().
		Hex("sender", originID[:]).
		Uint64("sealed", sealedHeight).
		Uint64("from", req.FromHeight).
		Uint64("to", req.ToHeight).
		Logger()

	// ignore requests for unsealed height
	if req.FromHeight > sealedHeight {
		log.Info().Msg("receives state sync requests for unsealed height, ignore")
		return nil
	}

	// fromHeight, toHeight must be sealed height
	fromHeight, toHeight := req.FromHeight, req.ToHeight
	if toHeight > sealedHeight {
		toHeight = sealedHeight
	}

	// for each height starting from fromHeight to toHeight,
	// query the statecommitment, and if exists, send the
	// state delta
	// TOOD: add context
	ctx := e.unit.Ctx()
	err = e.deltaRange(ctx, fromHeight, toHeight,
		func(delta *messages.ExecutionStateDelta) {
			err := e.syncConduit.Unicast(delta, originID)
			if err != nil {
				e.log.Error().Err(err).Msg("could not submit block delta")
			}
		})

	if err != nil {
		return fmt.Errorf("could not send deltas: %w", err)
	}

	log.Info().Msg("responded state deltas for a height range")

	return nil
}

// deltaRange querys it's local execution state to find deltas for a height
// range between the fromHeight to the toHeight. If delta is found, then
// pass it to the onDelta callback.
func (e *Engine) deltaRange(ctx context.Context, fromHeight uint64, toHeight uint64,
	onDelta func(*messages.ExecutionStateDelta)) error {

	for height := fromHeight; height <= toHeight; height++ {
		header, err := e.state.AtHeight(height).Head()
		if err != nil {
			return fmt.Errorf("could not query block header at height: %v", height)
		}

		blockID := header.ID()
		_, err = e.execState.StateCommitmentByBlockID(ctx, blockID)

		if err == nil {
			// this block has been executed, we will send the delta
			delta, err := e.execState.RetrieveStateDelta(ctx, blockID)
			if err != nil {
				return fmt.Errorf("could not retrieve state delta for block %v, %w", blockID, err)
			}

			onDelta(delta)

		} else if errors.Is(err, storage.ErrNotFound) {
			// this block has not been executed,
			// it parent block hasn't been executed, the higher block won't be
			// executed either, so we stop iterating through the heights
			break
		} else {
			return fmt.Errorf("could not query statecommitment for height %v: %w", height, err)
		}
	}

	return nil
}

func (e *Engine) handleStateDeltaResponse(executionNodeID flow.Identifier, delta *messages.ExecutionStateDelta) error {
	log := e.log.With().
		Hex("sender", executionNodeID[:]).
		Hex("block_id", logging.Entity(delta)).
		Uint64("height", delta.ExecutableBlock.Block.Header.Height).
		Logger()

	log.Debug().Msg("received state delta")

	// the request must be from an execution node
	id, err := e.state.Final().Identity(executionNodeID)
	if err != nil {
		return fmt.Errorf("invalid origin id (%s): %w", id, err)
	}

	if id.Role != flow.RoleExecution {
		return fmt.Errorf("invalid role for sending state deltas: %v, %s", executionNodeID, id.Role)
	}

	// check if the block has been executed already
	// delta ID is block ID
	blockID := delta.ID()
	_, err = e.execState.StateCommitmentByBlockID(e.unit.Ctx(), blockID)

	if err == nil {
		// the block has been executed, ignore
		e.log.Info().Hex("block", logging.Entity(delta)).Msg("ignore executed state delta")
		return nil
	}

	// exception
	if !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("could not get know block was executed or not: %w", err)
	}

	// block not executed yet, check if the block has been sealed
	lastSealed, err := e.state.Sealed().Head()
	if err != nil {
		return fmt.Errorf("failed to query last sealed height")
	}

	blockHeight := delta.ExecutableBlock.Block.Header.Height
	isUnsealed := blockHeight > lastSealed.Height

	if isUnsealed {
		// we never query delta for unsealed blocks, ignore
		log.Debug().Msg("ignore state deltas for unsealed blocks")
		return nil
	}

	err = e.validateStateDelta(delta)
	if err != nil {
		return fmt.Errorf("failed to validate the state delta: %w", err)
	}

	e.syncDeltas.Add(delta)

	// since the delta includes collections, we could just trigger the
	// handleCollection for those collections, which will check if the
	// block is executable and apply deltas to them.
	//
	// calling handleCollection could also ensures the collection are
	// stored in storage before applying the delta.
	for _, cc := range delta.ExecutableBlock.CompleteCollections {
		col := cc.Collection()
		// note, we will be passing execution node id to handleCollection
		err = e.handleCollection(executionNodeID, &col)
		if err != nil {
			return fmt.Errorf("failed to handle collection of the deltas: %w",
				err)
		}
	}

	// if a block has no collection, then try executing the block
	if len(delta.ExecutableBlock.CompleteCollections) == 0 {
		err = e.mempool.Run(
			func(
				blockByCollection *stdmap.BlockByCollectionBackdata,
				executionQueues *stdmap.QueuesBackdata,
			) error {
				// check if the delta is for the first unexecuted block
				// in a queue. Note if the block is not the first, then
				// we can't execute it until its parent has been executed.
				for _, queue := range executionQueues.All() {
					if queue.Head.Item.ID() == blockID {
						block := queue.Head.Item.(*entity.ExecutableBlock)
						e.executeBlockIfComplete(block)
						break
					}
				}

				return nil
			})
		if err != nil {
			return fmt.Errorf("failed to handle state delta: %w", err)
		}
	}

	log.Info().Msg("stored state delta")
	return nil
}

func (e *Engine) validateStateDelta(delta *messages.ExecutionStateDelta) error {
	// must match the statecommitment for parent block
	parentCommitment, err := e.execState.StateCommitmentByBlockID(e.unit.Ctx(), delta.ParentID())
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("could not get parent sttecommitment: %w", err)
	}

	// the parent block has not been executed yet, skip
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}

	if !bytes.Equal(parentCommitment, delta.StartState) {
		return engine.NewInvalidInputErrorf("internal inconsistency with delta for block (%v) - state commitment for parent retrieved from DB (%x) different from start state in delta! (%x)",
			delta.ParentID(),
			parentCommitment,
			delta.StartState)
	}

	// TODO: validate the delta with the child block's statecommitment

	return nil
}

func (e *Engine) applyStateDelta(delta *messages.ExecutionStateDelta) {
	blockID := delta.ID()
	log := e.log.With().Hex("block", blockID[:]).Logger()

	log.Debug().Msg("applying delta for block")

	// TODO - validate state delta, reject invalid messages

	executionReceipt, err := e.saveExecutionResults(
		e.unit.Ctx(),
		&delta.ExecutableBlock,
		delta.StateInteractions,
		delta.Events,
		delta.TransactionResults,
		delta.StartState,
	)

	if err != nil {
		log.Fatal().Err(err).Msg("fatal error while processing sync message")
	}

	finalState, ok := executionReceipt.ExecutionResult.FinalStateCommitment()
	if !ok {
		// set to start state next line will fail anyways
		finalState = delta.StartState
	}

	if !bytes.Equal(finalState, delta.EndState) {
		log.Error().
			Hex("saved_state", finalState).
			Hex("delta_end_state", delta.EndState).
			Hex("delta_start_state", delta.StartState).
			Err(err).Msg("processing sync message produced unexpected state commitment")
		return
	}

	err = e.onBlockExecuted(&delta.ExecutableBlock, delta.EndState)
	if err != nil {
		log.Error().Err(err).Msg("onBlockExecuted failed")
		return
	}

	log.Info().Msg("block has been executed successfully from applying state deltas")
}

// generateChunkDataPack creates a chunk data pack
func generateChunkDataPack(
	chunk *flow.Chunk,
	collectionID flow.Identifier,
	proof flow.StorageProof,
) *flow.ChunkDataPack {
	return &flow.ChunkDataPack{
		ChunkID:      chunk.ID(),
		StartState:   chunk.StartState,
		Proof:        proof,
		CollectionID: collectionID,
	}
}
