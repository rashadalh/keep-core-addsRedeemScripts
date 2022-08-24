package tbtc

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"

	"github.com/keep-network/keep-common/pkg/persistence"
	"github.com/keep-network/keep-core/pkg/chain"
	"github.com/keep-network/keep-core/pkg/generator"
	"github.com/keep-network/keep-core/pkg/internal/testutils"
	"github.com/keep-network/keep-core/pkg/net"
	"github.com/keep-network/keep-core/pkg/protocol/group"
	"github.com/keep-network/keep-core/pkg/tecdsa/dkg"
)

// TODO: Unit tests for `node.go`.
// TODO: Extract the DKG-specific code into a separate file `pkg/tbtc/dkg.go`

// node represents the current state of an ECDSA node.
type node struct {
	chain          Chain
	netProvider    net.Provider
	walletRegistry *walletRegistry
	dkgExecutor    *dkg.Executor
	protocolLatch  *generator.ProtocolLatch
}

func newNode(
	chain Chain,
	netProvider net.Provider,
	persistence persistence.Handle,
	scheduler *generator.Scheduler,
	config Config,
) *node {
	walletRegistry := newWalletRegistry(persistence)

	dkgExecutor := dkg.NewExecutor(
		logger,
		scheduler,
		config.PreParamsPoolSize,
		config.PreParamsGenerationTimeout,
		config.PreParamsGenerationDelay,
		config.PreParamsGenerationConcurrency,
	)

	latch := generator.NewProtocolLatch()
	scheduler.RegisterProtocol(latch)

	return &node{
		chain:          chain,
		netProvider:    netProvider,
		walletRegistry: walletRegistry,
		dkgExecutor:    dkgExecutor,
		protocolLatch:  latch,
	}
}

