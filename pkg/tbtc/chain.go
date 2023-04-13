package tbtc

import (
	"crypto/ecdsa"
	"math/big"
	"time"

	"github.com/keep-network/keep-core/pkg/bitcoin"

	"github.com/keep-network/keep-core/pkg/chain"
	"github.com/keep-network/keep-core/pkg/operator"
	"github.com/keep-network/keep-core/pkg/protocol/group"
	"github.com/keep-network/keep-core/pkg/sortition"
	"github.com/keep-network/keep-core/pkg/subscription"
	"github.com/keep-network/keep-core/pkg/tecdsa/dkg"
)

type DKGState int

const (
	Idle DKGState = iota
	AwaitingSeed
	AwaitingResult
	Challenge
)

// GroupSelectionChain defines the subset of the TBTC chain interface that
// pertains to the group selection activities.
type GroupSelectionChain interface {
	// SelectGroup returns the group members selected for the current group
	// selection. The function returns an error if the chain's state does not
	// allow for group selection at the moment.
	SelectGroup() (*GroupSelectionResult, error)
}

// GroupSelectionResult represents a group selection result, i.e. operators
// selected to perform the DKG protocol. The result consists of two slices
// of equal length holding the chain.OperatorID and chain.Address for each
// selected operator.
type GroupSelectionResult struct {
	OperatorsIDs       chain.OperatorIDs
	OperatorsAddresses chain.Addresses
}

// DistributedKeyGenerationChain defines the subset of the TBTC chain
// interface that pertains specifically to group formation's distributed key
// generation process.
type DistributedKeyGenerationChain interface {
	// OnDKGStarted registers a callback that is invoked when an on-chain
	// notification of the DKG process start is seen.
	OnDKGStarted(
		func(event *DKGStartedEvent),
	) subscription.EventSubscription

	// PastDKGStartedEvents fetches past DKG started events according to the
	// provided filter or unfiltered if the filter is nil. Returned events
	// are sorted by the block number in the ascending order, i.e. the latest
	// event is at the end of the slice.
	PastDKGStartedEvents(
		filter *DKGStartedEventFilter,
	) ([]*DKGStartedEvent, error)

	// OnDKGResultSubmitted registers a callback that is invoked when an on-chain
	// notification of the DKG result submission is seen.
	OnDKGResultSubmitted(
		func(event *DKGResultSubmittedEvent),
	) subscription.EventSubscription

	// OnDKGResultChallenged registers a callback that is invoked when an
	// on-chain notification of the DKG result challenge is seen.
	OnDKGResultChallenged(
		func(event *DKGResultChallengedEvent),
	) subscription.EventSubscription

	// OnDKGResultApproved registers a callback that is invoked when an on-chain
	// notification of the DKG result approval is seen.
	OnDKGResultApproved(
		func(event *DKGResultApprovedEvent),
	) subscription.EventSubscription

	// AssembleDKGResult assembles the DKG chain result according to the rules
	// expected by the given chain.
	AssembleDKGResult(
		submitterMemberIndex group.MemberIndex,
		groupPublicKey *ecdsa.PublicKey,
		operatingMembersIndexes []group.MemberIndex,
		misbehavedMembersIndexes []group.MemberIndex,
		signatures map[group.MemberIndex][]byte,
		groupSelectionResult *GroupSelectionResult,
	) (*DKGChainResult, error)

	// SubmitDKGResult submits the DKG result to the chain.
	SubmitDKGResult(dkgResult *DKGChainResult) error

	// GetDKGState returns the current state of the DKG procedure.
	GetDKGState() (DKGState, error)

	// CalculateDKGResultSignatureHash calculates a 32-byte hash that is used
	// to produce a signature supporting the given groupPublicKey computed
	// as result of the given DKG process. The misbehavedMembersIndexes parameter
	// should contain indexes of members that were considered as misbehaved
	// during the DKG process. The startBlock argument is the block at which
	// the given DKG process started.
	CalculateDKGResultSignatureHash(
		groupPublicKey *ecdsa.PublicKey,
		misbehavedMembersIndexes []group.MemberIndex,
		startBlock uint64,
	) (dkg.ResultSignatureHash, error)

	// IsDKGResultValid checks whether the submitted DKG result is valid from
	// the on-chain contract standpoint.
	IsDKGResultValid(dkgResult *DKGChainResult) (bool, error)

	// ChallengeDKGResult challenges the submitted DKG result.
	ChallengeDKGResult(dkgResult *DKGChainResult) error

	// ApproveDKGResult approves the submitted DKG result.
	ApproveDKGResult(dkgResult *DKGChainResult) error

	// DKGParameters gets the current value of DKG-specific control parameters.
	DKGParameters() (*DKGParameters, error)
}

// DKGChainResultHash represents a hash of the DKGChainResult. The algorithm
// used is specific to the chain.
type DKGChainResultHash [32]byte

// DKGChainResult represents a DKG result submitted to the chain.
type DKGChainResult struct {
	SubmitterMemberIndex     group.MemberIndex
	GroupPublicKey           []byte
	MisbehavedMembersIndexes []group.MemberIndex
	Signatures               []byte
	SigningMembersIndexes    []group.MemberIndex
	Members                  chain.OperatorIDs
	MembersHash              [32]byte
}

// DKGStartedEvent represents a DKG start event.
type DKGStartedEvent struct {
	Seed        *big.Int
	BlockNumber uint64
}

