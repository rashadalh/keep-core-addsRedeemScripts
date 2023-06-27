package spv

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/ipfs/go-log/v2"

	"github.com/keep-network/keep-core/pkg/bitcoin"
	"github.com/keep-network/keep-core/pkg/tbtc"
)

var logger = log.Logger("keep-maintainer-spv")

func Initialize(
	ctx context.Context,
	config Config,
	chain Chain,
	btcChain bitcoin.Chain,
) {
	spvMaintainer := &spvMaintainer{
		config:   config,
		chain:    chain,
		btcChain: btcChain,
	}

	go spvMaintainer.startControlLoop(ctx)
}

type spvMaintainer struct {
	config   Config
	chain    Chain
	btcChain bitcoin.Chain
}

func (sm *spvMaintainer) startControlLoop(ctx context.Context) {
	logger.Info("starting SPV maintainer")

	defer func() {
		logger.Info("stopping SPV maintainer")
	}()

	for {
		err := sm.maintainSpv(ctx)
		if err != nil {
			logger.Errorf(
				"error while maintaining SPV: [%v]; restarting maintainer",
				err,
			)
		}

		select {
		case <-time.After(sm.config.RestartBackOffTime):
		case <-ctx.Done():
			return
		}
	}
}

func (sm *spvMaintainer) maintainSpv(ctx context.Context) error {
	for {
		if err := sm.proveDepositSweepTransactions(); err != nil {
			return fmt.Errorf(
				"error while proving deposit sweep transactions: [%v]",
				err,
			)
		}

		// TODO: Add proving of other type of SPV transactions: redemption
		// transactions, moving funds transaction, etc.

		select {
		case <-time.After(sm.config.IdleBackOffTime):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (sm *spvMaintainer) proveDepositSweepTransactions() error {
	depositSweepTransactions, err := sm.getUnprovenDepositSweepTransactions()
	if err != nil {
		return fmt.Errorf(
			"failed to get unproven deposit sweep transactions: [%v]",
			err,
		)
	}

	// TODO: Consider handling a situation in which the block headers in the
	//       proof span multiple Bitcoin difficulty epochs.
	requiredConfirmations, err := sm.chain.TxProofDifficultyFactor()
	if err != nil {
		return fmt.Errorf(
			"failed to get transaction proof difficulty factor: [%v]",
			err,
		)
	}

	for _, transaction := range depositSweepTransactions {
		_, proof, err := bitcoin.AssembleSpvProof(
			transaction.Hash(),
			uint(requiredConfirmations.Uint64()),
			sm.btcChain,
		)
		if err != nil {
			return fmt.Errorf("failed to assemble SPV proof: [%v]", err)
		}

		mainUTXO, vault, err := parseTransactionInputs(
			sm.btcChain,
			sm.chain,
			transaction,
		)
		if err != nil {
			return fmt.Errorf(
				"error while parsing transaction inputs: [%v]",
				err,
			)
		}

		// TODO: Remember that the relay may temporarily be in the out-of-date
		//       state. It happens at the beginning of each Bitcoin difficulty
		//       epoch. Detect the situation when the proof contains block
		//       headers with a difficulty that is not yet proven. Skip proving
		//       such a transaction. It will be proven in the future by another
		//       round of processing deposit sweep proofs.
		if err := sm.chain.SubmitDepositSweepProofWithReimbursement(
			transaction,
			proof,
			mainUTXO,
			vault,
		); err != nil {
			return fmt.Errorf(
				"failed to submit deposit sweep proof with reimbursement: [%v]",
				err,
			)
		}
	}

	return nil
}

func (sm *spvMaintainer) getUnprovenDepositSweepTransactions() (
	[]*bitcoin.Transaction,
	error,
) {
	blockCounter, err := sm.chain.BlockCounter()
	if err != nil {
		return nil, fmt.Errorf("failed to get block counter: [%v]", err)
	}

	currentBlock, err := blockCounter.CurrentBlock()
	if err != nil {
		return nil, fmt.Errorf("failed to get current block: [%v]", err)
	}

	// Calculate the starting block of the range in which the events will be
	// searched for.
	startBlock := currentBlock - 40320

	// TODO: Limit how far in the past we are looking for the events.
	//       Possibly store latest checked height in memory or file.
	depositSweepTransactionProposals, err :=
		sm.chain.PastDepositSweepProposalSubmittedEvents(
			&tbtc.DepositSweepProposalSubmittedEventFilter{
				StartBlock: startBlock,
			},
		)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to get past deposit sweep proposal submitted events: [%v]",
			err,
		)
	}

	// There will often be multiple events emitted for a single wallet. Prepare
	// a list of unique wallet public key hashes.
	walletPublicKeyHashes := uniqueWalletPublicKeyHashes(
		depositSweepTransactionProposals,
	)

	unprovenDepositSweepTransactions := []*bitcoin.Transaction{}

	for _, walletPublicKeyHash := range walletPublicKeyHashes {
		// TODO: Should we check the wallet's state before attempting to submit
		//       the deposit sweep proof?
		// TODO: Think what the limit of transactions retrieved should be.
		walletTransactions, err := sm.btcChain.GetTransactionsForPublicKeyHash(
			walletPublicKeyHash,
			5,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to get transactions for wallet: [%v]",
				err,
			)
		}

		for _, transaction := range walletTransactions {
			isUnprovenDepositSweepTransaction, err :=
				sm.isUnprovenDepositSweepTransaction(
					transaction,
					walletPublicKeyHash,
				)
			if err != nil {
				return nil, fmt.Errorf(
					"failed to check if transaction is an unproven deposit sweep "+
						"transaction: [%v]",
					err,
				)
			}

			if isUnprovenDepositSweepTransaction {
				unprovenDepositSweepTransactions = append(
					unprovenDepositSweepTransactions,
					transaction,
				)
			}
		}
	}

	return unprovenDepositSweepTransactions, nil
}