// joinDKGIfEligible takes a seed value and undergoes the process of the
// distributed key generation if this node's operator proves to be eligible for
// the group generated by that seed. This is an interactive on-chain process,
// and joinDKGIfEligible can block for an extended period of time while it
// completes the on-chain operation.
func (n *node) joinDKGIfEligible(seed *big.Int, startBlockNumber uint64) {
	logger.Infof(
		"checking eligibility for DKG with seed [0x%x]",
		seed,
	)

	selectedSigningGroupOperators, err := n.chain.SelectGroup(seed)
	if err != nil {
		logger.Errorf(
			"failed to select group with seed [0x%x]: [%v]",
			seed,
			err,
		)
		return
	}

	chainConfig := n.chain.GetConfig()

	if len(selectedSigningGroupOperators) > chainConfig.GroupSize {
		logger.Errorf(
			"group size larger than supported: [%v]",
			len(selectedSigningGroupOperators),
		)
		return
	}

	signing := n.chain.Signing()

	_, operatorPublicKey, err := n.chain.OperatorKeyPair()
	if err != nil {
		logger.Errorf("failed to get operator public key: [%v]", err)
		return
	}

	operatorAddress, err := signing.PublicKeyToAddress(operatorPublicKey)
	if err != nil {
		logger.Errorf("failed to get operator address: [%v]", err)
		return
	}

	indexes := make([]uint8, 0)
	for index, operator := range selectedSigningGroupOperators {
		// See if we are amongst those chosen
		if operator == operatorAddress {
			indexes = append(indexes, uint8(index))
		}
	}

	// Create temporary broadcast channel name for DKG using the
	// group selection seed with the protocol name as prefix.
	channelName := fmt.Sprintf("%s-%s", ProtocolName, seed.Text(16))

	if len(indexes) > 0 {
		logger.Infof(
			"joining DKG with seed [0x%x] and controlling [%v] group members",
			seed,
			len(indexes),
		)

		broadcastChannel, err := n.netProvider.BroadcastChannelFor(channelName)
		if err != nil {
			logger.Errorf("failed to get broadcast channel: [%v]", err)
			return
		}

		membershipValidator := group.NewMembershipValidator(
			&testutils.MockLogger{},
			selectedSigningGroupOperators,
			signing,
		)

		err = broadcastChannel.SetFilter(membershipValidator.IsInGroup)
		if err != nil {
			logger.Errorf(
				"could not set filter for channel [%v]: [%v]",
				broadcastChannel.Name(),
				err,
			)
		}

		blockCounter, err := n.chain.BlockCounter()
		if err != nil {
			logger.Errorf("failed to get block counter: [%v]", err)
			return
		}

		for _, index := range indexes {
			// Capture the member index for the goroutine. The group member
			// index should be in range [1, groupSize] so we need to add 1.
			memberIndex := index + 1

			go func() {
				n.protocolLatch.Lock()
				defer n.protocolLatch.Unlock()

				result, executionEndBlock, err := n.dkgExecutor.Execute(
					seed,
					startBlockNumber,
					memberIndex,
					chainConfig.GroupSize,
					chainConfig.DishonestThreshold(),
					blockCounter,
					broadcastChannel,
					membershipValidator,
				)
				if err != nil {
					// TODO: Add retries into the mix.
					logger.Errorf(
						"[member:%v] failed to execute dkg: [%v]",
						memberIndex,
						err,
					)
					return
				}

				publicationStartBlock := executionEndBlock
				operatingMemberIndexes := result.Group.OperatingMemberIDs()
				dkgResultChannel := make(chan *DKGResultSubmittedEvent)

				dkgResultSubscription := n.chain.OnDKGResultSubmitted(
					func(event *DKGResultSubmittedEvent) {
						dkgResultChannel <- event
					},
				)
				defer dkgResultSubscription.Unsubscribe()

				err = dkg.Publish(
					logger,
					seed.Text(16),
					publicationStartBlock,
					memberIndex,
					blockCounter,
					broadcastChannel,
					membershipValidator,
					newDkgResultSigner(n.chain),
					newDkgResultSubmitter(n.chain),
					result,
				)
				if err != nil {
					// Result publication failed. It means that either the result
					// this member proposed is not supported by the majority of
					// group members or that the chain interaction failed.
					// In either case, we observe the chain for the result
					// published by any other group member and based on that,
					// we decide whether we should stay in the final group or
					// drop our membership.
					logger.Warningf(
						"[member:%v] DKG result publication process failed [%v]",
						memberIndex,
						err,
					)

					if operatingMemberIndexes, err = n.decideSigningGroupMemberFate(
						memberIndex,
						dkgResultChannel,
						publicationStartBlock,
						result,
					); err != nil {
						logger.Errorf(
							"failed to handle DKG result publishing failure: [%v]",
							err,
						)
						return
					}
				}

				signingGroupOperators, err := n.resolveFinalSigningGroupOperators(
					selectedSigningGroupOperators,
					operatingMemberIndexes,
				)
				if err != nil {
					logger.Errorf(
						"failed to resolve group operators: [%v]",
						err,
					)
					return
				}

				// TODO: Snapshot the key material before doing on-chain result
				//       submission.

				signer := newSigner(
					result.PrivateKeyShare.PublicKey(),
					signingGroupOperators,
					memberIndex,
					result.PrivateKeyShare,
				)

				err = n.walletRegistry.registerSigner(signer)
				if err != nil {
					logger.Errorf(
						"failed to register %s: [%v]",
						signer,
						err,
					)
					return
				}

				logger.Infof("registered %s", signer)
			}()
		}
	} else {
		logger.Infof("not eligible for DKG with seed [0x%x]", seed)
	}
}

// decideSigningGroupMemberFate decides what the member will do in case it
// failed to publish its DKG result. Member can stay in the group if it supports
// the same group public key as the one registered on-chain and the member is
// not considered as misbehaving by the group.
func (n *node) decideSigningGroupMemberFate(
	memberIndex group.MemberIndex,
	dkgResultChannel chan *DKGResultSubmittedEvent,
	publicationStartBlock uint64,
	result *dkg.Result,
) ([]group.MemberIndex, error) {
	dkgResultEvent, err := n.waitForDkgResultEvent(
		dkgResultChannel,
		publicationStartBlock,
	)
	if err != nil {
		return nil, err
	}

	groupPublicKeyBytes, err := result.GroupPublicKeyBytes()
	if err != nil {
		return nil, err
	}

	// If member doesn't support the same group public key, it could not stay
	// in the group.
	if !bytes.Equal(groupPublicKeyBytes, dkgResultEvent.GroupPublicKeyBytes) {
		return nil, fmt.Errorf(
			"[member:%v] could not stay in the group because "+
				"the member do not support the same group public key",
			memberIndex,
		)
	}

	misbehavedSet := make(map[group.MemberIndex]struct{})
	for _, misbehavedID := range dkgResultEvent.Misbehaved {
		misbehavedSet[misbehavedID] = struct{}{}
	}

	// If member is considered as misbehaved, it could not stay in the group.
	if _, isMisbehaved := misbehavedSet[memberIndex]; isMisbehaved {
		return nil, fmt.Errorf(
			"[member:%v] could not stay in the group because "+
				"the member is considered as misbehaving",
			memberIndex,
		)
	}

	// Construct a new view of the operating members according to the accepted
	// DKG result.
	operatingMemberIndexes := make([]group.MemberIndex, 0)
	for _, memberID := range result.Group.MemberIDs() {
		if _, isMisbehaved := misbehavedSet[memberID]; !isMisbehaved {
			operatingMemberIndexes = append(operatingMemberIndexes, memberID)
		}
	}

	return operatingMemberIndexes, nil
}

