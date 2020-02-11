package hotstuff

import (
	"github.com/dapperlabs/flow-go/engine/consensus/hotstuff/types"
	"github.com/dapperlabs/flow-go/model/flow"
)

// Communicator defines how the HotStuff core algorithm communicates with other
// actors participating in the consensus process.
type Communicator interface {

	// SendVote sends the given vote to the given node.
	SendVote(vote *types.Vote, to flow.Identifier) error

	// BroadcastProposal sends the given block proposal to all nodes
	// participating in the consensus process.
	BroadcastProposal(proposal *flow.Header) error
}