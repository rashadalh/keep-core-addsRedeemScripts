package spv

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/keep-network/keep-core/pkg/bitcoin"
	"github.com/keep-network/keep-core/pkg/chain"
	"github.com/keep-network/keep-core/pkg/tbtc"
)

type Chain interface {
	// SubmitDepositSweepProofWithReimbursement submits the deposit sweep proof
	// via MaintainerProxy. It is used to prove the deposit sweep Bitcoin
	// transaction and update depositors' balances. The caller is reimbursed.
	SubmitDepositSweepProofWithReimbursement(
		transaction *bitcoin.Transaction,
		proof *bitcoin.SpvProof,
		mainUTXO bitcoin.UnspentTransactionOutput,
		vault common.Address,
	) error

	// GetDepositRequest gets the on-chain deposit request for the given
	// funding transaction hash and output index.The returned values represent:
	// - deposit request which is non-nil only when the deposit request was
	//   found,
	// - boolean value which is true if the deposit request was found, false
	//   otherwise,
	// - error which is non-nil only when the function execution failed. It will
	//   be nil if the deposit request was not found, but the function execution
	//   succeeded.
	GetDepositRequest(
		fundingTxHash bitcoin.Hash,
		fundingOutputIndex uint32,
	) (*tbtc.DepositChainRequest, bool, error)

	GetWallet(
		walletPublicKeyHash [20]byte,
	) (*tbtc.WalletChainData, error)

	// ComputeMainUtxoHash computes the hash of the provided main UTXO
	// according to the on-chain Bridge rules.
	ComputeMainUtxoHash(mainUtxo *bitcoin.UnspentTransactionOutput) [32]byte

	// TxProofDifficultyFactor returns the number of confirmations on the
	// Bitcoin chain required to successfully evaluate an SPV proof.
	TxProofDifficultyFactor() (*big.Int, error)

	// BlockCounter returns the chain's block counter.
	BlockCounter() (chain.BlockCounter, error)

	// GetPendingRedemptionRequest gets the on-chain pending redemption request
	// for the given wallet public key hash and redeemer output script.
	// The returned bool value indicates whether the request was found or not.
	GetPendingRedemptionRequest(
		walletPublicKeyHash [20]byte,
		redeemerOutputScript bitcoin.Script,
	) (*tbtc.RedemptionRequest, bool, error)

	// SubmitRedemptionProofWithReimbursement submits the redemption proof
	// via MaintainerProxy. The caller is reimbursed.
	SubmitRedemptionProofWithReimbursement(
		transaction *bitcoin.Transaction,
		proof *bitcoin.SpvProof,
		mainUTXO bitcoin.UnspentTransactionOutput,
		walletPublicKeyHash [20]byte,
	) error

	// SubmitMovingFundsProofWithReimbursement submits the moving funds proof
	// via MaintainerProxy. The caller is reimbursed.
	SubmitMovingFundsProofWithReimbursement(
		transaction *bitcoin.Transaction,
		proof *bitcoin.SpvProof,
		mainUTXO bitcoin.UnspentTransactionOutput,
		walletPublicKeyHash [20]byte,
	) error

	// PastDepositRevealedEvents fetches past deposit reveal events according
	// to the provided filter or unfiltered if the filter is nil. Returned
	// events are sorted by the block number in the ascending order, i.e. the
	// latest event is at the end of the slice.
	PastDepositRevealedEvents(
		filter *tbtc.DepositRevealedEventFilter,
	) ([]*tbtc.DepositRevealedEvent, error)

	// PastRedemptionRequestedEvents fetches past redemption requested events according
	// to the provided filter or unfiltered if the filter is nil. Returned
	// events are sorted by the block number in the ascending order, i.e. the
	// latest event is at the end of the slice.
	PastRedemptionRequestedEvents(
		filter *tbtc.RedemptionRequestedEventFilter,
	) ([]*tbtc.RedemptionRequestedEvent, error)

	// PastMovingFundsCommitmentSubmittedEvents fetches past moving funds
	// commitment submitted events according to the provided filter or
	// unfiltered if the filter is nil. Returned events are sorted by the block
	// number in the ascending order, i.e. the latest event is at the end of the
	// slice.
	PastMovingFundsCommitmentSubmittedEvents(
		filter *tbtc.MovingFundsCommitmentSubmittedEventFilter,
	) ([]*tbtc.MovingFundsCommitmentSubmittedEvent, error)
}