// waitForDkgResultEvent waits for the DKG result submission event. It times out
// and returns error if the DKG result event is not emitted on time.
func (n *node) waitForDkgResultEvent(
	dkgResultChannel chan *DKGResultSubmittedEvent,
	publicationStartBlock uint64,
) (*DKGResultSubmittedEvent, error) {
	config := n.chain.GetConfig()

	timeoutBlock := publicationStartBlock + dkg.PrePublicationBlocks() +
		(uint64(config.GroupSize) * config.ResultPublicationBlockStep)

	blockCounter, err := n.chain.BlockCounter()
	if err != nil {
		return nil, err
	}

	timeoutBlockChannel, err := blockCounter.BlockHeightWaiter(timeoutBlock)
	if err != nil {
		return nil, err
	}

	select {
	case dkgResultEvent := <-dkgResultChannel:
		return dkgResultEvent, nil
	case <-timeoutBlockChannel:
		return nil, fmt.Errorf("ECDSA DKG result publication timed out")
	}
}

// resolveFinalSigningGroupOperators takes two parameters:
// - selectedOperators: Contains addresses of all selected operators. Slice
//   length equals to the groupSize. Each element with index N corresponds
//   to the group member with ID N+1.
// - operatingMembersIndexes: Contains group members indexes that were neither
//   disqualified nor marked as inactive. Slice length is lesser than or equal
//   to the groupSize.
//
// Using those parameters, this function transforms the selectedOperators
// slice into another slice that contains addresses of all operators
// that were neither disqualified nor marked as inactive. This way, the
// resulting slice has only addresses of properly operating operators
// who form the resulting group.
//
// Example:
// selectedOperators: [member1, member2, member3, member4, member5]
// operatingMembersIndexes: [5, 1, 3]
// signingGroupOperators: [member1, member3, member5]
func (n *node) resolveFinalSigningGroupOperators(
	selectedOperators []chain.Address,
	operatingMembersIndexes []group.MemberIndex,
) ([]chain.Address, error) {
	config := n.chain.GetConfig()

	// TODO: Use `GroupQuorum` parameter instead of `HonestThreshold`
	if len(selectedOperators) != config.GroupSize ||
		len(operatingMembersIndexes) < config.HonestThreshold {
		return nil, fmt.Errorf("invalid input parameters")
	}

	sort.Slice(operatingMembersIndexes, func(i, j int) bool {
		return operatingMembersIndexes[i] < operatingMembersIndexes[j]
	})

	signingGroupOperators := make(
		[]chain.Address,
		len(operatingMembersIndexes),
	)

	for i, operatingMemberID := range operatingMembersIndexes {
		signingGroupOperators[i] = selectedOperators[operatingMemberID-1]
	}

	return signingGroupOperators, nil
}

// dkgResultSigner is responsible for signing the DKG result and verification of
// signatures generated by other group members.
type dkgResultSigner struct { // TODO: Add unit tests
	chain Chain
}

func newDkgResultSigner(chain Chain) *dkgResultSigner {
	return &dkgResultSigner{
		chain: chain,
	}
}

// SignResult signs the provided DKG result. It returns the information
// pertaining to the signing process: public key, signature, result hash.
func (drs *dkgResultSigner) SignResult(result *dkg.Result) (*dkg.SignedResult, error) {
	resultHash, err := drs.chain.CalculateDKGResultHash(result)
	if err != nil {
		return nil, fmt.Errorf(
			"dkg result hash calculation failed [%w]",
			err,
		)
	}

	signing := drs.chain.Signing()

	signature, err := signing.Sign(resultHash[:])
	if err != nil {
		return nil, fmt.Errorf(
			"dkg result hash signing failed [%w]",
			err,
		)
	}

	return &dkg.SignedResult{
		PublicKey:  signing.PublicKey(),
		Signature:  signature,
		ResultHash: resultHash,
	}, nil
}

// VerifySignature verifies if the signature was generated from the provided
// DKG result has using the provided public key.
func (drs *dkgResultSigner) VerifySignature(signedResult *dkg.SignedResult) (bool, error) {
	return drs.chain.Signing().VerifyWithPublicKey(
		signedResult.ResultHash[:],
		signedResult.Signature,
		signedResult.PublicKey,
	)
}

