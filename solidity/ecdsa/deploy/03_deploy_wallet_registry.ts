import type { HardhatRuntimeEnvironment } from "hardhat/types"
import type { DeployFunction } from "hardhat-deploy/types"

const func: DeployFunction = async (hre: HardhatRuntimeEnvironment) => {
  const { getNamedAccounts, deployments, helpers } = hre
  const { deployer, walletOwner } = await getNamedAccounts()

  const SortitionPool = await deployments.get("SortitionPool")
  let TokenStaking = await deployments.get("TokenStaking")
  const DKGValidator = await deployments.get("DKGValidator")

  const DKG = await deployments.deploy("DKG", {
    from: deployer,
    log: true,
  })

  const Wallets = await deployments.deploy("Wallets", {
    from: deployer,
    log: true,
  })

  // TODO: StakingStub contract should be replaced by actual implementation of
  // TokenStaking contract, as soon as integration is implemented.
  if (deployments.getNetworkName() === "hardhat") {
    console.log("deploying StakingStub contract instead of TokenStaking")
    TokenStaking = await deployments.deploy("StakingStub", {
      from: deployer,
      log: true,
    })
  }

  const WalletRegistry = await deployments.deploy("WalletRegistry", {
    contract:
      deployments.getNetworkName() === "hardhat"
        ? "WalletRegistryStub"
        : undefined,
    from: deployer,
    args: [
      SortitionPool.address,
      TokenStaking.address,
      DKGValidator.address,
      walletOwner,
    ],
    libraries: { DKG: DKG.address, Wallets: Wallets.address },
    log: true,
  })

  await helpers.ownable.transferOwnership(
    "SortitionPool",
    WalletRegistry.address,
    deployer
  )

  if (hre.network.tags.tenderly) {
    await hre.tenderly.verify({
      name: "WalletRegistry",
      address: WalletRegistry.address,
    })
  }
}

export default func

func.tags = ["WalletRegistry"]
func.dependencies = ["SortitionPool", "TokenStaking", "DKGValidator"]
