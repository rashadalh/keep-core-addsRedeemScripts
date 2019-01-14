package chain

import (
	"math/big"

	"github.com/keep-network/keep-core/pkg/beacon/relay/config"
	"github.com/keep-network/keep-core/pkg/beacon/relay/event"
	"github.com/keep-network/keep-core/pkg/beacon/relay/groupselection"
	"github.com/keep-network/keep-core/pkg/gen/async"
)

// RelayEntryInterface defines the subset of the relay chain interface that
// pertains specifically to submission and retrieval of relay requests and
// entries.
type RelayEntryInterface interface {
	// RequestRelayEntry makes an on-chain request to start generation of a
	// random signature.  An event is generated.
	RequestRelayEntry(blockReward, seed *big.Int) *async.RelayRequestPromise
	// SubmitRelayEntry submits an entry in the threshold relay and returns a
	// promise to track the submission result. The promise is fulfilled with
	// the entry as seen on-chain, or failed if there is an error submitting
	// the entry.
	SubmitRelayEntry(entry *event.Entry) *async.RelayEntryPromise

	// OnRelayEntryGenerated is a callback that is invoked when an on-chain
	// notification of a new, valid relay entry is seen.
	OnRelayEntryGenerated(func(entry *event.Entry))
	// OnRelayEntryRequested is a callback that is invoked when an on-chain
	// notification of a new, valid relay request is seen.
	OnRelayEntryRequested(func(request *event.Request))
}

// GroupInterface defines the subset of the relay chain interface that pertains
// specifically to relay group management.
type GroupInterface interface {
	// SubmitGroupPublicKey submits a 96-byte BLS public key to the blockchain,
	// associated with a request with id requestID. On-chain errors are reported
	// through the promise.
	SubmitGroupPublicKey(requestID *big.Int, key [96]byte) *async.GroupRegistrationPromise
	// OnGroupRegistered is a callback that is invoked when an on-chain
	// notification of a new, valid group being registered is seen.
	OnGroupRegistered(func(key *event.GroupRegistration))
	// SubmitTicket submits a ticket corresponding to the virtual staker to
	// the chain, and returns a promise to track the submission. The promise
	// is fulfilled with the entry as seen on-chain, or failed if there is an
	// error submitting the entry.
	SubmitTicket(ticket *groupselection.Ticket) *async.GroupTicketPromise
	// SubmitChallenge submits a challenge corresponding to a ticket that
	// fails `costlyCheck`, and returns a promise to track the challenge
	// submission. The promise is fulfilled with the challenge as seen on-chain,
	// or failed if there is an error submitting the entry.
	SubmitChallenge(ticket *groupselection.TicketChallenge) *async.GroupTicketChallengePromise
	// OnGroupSelectionResult is a callback that is invoked when the final
	// phase of group selection has been completed on-chain, and the chain
	// emits a notification of an ordered list of seletected tickets.
	// These tickets represent the stakers eligble to form the next group.
	OnGroupSelectionResult(func(result *groupselection.Result))
	// GetOrderedTickets returns submitted tickets which have passed checks
	// on-chain.
	GetOrderedTickets() []*groupselection.Ticket
}

// DistributedKeyGenerationInterface defines the subset of the relay chain
// interface that pertains specifically to group formation's distributed key
// generation process.
type DistributedKeyGenerationInterface interface {
	// SubmitDKGResult sends DKG result to a chain.
	SubmitDKGResult(requestID *big.Int, dkgResult *DKGResult) *async.DKGResultPublicationPromise
	// OnDKGResultPublished is a callback that is invoked when an on-chain
	// notification of a new, valid published result is seen.
	OnDKGResultPublished(func(dkgResultPublication *event.DKGResultPublication)) event.Subscription
	// IsDKGResultPublished checks if any DKG result has already been published
	// to a chain for the given request ID.
	IsDKGResultPublished(requestID *big.Int) (bool, error)
}

// Interface represents the interface that the relay expects to interact with
// the anchoring blockchain on.
type Interface interface {
	// GetConfig returns the expected configuration of the threshold relay.
	GetConfig() (config.Chain, error)

	GroupInterface
	RelayEntryInterface
	DistributedKeyGenerationInterface
}