// dkgResultSubmitter is responsible for submitting the DKG result to the chain.
type dkgResultSubmitter struct { // TODO: Add unit tests
	chain Chain
}

func newDkgResultSubmitter(chain Chain) *dkgResultSubmitter {
	return &dkgResultSubmitter{
		chain: chain,
	}
}

// SubmitResult submits the DKG result along with submitting signatures to the
// chain. In the process, it checks if the number of signatures is above
// the required threshold, whether the result was already submitted and waits
// until the member is eligible for DKG result submission.
func (drs *dkgResultSubmitter) SubmitResult(
	memberIndex group.MemberIndex,
	result *dkg.Result,
	signatures map[group.MemberIndex][]byte,
	startBlockNumber uint64,
) error {
	config := drs.chain.GetConfig()

	// TODO: Compare signatures to the GroupQuorum parameter
	if len(signatures) < config.HonestThreshold {
		return fmt.Errorf(
			"could not submit result with [%v] signatures for signature "+
				"honest threshold [%v]",
			len(signatures),
			config.HonestThreshold,
		)
	}

	resultSubmittedChan := make(chan uint64)

	subscription := drs.chain.OnDKGResultSubmitted(
		func(event *DKGResultSubmittedEvent) {
			resultSubmittedChan <- event.BlockNumber
		},
	)
	defer subscription.Unsubscribe()

	dkgState, err := drs.chain.GetDKGState()
	if err != nil {
		return fmt.Errorf("could not check DKG state: [%w]", err)
	}

	if dkgState != AwaitingResult {
		// Someone who was ahead of us in the queue submitted the result. Giving up.
		logger.Infof(
			"[member:%v] DKG is no longer awaiting the result; "+
				"aborting DKG result submission",
			memberIndex,
		)
		return nil
	}

	// Wait until the current member is eligible to submit the result.
	submitterEligibleChan, err := drs.setupEligibilityQueue(
		startBlockNumber,
		memberIndex,
	)
	if err != nil {
		return fmt.Errorf("cannot set up eligibility queue: [%w]", err)
	}

	for {
		select {
		case blockNumber := <-submitterEligibleChan:
			// Member becomes eligible to submit the result. Result submission
 			// would trigger the sender side of the result submission event
 			// listener but also cause the receiver side (this select)
 			// termination that will result with a dangling goroutine blocked
 			// forever on the `onSubmittedResultChan` channel. This would
 			// cause a resource leak. In order to avoid that, we should
 			// unsubscribe from the result submission event listener before
 			// submitting the result.
			subscription.Unsubscribe()

			publicKeyBytes, err := result.GroupPublicKeyBytes()
			if err != nil {
				return fmt.Errorf("cannot get public key bytes [%w]", err)
			}

			logger.Infof(
				"[member:%v] submitting DKG result with public key [0x%x] and "+
					"[%v] supporting member signatures at block [%v]",
				memberIndex,
				publicKeyBytes,
				len(signatures),
				blockNumber,
			)

			return drs.chain.SubmitDKGResult(
				memberIndex,
				result,
				signatures,
			)
		case blockNumber := <-resultSubmittedChan:
			logger.Infof(
				"[member:%v] leaving; DKG result submitted by other member "+
					"at block [%v]",
				memberIndex,
				blockNumber,
			)
			// A result has been submitted by other member. Leave without
			// publishing the result.
			return nil
		}
	}
}

// setupEligibilityQueue waits until the current member is eligible to
// submit a result to the blockchain. First member is eligible to submit straight
// away, each following member is eligible after pre-defined block step.
// TODO: Revisit the setupEligibilityQueue function. The RFC mentions we should
//       start submitting from a random member, not the first one.
func (drs *dkgResultSubmitter) setupEligibilityQueue(
	startBlockNumber uint64,
	memberIndex group.MemberIndex,
) (<-chan uint64, error) {
	blockWaitTime := (uint64(memberIndex) - 1) *
		drs.chain.GetConfig().ResultPublicationBlockStep

	eligibleBlockHeight := startBlockNumber + blockWaitTime

	logger.Infof(
		"[member:%v] waiting for block [%v] to submit",
		memberIndex,
		eligibleBlockHeight,
	)

	blockCounter, err := drs.chain.BlockCounter()
	if err != nil {
		return nil, fmt.Errorf("could not get block counter [%w]", err)
	}

	waiter, err := blockCounter.BlockHeightWaiter(eligibleBlockHeight)
	if err != nil {
		return nil, fmt.Errorf("block height waiter failure [%w]", err)
	}

	return waiter, err
}
