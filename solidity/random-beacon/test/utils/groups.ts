import { helpers } from "hardhat"
import { signAndSubmitDkgResult } from "./dkg"
import { constants, params } from "../fixtures"
import blsData from "../data/bls"
import { Operator } from "./sortitionpool"
import type { RandomBeaconStub, RandomBeacon } from "../../typechain"

const { mineBlocks } = helpers.time

// eslint-disable-next-line import/prefer-default-export
export async function createGroup(
  randomBeacon: RandomBeacon,
  signers: Operator[]
): Promise<void> {
  const { blockNumber: startBlock } = await randomBeacon.genesis()
  await mineBlocks(constants.offchainDkgTime)
  await signAndSubmitDkgResult(
    randomBeacon,
    blsData.groupPubKey,
    signers,
    startBlock
  )
  await mineBlocks(params.dkgResultChallengePeriodLength)
  await randomBeacon.approveDkgResult()
}
