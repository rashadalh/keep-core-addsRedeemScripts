package sortition

import (
	"context"
	"fmt"
	"time"

	"github.com/ipfs/go-log"
)

const (
	DefaultStatusCheckTick = 10 * time.Second
)

var logger = log.Logger("keep-sortition")

var errOperatorUnknown = fmt.Errorf("operator not registered for the staking provider, check Threshold dashboard")

// MonitorPool periodically checks the status of the operator in the sortition
// pool. If the operator is supposed to be in the sortition pool but is not
// there yet, the function attempts to add the operator to the pool. If the
// operator is already in the pool and its status is no longer up to date, the
// function attempts to update the operator's status in the pool.
func MonitorPool(
	ctx context.Context,
	chain Chain,
	tick time.Duration,
) error {
	_, isRegistered, err := chain.OperatorToStakingProvider()
	if err != nil {
		return fmt.Errorf("could not resolve staking provider: [%w]", err)
	}

	if !isRegistered {
		return errOperatorUnknown
	}

	err = checkOperatorStatus(chain)
	if err != nil {
		logger.Errorf("could not check operator sortition pool status: [%v]", err)
	}

	ticker := time.NewTicker(tick)

	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				err = checkOperatorStatus(chain)
				if err != nil {
					logger.Errorf("could not check operator sortition pool status: [%v]", err)
					continue
				}
			}
		}
	}()

	return nil
}

func checkOperatorStatus(chain Chain) error {
	logger.Info("checking sortition pool operator status")

	isOperatorInPool, err := chain.IsOperatorInPool()
	if err != nil {
		return err
	}

	isOperatorUpToDate, err := chain.IsOperatorUpToDate()
	if err != nil {
		return err
	}

	if isOperatorInPool {
		logger.Info("operator is in the sortition pool")

		err = checkRewardsEligibility(chain)
		if err != nil {
			logger.Errorf("could not check for rewards eligibility: [%v]", err)
		}
	} else {
		logger.Info("operator is not in the sortition pool")
	}

	if isOperatorUpToDate {
		if isOperatorInPool {
			logger.Info("sortition pool operator weight is up to date")
		} else {
			logger.Info("please inspect staking providers's authorization for the Random Beacon")
		}

		return nil
	}

	isLocked, err := chain.IsPoolLocked()
	if err != nil {
		return err
	}

	if isLocked {
		logger.Info("sortition pool state is locked, waiting with the update")
		return nil
	}

	if isOperatorInPool {
		logger.Info("updating operator status in the sortition pool")
		err := chain.UpdateOperatorStatus()
		if err != nil {
			logger.Errorf("could not update the sortition pool: [%v]", err)
		}
	} else {
		logger.Info("joining the sortition pool")
		err := chain.JoinSortitionPool()
		if err != nil {
			logger.Errorf("could not join the sortition pool: [%v]", err)
		}
	}

	return nil
}

func checkRewardsEligibility(chain Chain) error {
	isEligibleForRewards, err := chain.IsEligibleForRewards()
	if err != nil {
		return err
	}

	if isEligibleForRewards {
		logger.Info("operator is eligible for rewards")
	} else {
		logger.Info("operator is marked as ineligible for rewards")

		canRestoreRewardEligibility, err := chain.CanRestoreRewardEligibility()
		if err != nil {
			return err
		}

		if canRestoreRewardEligibility {
			logger.Info("restoring eligibility for rewards")

			err = chain.RestoreRewardEligibility()
			if err != nil {
				return err
			}
		} else {
			logger.Info("cannot restore eligibility for rewards yet")
		}
	}

	return nil
}