// DKGStartedEventFilter is a component allowing to filter DKGStartedEvent.
type DKGStartedEventFilter struct {
	StartBlock uint64
	EndBlock   *uint64
	Seed       []*big.Int
}

// DKGResultSubmittedEvent represents a DKG result submission event. It is
// emitted after a submitted DKG result lands on the chain.
type DKGResultSubmittedEvent struct {
	Seed        *big.Int
	ResultHash  DKGChainResultHash
	Result      *DKGChainResult
	BlockNumber uint64
}

// DKGResultChallengedEvent represents a DKG result challenge event. It is
// emitted after a submitted DKG result is challenged as an invalid result.
type DKGResultChallengedEvent struct {
	ResultHash  DKGChainResultHash
	Challenger  chain.Address
	Reason      string
	BlockNumber uint64
}

// DKGResultApprovedEvent represents a DKG result approval event. It is
// emitted after a submitted DKG result is approved as a valid result.
type DKGResultApprovedEvent struct {
	ResultHash  DKGChainResultHash
	Approver    chain.Address
	BlockNumber uint64
}

// DKGParameters contains values of DKG-specific control parameters.
type DKGParameters struct {
	SubmissionTimeoutBlocks       uint64
	ChallengePeriodBlocks         uint64
	ApprovePrecedencePeriodBlocks uint64
}

// BridgeChain defines the subset of the TBTC chain interface that pertains
// specifically to the tBTC Bridge operations.
type BridgeChain interface {
	// OnHeartbeatRequested registers a callback that is invoked when an
	// on-chain notification of the Bridge heartbeat request is seen.
	OnHeartbeatRequested(
		func(event *HeartbeatRequestedEvent),
	) subscription.EventSubscription
}

// HeartbeatRequestedEvent represents a Bridge heartbeat request event.
type HeartbeatRequestedEvent struct {
	WalletPublicKey []byte
	Messages        []*big.Int
	BlockNumber     uint64
}

// WalletCoordinatorChain defines the subset of the TBTC chain interface that
// pertains specifically to the tBTC wallet coordination.
type WalletCoordinatorChain interface {
	// OnDepositSweepProposalSubmitted registers a callback that is invoked when
	// an on-chain notification of the deposit sweep proposal submission is seen.
	OnDepositSweepProposalSubmitted(
		func(event *DepositSweepProposalSubmittedEvent),
	) subscription.EventSubscription

	// PastDepositSweepProposalSubmittedEvents fetches past deposit sweep
	// proposal events according to the provided filter or unfiltered if the
	// filter is nil. Returned events are sorted by the block number in the
	// ascending order, i.e. the latest event is at the end of the slice.
	PastDepositSweepProposalSubmittedEvents(
		filter *DepositSweepProposalSubmittedEventFilter,
	) ([]*DepositSweepProposalSubmittedEvent, error)

	// GetWalletLock gets the current wallet lock for the given wallet.
	// Returned values represent the expiration time and the cause of the lock.
	// The expiration time can be UNIX timestamp 0 which means there is no lock
	// on the wallet at the given moment.
	GetWalletLock(
		walletPublicKeyHash [20]byte,
	) (time.Time, WalletAction, error)

	// ValidateDepositSweepProposal validates the given deposit sweep proposal
	// against the chain. It requires some additional data about the deposits
	// that must be fetched externally. Returns true if the given proposal
	// is valid and an error otherwise.
	ValidateDepositSweepProposal(
		proposal *DepositSweepProposal,
		depositsExtraInfo []struct {
			fundingTx        *bitcoin.Transaction
			BlindingFactor   [8]byte
			WalletPubKeyHash [20]byte
			RefundPubKeyHash [20]byte
			RefundLocktime   [4]byte
		},
	) (bool, error)
}

// DepositSweepProposal represents a deposit sweep proposal submitted to the chain.
type DepositSweepProposal struct {
	WalletPubKeyHash [20]byte
	DepositsKeys     []struct {
		FundingTxHash      bitcoin.Hash
		FundingOutputIndex uint32
	}
	SweepTxFee *big.Int
}

// DepositSweepProposalSubmittedEvent represents a deposit sweep proposal
// submission event.
type DepositSweepProposalSubmittedEvent struct {
	Proposal          *DepositSweepProposal
	ProposalSubmitter chain.Address
	BlockNumber       uint64
}

// DepositSweepProposalSubmittedEventFilter is a component allowing to
// filter DepositSweepProposalSubmittedEvent.
type DepositSweepProposalSubmittedEventFilter struct {
	StartBlock          uint64
	EndBlock            *uint64
	ProposalSubmitter   []chain.Address
	WalletPublicKeyHash [20]byte
}

// Chain represents the interface that the TBTC module expects to interact
// with the anchoring blockchain on.
type Chain interface {
	// BlockCounter returns the chain's block counter.
	BlockCounter() (chain.BlockCounter, error)
	// Signing returns the chain's signer.
	Signing() chain.Signing
	// OperatorKeyPair returns the key pair of the operator assigned to this
	// chain handle.
	OperatorKeyPair() (*operator.PrivateKey, *operator.PublicKey, error)

	sortition.Chain
	GroupSelectionChain
	DistributedKeyGenerationChain
	BridgeChain
	WalletCoordinatorChain
}
