package miner

import (
	"fmt"

	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"

	"github.com/filecoin-project/specs-actors/v5/actors/builtin"
)

// The period over which a miner's active sectors are expected to be proven via WindowPoSt.
// This guarantees that (1) user data is proven daily, (2) user data is stored for 24h by a rational miner
// (due to Window PoSt cost assumption).
var WPoStProvingPeriod = abi.ChainEpoch(builtin.EpochsInDay) // 24 hours PARAM_SPEC

// The period between the opening and the closing of a WindowPoSt deadline in which the miner is expected to
// provide a Window PoSt proof.
// This provides a miner enough time to compute and propagate a Window PoSt proof.
var WPoStChallengeWindow = abi.ChainEpoch(30 * 60 / builtin.EpochDurationSeconds) // 30 minutes (48 per day) PARAM_SPEC

// WPoStDisputeWindow is the period after a challenge window ends during which
// PoSts submitted during that period may be disputed.
var WPoStDisputeWindow = 2 * ChainFinality // PARAM_SPEC

// The number of non-overlapping PoSt deadlines in a proving period.
// This spreads a miner's Window PoSt work across a proving period.
const WPoStPeriodDeadlines = uint64(48) // PARAM_SPEC

// MaxPartitionsPerDeadline is the maximum number of partitions that will be assigned to a deadline.
// For a minimum storage of upto 1Eib, we need 300 partitions per deadline.
// 48 * 32GiB * 2349 * 300 = 1.00808144 EiB
// So, to support upto 10Eib storage, we set this to 3000.
const MaxPartitionsPerDeadline = 3000

func init() {
	// Check that the challenge windows divide the proving period evenly.
	if WPoStProvingPeriod%WPoStChallengeWindow != 0 {
		panic(fmt.Sprintf("incompatible proving period %d and challenge window %d", WPoStProvingPeriod, WPoStChallengeWindow))
	}
	// Check that WPoStPeriodDeadlines is consistent with the proving period and challenge window.
	if abi.ChainEpoch(WPoStPeriodDeadlines)*WPoStChallengeWindow != WPoStProvingPeriod {
		panic(fmt.Sprintf("incompatible proving period %d and challenge window %d", WPoStProvingPeriod, WPoStChallengeWindow))
	}

	// Check to make sure the dispute window is longer than finality so there's always some time to dispute bad proofs.
	if WPoStDisputeWindow <= ChainFinality {
		panic(fmt.Sprintf("the proof dispute period %d must exceed finality %d", WPoStDisputeWindow, ChainFinality))
	}

	// A deadline becomes immutable one challenge window before it's challenge window opens.
	// The challenge lookback must fall within this immutability period.
	if WPoStChallengeLookback > WPoStChallengeWindow {
		panic("the challenge lookback cannot exceed one challenge window")
	}

	// Deadlines are immutable when the challenge window is open, and during
	// the previous challenge window.
	immutableWindow := 2 * WPoStChallengeWindow

	// We want to reserve at least one deadline's worth of time to compact a
	// deadline.
	minCompactionWindow := WPoStChallengeWindow

	// Make sure we have enough time in the proving period to do everything we need.
	if (minCompactionWindow + immutableWindow + WPoStDisputeWindow) > WPoStProvingPeriod {
		panic(fmt.Sprintf("together, the minimum compaction window (%d) immutability window (%d) and the dispute window (%d) exceed the proving period (%d)",
			minCompactionWindow, immutableWindow, WPoStDisputeWindow, WPoStProvingPeriod))
	}
}

// The maximum number of partitions that can be loaded in a single invocation.
// This limits the number of simultaneous fault, recovery, or sector-extension declarations.
// We set this to same as MaxPartitionsPerDeadline so we can process that many partitions every deadline.
const AddressedPartitionsMax = MaxPartitionsPerDeadline

// Maximum number of unique "declarations" in batch operations.
const DeclarationsMax = AddressedPartitionsMax

// The maximum number of sector infos that can be loaded in a single invocation.
// This limits the amount of state to be read in a single message execution.
const AddressedSectorsMax = 25_000 // PARAM_SPEC

// Libp2p peer info limits.
const (
	// MaxPeerIDLength is the maximum length allowed for any on-chain peer ID.
	// Most Peer IDs are expected to be less than 50 bytes.
	MaxPeerIDLength = 128 // PARAM_SPEC

	// MaxMultiaddrData is the maximum amount of data that can be stored in multiaddrs.
	MaxMultiaddrData = 1024 // PARAM_SPEC
)

