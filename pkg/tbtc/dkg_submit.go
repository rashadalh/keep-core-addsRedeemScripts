package tbtc

import (
	"context"
	"fmt"

	"github.com/ipfs/go-log/v2"
	"github.com/keep-network/keep-core/pkg/protocol/group"
	"github.com/keep-network/keep-core/pkg/tecdsa/dkg"
)

// dkgResultSigner is responsible for signing the DKG result and verification of
// signatures generated by other group members.
type dkgResultSigner struct {
	chain         Chain
	dkgStartBlock uint64
}

func newDkgResultSigner(chain Chain, dkgStartBlock uint64) *dkgResultSigner {
	return &dkgResultSigner{
		chain:         chain,
		dkgStartBlock: dkgStartBlock,
	}
}

// SignResult signs the provided DKG result. It returns the information
// pertaining to the signing process: public key, signature, result hash.
func (drs *dkgResultSigner) SignResult(result *dkg.Result) (*dkg.SignedResult, error) {
	if result == nil {
		return nil, fmt.Errorf("result is nil")
	}

	groupPublicKey, err := result.GroupPublicKey()
	if err != nil {
		return nil, fmt.Errorf("cannot get group public key: [%v]", err)
	}

	resultHash, err := drs.chain.CalculateDKGResultSignatureHash(
		groupPublicKey,
		result.MisbehavedMembersIndexes(),
		drs.dkgStartBlock,
	)
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
type dkgResultSubmitter struct {
	dkgLogger log.StandardLogger

	chain                Chain
	groupParameters      *GroupParameters
	groupSelectionResult *GroupSelectionResult

	waitForBlockFn waitForBlockFn
}

func newDkgResultSubmitter(
	dkgLogger log.StandardLogger,
	chain Chain,
	groupParameters *GroupParameters,
	groupSelectionResult *GroupSelectionResult,
	waitForBlockFn waitForBlockFn,
) *dkgResultSubmitter {
	return &dkgResultSubmitter{
		dkgLogger:            dkgLogger,
		chain:                chain,
		groupSelectionResult: groupSelectionResult,
		groupParameters:      groupParameters,
		waitForBlockFn:       waitForBlockFn,
	}
}

// SubmitResult submits the DKG result along with submitting signatures to the
// chain. In the process, it checks if the number of signatures is above
// the required threshold, whether the result was already submitted and waits
// until the member is eligible for DKG result submission or the given context
// is done, whichever comes first.
func (drs *dkgResultSubmitter) SubmitResult(
	ctx context.Context,
	memberIndex group.MemberIndex,
	result *dkg.Result,
	signatures map[group.MemberIndex][]byte,
) error {
	if len(signatures) < drs.groupParameters.GroupQuorum {
		return fmt.Errorf(
			"could not submit result with [%v] signatures for group quorum [%v]",
			len(signatures),
			drs.groupParameters.GroupQuorum,
		)
	}

	dkgState, err := drs.chain.GetDKGState()
	if err != nil {
		return fmt.Errorf("could not check DKG state: [%w]", err)
	}

	if dkgState != AwaitingResult {
		// Someone who was ahead of us in the queue submitted the result. Giving up.
		drs.dkgLogger.Infof(
			"[member:%v] DKG is no longer awaiting the result; "+
				"aborting DKG result on-chain submission",
			memberIndex,
		)
		return nil
	}

	groupPublicKey, err := result.GroupPublicKey()
	if err != nil {
		return fmt.Errorf("cannot get group public key [%w]", err)
	}

	dkgResult, err := drs.chain.AssembleDKGResult(
		memberIndex,
		groupPublicKey,
		result.Group.OperatingMemberIndexes(),
		result.MisbehavedMembersIndexes(),
		signatures,
		drs.groupSelectionResult,
	)
	if err != nil {
		return fmt.Errorf("cannot assemble DKG chain result [%w]", err)
	}

	isValid, err := drs.chain.IsDKGResultValid(dkgResult)
	if err != nil {
		return fmt.Errorf("cannot validate DKG result: [%w]", err)
	}

	if !isValid {
		return fmt.Errorf("invalid DKG result")
	}

	blockCounter, err := drs.chain.BlockCounter()
	if err != nil {
		return err
	}

	// We can't determine a common block at which the publication starts.
	// However, all we want here is to ensure the members does not submit
	// in the same time. This can be achieved by simply using the index-based
	// delay starting from the current block.
	currentBlock, err := blockCounter.CurrentBlock()
	if err != nil {
		return fmt.Errorf("cannot get current block: [%v]", err)
	}
	delayBlocks := uint64(memberIndex-1) * dkgResultSubmissionDelayStepBlocks
	submissionBlock := currentBlock + delayBlocks

	drs.dkgLogger.Infof(
		"[member:%v] waiting for block [%v] to submit DKG result",
		memberIndex,
		submissionBlock,
	)

	err = drs.waitForBlockFn(ctx, submissionBlock)
	if err != nil {
		return fmt.Errorf(
			"error while waiting for DKG result submission block: [%v]",
			err,
		)
	}

	if ctx.Err() != nil {
		// The context was cancelled by the upstream. Regardless of the cause,
		// that means the DKG is no longer awaiting the result, and we can
		// safely return.
		drs.dkgLogger.Infof(
			"[member:%v] DKG is no longer awaiting the result; "+
				"aborting DKG result on-chain submission",
			memberIndex,
		)
		return nil
	}

	drs.dkgLogger.Infof(
		"[member:%v] submitting DKG result with [%v] supporting "+
			"member signatures",
		memberIndex,
		len(signatures),
	)

	return drs.chain.SubmitDKGResult(dkgResult)
}