func (sm *spvMaintainer) isUnprovenDepositSweepTransaction(
	transaction *bitcoin.Transaction,
	walletPublicKeyHash [20]byte,
) (bool, error) {
	// If the transaction does not have exactly one output, it cannot be a
	// deposit sweep transaction.
	if len(transaction.Outputs) != 1 {
		return false, nil
	}

	hasDepositInputs := false

	// Look at the transaction's inputs. All the inputs must be deposit inputs,
	// except for one input which can be the main UTXO.
	for _, input := range transaction.Inputs {
		fundingTransactionHash := input.Outpoint.TransactionHash
		fundingOutpointIndex := input.Outpoint.OutputIndex

		// Check if the input is a deposit input.
		deposit, err := sm.chain.Deposits(
			fundingTransactionHash,
			fundingOutpointIndex,
		)
		if err != nil {
			return false, fmt.Errorf("failed to get a deposit: [%v]", err)
		}

		if deposit.RevealedAt.Equal(time.Unix(0, 0)) {
			// The input is not a deposit input. The transaction can still be
			// a deposit sweep transaction, since the input may be the main UTXO.

			// Check if the input represents the current main UTXO of the wallet.
			// Notice that we don't have to verify if there is only one main
			// UTXO among the transaction's inputs since only one input may have
			// such a structure that the calculated hash will match the wallet's
			// main UTXO hash stored on-chain.
			isMainUtxo, err := sm.isInputCurrentWalletsMainUTXO(
				fundingTransactionHash,
				fundingOutpointIndex,
				walletPublicKeyHash,
			)
			if err != nil {
				return false, fmt.Errorf(
					"failed to check if input is the main UTXO",
				)
			}

			// The input is not the current main UTXO of the wallet. The
			// transaction is either a deposit sweep transaction that is already
			// proven or it's not a deposit sweep transaction at all.
			if !isMainUtxo {
				return false, nil
			}

			// The input is the current main UTXO of the wallet. Proceed with
			// checking other inputs.
		} else {
			// The input is a deposit input. Check if it swept or not.
			if deposit.SweptAt.Equal(time.Unix(0, 0)) {
				// The input is a deposit and it's unswept.
				hasDepositInputs = true
			} else {
				// The input is a deposit, but it's already swept.
				// The transaction must a deposit sweep transaction, but it's
				// already proven.
				return false, nil
			}
		}
	}

	// All the inputs represent either unswept deposits or the current main UTXO.
	// As the final check verify if at least one of them was a deposit input.
	// This will distinguish a deposit sweep transaction from a different
	// transaction type that may have the main UTXO as input, e.g. redemption.
	return hasDepositInputs, nil
}

func (sm *spvMaintainer) isInputCurrentWalletsMainUTXO(
	fundingTxHash bitcoin.Hash,
	fundingOutputIndex uint32,
	walletPublicKeyHash [20]byte,
) (bool, error) {
	// Get the transaction the input originated from to calculate the input value.
	previousTransaction, err := sm.btcChain.GetTransaction(fundingTxHash)
	if err != nil {
		return false, fmt.Errorf("failed to get previous transaction: [%v]", err)
	}
	fundingOutputValue := previousTransaction.Outputs[fundingOutputIndex].Value

	// Assume the input is the main UTXO and calculate hash.
	mainUtxoHash := sm.chain.ComputeMainUtxoHash(&bitcoin.UnspentTransactionOutput{
		Outpoint: &bitcoin.TransactionOutpoint{
			TransactionHash: fundingTxHash,
			OutputIndex:     fundingOutputIndex,
		},
		Value: fundingOutputValue,
	})

	// Get the wallet and check if its main UTXO matches the calculated hash.
	wallet, err := sm.chain.GetWallet(walletPublicKeyHash)
	if err != nil {
		return false, fmt.Errorf("failed to get wallet: [%v]", err)
	}

	return bytes.Equal(mainUtxoHash[:], wallet.MainUtxoHash[:]), nil
}

// uniqueWalletPublicKeyHashes parses the list of events and returns a list of
// unique wallet public key hashes.
func uniqueWalletPublicKeyHashes(
	events []*tbtc.DepositSweepProposalSubmittedEvent,
) [][20]byte {
	cache := make(map[string]struct{})
	var publicKeyHashes [][20]byte

	for _, event := range events {
		key := event.Proposal.WalletPublicKeyHash
		strKey := hex.EncodeToString(key[:])

		// Check for uniqueness
		if _, exists := cache[strKey]; !exists {
			cache[strKey] = struct{}{}
			publicKeyHashes = append(publicKeyHashes, key)
		}
	}

	return publicKeyHashes
}
