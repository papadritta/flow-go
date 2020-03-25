// (c) 2019 Dapper Labs - ALL RIGHTS RESERVED

package ingestion

import (
	"errors"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/dapperlabs/flow-go/engine"
	"github.com/dapperlabs/flow-go/model/flow"
	"github.com/dapperlabs/flow-go/model/flow/filter"
	"github.com/dapperlabs/flow-go/model/messages"
	"github.com/dapperlabs/flow-go/module"
	"github.com/dapperlabs/flow-go/module/trace"
	"github.com/dapperlabs/flow-go/network"
	"github.com/dapperlabs/flow-go/protocol"
	"github.com/dapperlabs/flow-go/storage"
	"github.com/dapperlabs/flow-go/utils/logging"
)

// Engine represents the ingestion engine, used to funnel data from other nodes
// to a centralized location that can be queried by a user
type Engine struct {
	unit   *engine.Unit   // used to manage concurrency & shutdown
	log    zerolog.Logger // used to log relevant actions with context
	tracer trace.Tracer   // used to trace the data
	state  protocol.State // used to access the  protocol state
	me     module.Local   // used to access local node information

	// Conduits
	collectionConduit network.Conduit

	// storage
	collections  storage.Collections
	transactions storage.Transactions
}

// New creates a new observation ingestion engine
func New(log zerolog.Logger,
	net module.Network,
	state protocol.State,
	tracer trace.Tracer,
	me module.Local,
	collections storage.Collections,
	transactions storage.Transactions) (*Engine, error) {

	// initialize the propagation engine with its dependencies
	eng := &Engine{
		unit:         engine.NewUnit(),
		log:          log.With().Str("engine", "ingestion").Logger(),
		tracer:       tracer,
		state:        state,
		me:           me,
		collections:  collections,
		transactions: transactions,
	}

	collConduit, err := net.Register(engine.CollectionProvider, eng)
	if err != nil {
		return nil, fmt.Errorf("could not register collection provider engine: %w", err)
	}

	eng.collectionConduit = collConduit

	return eng, nil
}

// Ready returns a ready channel that is closed once the engine has fully
// started. For the ingestion engine, we consider the engine up and running
// upon initialization.
func (e *Engine) Ready() <-chan struct{} {
	return e.unit.Ready()
}

// Done returns a done channel that is closed once the engine has fully stopped.
// For the ingestion engine, it only waits for all submit goroutines to end.
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
			e.log.Error().Err(err).Msg("could not process submitted event")
		}
	})
}

// ProcessLocal processes an event originating on the local node.
func (e *Engine) ProcessLocal(event interface{}) error {
	return e.Process(e.me.NodeID(), event)
}

// Process processes the given event from the node with the given origin ID in
// a blocking manner. It returns the potential processing error when done.
func (e *Engine) Process(originID flow.Identifier, event interface{}) error {
	return e.unit.Do(func() error {
		return e.process(originID, event)
	})
}

// process processes the given ingestion engine event. Events that are given
// to this function originate within the expulsion engine on the node with the
// given origin ID.
func (e *Engine) process(originID flow.Identifier, event interface{}) error {
	switch entity := event.(type) {
	case *flow.Block:
		return e.onBlock(originID, entity)
	case *messages.CollectionResponse:
		return e.handleCollectionResponse(originID, entity)
	case *flow.CollectionGuarantee:
		return e.onCollectionGuarantee(originID, entity)
	default:
		return fmt.Errorf("invalid event type (%T)", event)
	}
}

// onBlock handles an incoming block.
// TODO this will be an event triggered by the follower node when a new finalized or sealed block is received
func (e *Engine) onBlock(originID flow.Identifier, block *flow.Block) error {

	e.log.Info().
		Hex("origin_id", originID[:]).
		Hex("block_id", logging.Entity(block)).
		Uint64("block_view", block.View).
		Msg("received block")

	return e.requestCollections(block.Guarantees...)
}

// handleCollectionResponse handles the response of the a collection request made earlier when a block was received
func (e *Engine) handleCollectionResponse(originID flow.Identifier, response *messages.CollectionResponse) error {
	collection := response.Collection
	light := collection.Light()

	// store the light collection (collection minus the transaction body - those are stored separately)
	// and add transaction ids as index
	err := e.collections.StoreLightAndIndexByTransaction(&light)
	if err != nil {
		// ignore collection if already seen
		if errors.Is(err, storage.ErrAlreadyExists) {
			return nil
		}
		return err
	}

	// now store each of the transaction body
	for _, tx := range collection.Transactions {
		err := e.transactions.Store(tx)
		if err != nil {
			return err
		}
	}

	return nil
}

// onCollectionGuarantee is used to process collection guarantees received
// from nodes that are not consensus nodes (notably collection nodes).
func (e *Engine) onCollectionGuarantee(originID flow.Identifier, guarantee *flow.CollectionGuarantee) error {

	e.log.Info().
		Hex("origin_id", originID[:]).
		Hex("collection_id", logging.Entity(guarantee)).
		Msg("collection guarantee received")

	// get the identity of the origin node, so we can check if it's a valid
	// source for a collection guarantee (usually collection nodes)
	id, err := e.state.Final().Identity(originID)
	if err != nil {
		return fmt.Errorf("could not get origin node identity: %w", err)
	}

	// check that the origin is a collection node; this check is fine even if it
	// excludes our own ID - in the case of local submission of collections, we
	// should use the propagation engine, which is for exchange of collections
	// between consensus nodes anyway; we do no processing or validation in this
	// engine beyond validating the origin
	if id.Role != flow.RoleCollection {
		return fmt.Errorf("invalid origin node role (%s)", id.Role)
	}

	return e.requestCollections(guarantee)
}

func (e *Engine) requestCollections(guarantees ...*flow.CollectionGuarantee) error {
	ids, err := e.findCollectionNodes()
	if err != nil {
		return err
	}

	// Request all the collections for this block
	for _, g := range guarantees {
		err := e.collectionConduit.Submit(&messages.CollectionRequest{ID: g.ID()}, ids...)
		if err != nil {
			return err
		}
	}

	return nil

}

func (e *Engine) findCollectionNodes() ([]flow.Identifier, error) {
	identities, err := e.state.Final().Identities(filter.HasRole(flow.RoleCollection))
	if err != nil {
		return nil, fmt.Errorf("could not retrieve identities: %w", err)
	}
	if len(identities) < 1 {
		return nil, fmt.Errorf("no Collection identity found")
	}
	identifiers := flow.GetIDs(identities)
	return identifiers, nil
}