// Maximum number of control addresses a miner may register.
const MaxControlAddresses = 10

// The maximum number of partitions that may be required to be loaded in a single invocation,
// when all the sector infos for the partitions will be loaded.
func loadPartitionsSectorsMax(partitionSectorCount uint64) uint64 {
	return min64(AddressedSectorsMax/partitionSectorCount, AddressedPartitionsMax)
}

// Epochs after which chain state is final with overwhelming probability (hence the likelihood of two fork of this size is negligible)
// This is a conservative value that is chosen via simulations of all known attacks.
const ChainFinality = abi.ChainEpoch(900) // PARAM_SPEC

// Prefix for sealed sector CIDs (CommR).
var SealedCIDPrefix = cid.Prefix{
	Version:  1,
	Codec:    cid.FilCommitmentSealed,
	MhType:   mh.POSEIDON_BLS12_381_A1_FC1,
	MhLength: 32,
}

// List of proof types which may be used when creating a new miner actor.
// This is mutable to allow configuration of testing and development networks.
var WindowPoStProofTypes = map[abi.RegisteredPoStProof]struct{}{
	abi.RegisteredPoStProof_StackedDrgWindow32GiBV1: {},
	abi.RegisteredPoStProof_StackedDrgWindow64GiBV1: {},
}

// Checks whether a PoSt proof type is supported for new miners.
func CanWindowPoStProof(s abi.RegisteredPoStProof) bool {
	_, ok := WindowPoStProofTypes[s]
	return ok
}

// List of proof types which may be used when pre-committing a new sector.
// This is mutable to allow configuration of testing and development networks.
// From network version 8, sectors sealed with the V1 seal proof types cannot be committed.
var PreCommitSealProofTypesV8 = map[abi.RegisteredSealProof]struct{}{
	abi.RegisteredSealProof_StackedDrg32GiBV1_1: {},
	abi.RegisteredSealProof_StackedDrg64GiBV1_1: {},
}

// Checks whether a seal proof type is supported for new miners and sectors.
func CanPreCommitSealProof(s abi.RegisteredSealProof) bool {
	_, ok := PreCommitSealProofTypesV8[s]
	return ok
}

// Checks whether a seal proof type is supported for new miners and sectors.
// As of network version 11, all permitted seal proof types may be extended.
func CanExtendSealProofType(_ abi.RegisteredSealProof) bool {
	return true
}

// Maximum delay to allow between sector pre-commit and subsequent proof.
// The allowable delay depends on seal proof algorithm.
var MaxProveCommitDuration = map[abi.RegisteredSealProof]abi.ChainEpoch{
	abi.RegisteredSealProof_StackedDrg32GiBV1:  builtin.EpochsInDay + PreCommitChallengeDelay, // PARAM_SPEC
	abi.RegisteredSealProof_StackedDrg2KiBV1:   builtin.EpochsInDay + PreCommitChallengeDelay,
	abi.RegisteredSealProof_StackedDrg8MiBV1:   builtin.EpochsInDay + PreCommitChallengeDelay,
	abi.RegisteredSealProof_StackedDrg512MiBV1: builtin.EpochsInDay + PreCommitChallengeDelay,
	abi.RegisteredSealProof_StackedDrg64GiBV1:  builtin.EpochsInDay + PreCommitChallengeDelay,

	abi.RegisteredSealProof_StackedDrg32GiBV1_1:  9*builtin.EpochsInDay + PreCommitChallengeDelay, // PARAM_SPEC
	abi.RegisteredSealProof_StackedDrg2KiBV1_1:   9*builtin.EpochsInDay + PreCommitChallengeDelay,
	abi.RegisteredSealProof_StackedDrg8MiBV1_1:   9*builtin.EpochsInDay + PreCommitChallengeDelay,
	abi.RegisteredSealProof_StackedDrg512MiBV1_1: 9*builtin.EpochsInDay + PreCommitChallengeDelay,
	abi.RegisteredSealProof_StackedDrg64GiBV1_1:  9*builtin.EpochsInDay + PreCommitChallengeDelay,
}

// The maximum number of sector pre-commitments in a single batch.
// 32 sectors per epoch would support a single miner onboarding 1EiB of 32GiB sectors in 1 year.
const PreCommitSectorBatchMaxSize = 256

