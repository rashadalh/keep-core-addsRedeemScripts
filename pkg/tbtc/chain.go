package tbtc

import (
	"github.com/keep-network/keep-core/pkg/chain"
	"github.com/keep-network/keep-core/pkg/operator"
	"github.com/keep-network/keep-core/pkg/sortition"
	"github.com/keep-network/keep-core/pkg/subscription"
	"math/big"
)

// GroupSelectionChain defines the subset of the TBTC chain interface that
// pertains to the group selection activities.
type GroupSelectionChain interface {
	// SelectGroup returns the group members for the group generated by
	// the given seed. This function can return an error if the beacon chain's
	// state does not allow for group selection at the moment.
	SelectGroup(seed *big.Int) ([]chain.Address, error)
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
}

// DKGStartedEvent represents a DKG start event.
type DKGStartedEvent struct {
	Seed        *big.Int
	BlockNumber uint64
}

// Chain represents the interface that the TBTC module expects to interact
// with the anchoring blockchain on.
type Chain interface {
	// GetConfig returns the expected configuration of the TBTC module.
	GetConfig() *ChainConfig
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
}

// ChainConfig contains the config data needed for the TBTC to operate.
type ChainConfig struct {
	// GroupSize is the size of a group in TBTC.
	GroupSize int
	// HonestThreshold is the minimum number of active participants behaving
	// according to the protocol needed to generate a signature.
	HonestThreshold int
}

// DishonestThreshold is the maximum number of misbehaving participants for
// which it is still possible to generate a signature.
// Misbehaviour is any misconduct to the protocol, including inactivity.
func (cc *ChainConfig) DishonestThreshold() int {
	return cc.GroupSize - cc.HonestThreshold
}