// Maximum delay between challenge and pre-commitment.
// This prevents a miner sealing sectors far in advance of committing them to the chain, thus committing to a
// particular chain.
var MaxPreCommitRandomnessLookback = builtin.EpochsInDay + ChainFinality // PARAM_SPEC

// Number of epochs between publishing a sector pre-commitment and when the challenge for interactive PoRep is drawn.
// This (1) prevents a miner predicting a challenge before staking their pre-commit deposit, and
// (2) prevents a miner attempting a long fork in the past to insert a pre-commitment after seeing the challenge.
var PreCommitChallengeDelay = abi.ChainEpoch(150) // PARAM_SPEC

// Lookback from the deadline's challenge window opening from which to sample chain randomness for the WindowPoSt challenge seed.
// This means that deadline windows can be non-overlapping (which make the programming simpler) without requiring a
// miner to wait for chain stability during the challenge window.
// This value cannot be too large lest it compromise the rationality of honest storage (from Window PoSt cost assumptions).
const WPoStChallengeLookback = abi.ChainEpoch(20) // PARAM_SPEC

// Minimum period between fault declaration and the next deadline opening.
// If the number of epochs between fault declaration and deadline's challenge window opening is lower than FaultDeclarationCutoff,
// the fault declaration is considered invalid for that deadline.
// This guarantees that a miner is not likely to successfully fork the chain and declare a fault after seeing the challenges.
const FaultDeclarationCutoff = WPoStChallengeLookback + 50 // PARAM_SPEC

// The maximum age of a fault before the sector is terminated.
// This bounds the time a miner can lose client's data before sacrificing pledge and deal collateral.
var FaultMaxAge = WPoStProvingPeriod * 14 // PARAM_SPEC

// Staging period for a miner worker key change.
// This delay prevents a miner choosing a more favorable worker key that wins leader elections.
const WorkerKeyChangeDelay = ChainFinality // PARAM_SPEC

// Minimum number of epochs past the current epoch a sector may be set to expire.
const MinSectorExpiration = 180 * builtin.EpochsInDay // PARAM_SPEC

// The maximum number of epochs past the current epoch that sector lifetime may be extended.
// A sector may be extended multiple times, however, the total maximum lifetime is also bounded by
// the associated seal proof's maximum lifetime.
const MaxSectorExpirationExtension = 270 * builtin.EpochsInDay // PARAM_SPEC

// Ratio of sector size to maximum number of deals per sector.
// The maximum number of deals is the sector size divided by this number (2^27)
// which limits 32GiB sectors to 256 deals and 64GiB sectors to 512
const DealLimitDenominator = 134217728 // PARAM_SPEC

// Number of epochs after a consensus fault for which a miner is ineligible
// for permissioned actor methods and winning block elections.
const ConsensusFaultIneligibilityDuration = ChainFinality

// DealWeight and VerifiedDealWeight are spacetime occupied by regular deals and verified deals in a sector.
// Sum of DealWeight and VerifiedDealWeight should be less than or equal to total SpaceTime of a sector.
// Sectors full of VerifiedDeals will have a SectorQuality of VerifiedDealWeightMultiplier/QualityBaseMultiplier.
// Sectors full of Deals will have a SectorQuality of DealWeightMultiplier/QualityBaseMultiplier.
// Sectors with neither will have a SectorQuality of QualityBaseMultiplier/QualityBaseMultiplier.
// SectorQuality of a sector is a weighted average of multipliers based on their proportions.
func QualityForWeight(size abi.SectorSize, duration abi.ChainEpoch, dealWeight, verifiedWeight abi.DealWeight) abi.SectorQuality {
	// sectorSpaceTime = size * duration
	sectorSpaceTime := big.Mul(big.NewIntUnsigned(uint64(size)), big.NewInt(int64(duration)))
	// totalDealSpaceTime = dealWeight + verifiedWeight
	totalDealSpaceTime := big.Add(dealWeight, verifiedWeight)

	// Base - all size * duration of non-deals
	// weightedBaseSpaceTime = (sectorSpaceTime - totalDealSpaceTime) * QualityBaseMultiplier
	weightedBaseSpaceTime := big.Mul(big.Sub(sectorSpaceTime, totalDealSpaceTime), builtin.QualityBaseMultiplier)
	// Deal - all deal size * deal duration * 10
	// weightedDealSpaceTime = dealWeight * DealWeightMultiplier
	weightedDealSpaceTime := big.Mul(dealWeight, builtin.DealWeightMultiplier)
	// Verified - all verified deal size * verified deal duration * 100
	// weightedVerifiedSpaceTime = verifiedWeight * VerifiedDealWeightMultiplier
	weightedVerifiedSpaceTime := big.Mul(verifiedWeight, builtin.VerifiedDealWeightMultiplier)
	// Sum - sum of all spacetime
	// weightedSumSpaceTime = weightedBaseSpaceTime + weightedDealSpaceTime + weightedVerifiedSpaceTime
	weightedSumSpaceTime := big.Sum(weightedBaseSpaceTime, weightedDealSpaceTime, weightedVerifiedSpaceTime)
	// scaledUpWeightedSumSpaceTime = weightedSumSpaceTime * 2^20
	scaledUpWeightedSumSpaceTime := big.Lsh(weightedSumSpaceTime, builtin.SectorQualityPrecision)

	// Average of weighted space time: (scaledUpWeightedSumSpaceTime / sectorSpaceTime * 10)
	return big.Div(big.Div(scaledUpWeightedSumSpaceTime, sectorSpaceTime), builtin.QualityBaseMultiplier)
}

// The power for a sector size, committed duration, and weight.
func QAPowerForWeight(size abi.SectorSize, duration abi.ChainEpoch, dealWeight, verifiedWeight abi.DealWeight) abi.StoragePower {
	quality := QualityForWeight(size, duration, dealWeight, verifiedWeight)
	return big.Rsh(big.Mul(big.NewIntUnsigned(uint64(size)), quality), builtin.SectorQualityPrecision)
}

// The quality-adjusted power for a sector.
func QAPowerForSector(size abi.SectorSize, sector *SectorOnChainInfo) abi.StoragePower {
	duration := sector.Expiration - sector.Activation
	return QAPowerForWeight(size, duration, sector.DealWeight, sector.VerifiedDealWeight)
}

// Determine maximum number of deal miner's sector can hold
func SectorDealsMax(size abi.SectorSize) uint64 {
	return max64(256, uint64(size/DealLimitDenominator))
}

// Default share of block reward allocated as reward to the consensus fault reporter.
// Applied as epochReward / (expectedLeadersPerEpoch * consensusFaultReporterDefaultShare)
const consensusFaultReporterDefaultShare int64 = 4

// Specification for a linear vesting schedule.
type VestSpec struct {
	InitialDelay abi.ChainEpoch // Delay before any amount starts vesting.
	VestPeriod   abi.ChainEpoch // Period over which the total should vest, after the initial delay.
	StepDuration abi.ChainEpoch // Duration between successive incremental vests (independent of vesting period).
	Quantization abi.ChainEpoch // Maximum precision of vesting table (limits cardinality of table).
}

// The vesting schedule for total rewards (block reward + gas reward) earned by a block producer.
var RewardVestingSpec = VestSpec{ // PARAM_SPEC
	InitialDelay: abi.ChainEpoch(0),
	VestPeriod:   abi.ChainEpoch(90 * builtin.EpochsInDay),
	StepDuration: abi.ChainEpoch(1 * builtin.EpochsInDay),
	Quantization: 12 * builtin.EpochsInHour,
}

// When an actor reports a consensus fault, they earn a share of the penalty paid by the miner.
func RewardForConsensusSlashReport(epochReward abi.TokenAmount) abi.TokenAmount {
	return big.Div(epochReward,
		big.Mul(big.NewInt(builtin.ExpectedLeadersPerEpoch),
			big.NewInt(consensusFaultReporterDefaultShare)),
	)
}

// The reward given for successfully disputing a window post.
func RewardForDisputedWindowPoSt(proofType abi.RegisteredPoStProof, disputedPower PowerPair) abi.TokenAmount {
	// This is currently just the base. In the future, the fee may scale based on the disputed power.
	return BaseRewardForDisputedWindowPoSt
}

const MaxAggregatedSectors = 819
const MinAggregatedSectors = 4
const MaxAggregateProofSize = 81960

// The delay between pre commit expiration and clean up from state. This enforces that expired pre-commits
// stay in state for a period of time creating a grace period during which a late-running aggregated prove-commit
// can still prove its non-expired precommits without resubmitting a message
const ExpiredPreCommitCleanUpDelay = 8 * builtin.EpochsInHour
