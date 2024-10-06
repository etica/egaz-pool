package payouts

import (
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yuriy0803/core-geth1/common/math"

	"github.com/yuriy0803/open-etc-pool-friends/rpc"
	"github.com/yuriy0803/open-etc-pool-friends/storage"
	"github.com/yuriy0803/open-etc-pool-friends/util"
)

type UnlockerConfig struct {
	Enabled                 bool     `json:"enabled"`
	PoolFee                 float64  `json:"poolFee"`
	PoolFeeAddress          string   `json:"poolFeeAddress"`
	Depth                   int64    `json:"depth"`
	ImmatureDepth           int64    `json:"immatureDepth"`
	KeepTxFees              bool     `json:"keepTxFees"`
	Interval                string   `json:"interval"`
	Daemon                  string   `json:"daemon"`
	Timeout                 string   `json:"timeout"`
	Ecip1017FBlock          int64    `json:"ecip1017FBlock"`
	Ecip1017EraRounds       *big.Int `json:"ecip1017EraRounds"`
	ByzantiumFBlock         *big.Int `json:"byzantiumFBlock"`
	ConstantinopleFBlock    *big.Int `json:"constantinopleFBlock"`
	Network                 string   `json:"network"`
	IsLondonHardForkEnabled bool     `json:"isLondonHardForkEnabled"`
}

const minDepth = 16

// London hark fork
const londonHardForkHeight = 12965000

// Universal block reward ethash
const UniversalHardForkHeight = 0

var UniversalBlockReward = math.MustParseBig256("2000000000000000000") // 2.00
var UniversalUncleReward = math.MustParseBig256("1750000000000000000") // 1.75

// params for etchash
var homesteadReward = math.MustParseBig256("5000000000000000000")
var disinflationRateQuotient = big.NewInt(4) // Disinflation rate quotient for ECIP1017
var disinflationRateDivisor = big.NewInt(5)  // Disinflation rate divisor for ECIP1017
// params for ethash
var frontierBlockReward = big.NewInt(5e+18)
var byzantiumBlockReward = big.NewInt(3e+18)
var constantinopleBlockReward = big.NewInt(2e+18)

// params for ubqhash
var ubiqStartReward = big.NewInt(8e+18)

// params for Octaspace
var octaspaceStartReward = big.NewInt(650e+16)

// params for expanse
const byzantiumHardForkHeight = 800000

var homesteadExpanseReward = math.MustParseBig256("8000000000000000000")
var byzantiumExpanseReward = math.MustParseBig256("4000000000000000000")

// misc consts
var big32 = big.NewInt(32)
var big8 = big.NewInt(8)
var big2 = big.NewInt(2)

// Donate 1% from pool fees to developers
const donationFee = 1.0
const donationAccount = "0xc4D275cF97D6A4cC29EDe86d16976E808D264732"

type BlockUnlocker struct {
	config   *UnlockerConfig
	backend  *storage.RedisClient
	rpc      *rpc.RPCClient
	halt     bool
	lastFail error
}

func NewBlockUnlocker(cfg *UnlockerConfig, backend *storage.RedisClient, network string) *BlockUnlocker {
	// determine which monetary policy to use based on network
	// configure any reward params if needed.
	if network == "classic" {
		cfg.Ecip1017FBlock = 5000000
		cfg.Ecip1017EraRounds = big.NewInt(5000000)
	} else if network == "mordor" {
		cfg.Ecip1017FBlock = 0
		cfg.Ecip1017EraRounds = big.NewInt(2000000)
	} else if network == "ethereum" {
		cfg.ByzantiumFBlock = big.NewInt(4370000)
		cfg.ConstantinopleFBlock = big.NewInt(7280000)
	} else if network == "ethereumPow" {
		// nothing needs configuring here, simply proceed.
	} else if network == "ethereumFair" {
		cfg.ByzantiumFBlock = big.NewInt(4370000)
		cfg.ConstantinopleFBlock = big.NewInt(7280000)
	} else if network == "ropsten" {
		cfg.ByzantiumFBlock = big.NewInt(1700000)
		cfg.ConstantinopleFBlock = big.NewInt(4230000)
	} else if network == "expanse" {
		// nothing needs configuring here, simply proceed.
	} else if network == "etica" {
		// nothing needs configuring here, simply proceed.
	} else if network == "ubiq" {
		// nothing needs configuring here, simply proceed.
	} else if network == "octaspace" {
		// nothing needs configuring here, simply proceed.
	} else if network == "universal" {
		// nothing needs configuring here, simply proceed.
	} else {
		log.Fatalln("Invalid network set", network)
	}

	cfg.Network = network

	if len(cfg.PoolFeeAddress) != 0 && !util.IsValidHexAddress(cfg.PoolFeeAddress) {
		log.Fatalln("Invalid poolFeeAddress", cfg.PoolFeeAddress)
	}
	if cfg.Depth < minDepth*2 {
		log.Fatalf("Block maturity depth can't be < %v, your depth is %v", minDepth*2, cfg.Depth)
	}
	if cfg.ImmatureDepth < minDepth {
		log.Fatalf("Immature depth can't be < %v, your depth is %v", minDepth, cfg.ImmatureDepth)
	}
	u := &BlockUnlocker{config: cfg, backend: backend}
	u.rpc = rpc.NewRPCClient("BlockUnlocker", cfg.Daemon, cfg.Timeout)
	return u
}

func (u *BlockUnlocker) Start() {
	log.Println("Starting block unlocker")
	intv := util.MustParseDuration(u.config.Interval)
	timer := time.NewTimer(intv)
	log.Printf("Set block unlock interval to %v", intv)

	// Immediately unlock after start
	u.unlockPendingBlocks()
	u.unlockAndCreditMiners()
	timer.Reset(intv)

	go func() {
		for {
			select {
			case <-timer.C:
				u.unlockPendingBlocks()
				u.unlockAndCreditMiners()
				timer.Reset(intv)
			}
		}
	}()
}

type UnlockResult struct {
	maturedBlocks  []*storage.BlockData
	orphanedBlocks []*storage.BlockData
	orphans        int
	uncles         int
	blocks         int
}

/* Geth does not provide consistent state when you need both new height and new job,
 * so in redis I am logging just what I have in a pool state on the moment when block found.
 * Having very likely incorrect height in database results in a weird block unlocking scheme,
 * when I have to check what the hell we actually found and traversing all the blocks with height-N and height+N
 * to make sure we will find it. We can't rely on round height here, it's just a reference point.
 * ISSUE: https://github.com/ethereum/go-ethereum/issues/2333
 */
func (u *BlockUnlocker) unlockCandidates(candidates []*storage.BlockData) (*UnlockResult, error) {
	result := &UnlockResult{}

	// Data row is: "height:nonce:powHash:mixDigest:timestamp:diff:totalShares"
	for _, candidate := range candidates {
		orphan := true

		/* Search for a normal block with wrong height here by traversing 16 blocks back and forward.
		 * Also we are searching for a block that can include this one as uncle.
		 */
		if candidate.Height < minDepth {
			orphan = false
			// avoid scanning the first 16 blocks
			continue
		}
		for i := int64(minDepth * -1); i < minDepth; i++ {
			height := candidate.Height + i

			if height < 0 {
				continue
			}

			block, err := u.rpc.GetBlockByHeight(height)

			if err != nil {
				log.Printf("Error while retrieving block %v from node: %v", height, err)
				return nil, err
			}
			if block == nil {
				return nil, fmt.Errorf("Error while retrieving block %v from node, wrong node height", height)
			}

			if matchCandidate(block, candidate) {
				orphan = false
				result.blocks++

				err = u.handleBlock(block, candidate)
				if err != nil {
					u.halt = true
					u.lastFail = err
					return nil, err
				}
				result.maturedBlocks = append(result.maturedBlocks, candidate)
				log.Printf("Mature block %v with %v tx, hash: %v", candidate.Height, len(block.Transactions), candidate.Hash[0:10])
				break
			}

			if len(block.Uncles) == 0 {
				continue
			}

			// Trying to find uncle in current block during our forward check
			for uncleIndex, uncleHash := range block.Uncles {
				uncle, err := u.rpc.GetUncleByBlockNumberAndIndex(height, uncleIndex)
				if err != nil {
					return nil, fmt.Errorf("Error while retrieving uncle of block %v from node: %v", uncleHash, err)
				}
				if uncle == nil {
					return nil, fmt.Errorf("Error while retrieving uncle of block %v from node", height)
				}

				// Found uncle
				if matchCandidate(uncle, candidate) {
					orphan = false
					result.uncles++

					err := handleUncle(height, uncle, candidate, u.config)
					if err != nil {
						u.halt = true
						u.lastFail = err
						return nil, err
					}
					result.maturedBlocks = append(result.maturedBlocks, candidate)
					log.Printf("Mature uncle %v/%v of reward %v with hash: %v", candidate.Height, candidate.UncleHeight,
						util.FormatReward(candidate.Reward), uncle.Hash[0:10])
					break
				}
			}
			// Found block or uncle
			if !orphan {
				break
			}
		}
		// Block is lost, we didn't find any valid block or uncle matching our data in a blockchain
		if orphan {
			result.orphans++
			candidate.Orphan = true
			result.orphanedBlocks = append(result.orphanedBlocks, candidate)
			log.Printf("Orphaned block %v:%v", candidate.RoundHeight, candidate.Nonce)
		}
	}
	return result, nil
}

func matchCandidate(block *rpc.GetBlockReply, candidate *storage.BlockData) bool {
	// Just compare hash if block is unlocked as immature
	if len(candidate.Hash) > 0 && strings.EqualFold(candidate.Hash, block.Hash) {
		return true
	}
	// Geth-style candidate matching
	if len(block.Nonce) > 0 {
		return strings.EqualFold(block.Nonce, candidate.Nonce)
	}
	// Parity's EIP: https://github.com/ethereum/EIPs/issues/95
	if len(block.SealFields) == 2 {
		return strings.EqualFold(candidate.Nonce, block.SealFields[1])
	}
	return false
}

func (u *BlockUnlocker) handleBlock(block *rpc.GetBlockReply, candidate *storage.BlockData) error {
	correctHeight, err := strconv.ParseInt(strings.Replace(block.Number, "0x", "", -1), 16, 64)
	if err != nil {
		return err
	}
	candidate.Height = correctHeight
	var reward *big.Int = big.NewInt(0)
	if u.config.Network == "classic" || u.config.Network == "mordor" {
		era := GetBlockEra(big.NewInt(candidate.Height), u.config.Ecip1017EraRounds)
		reward = getConstReward(era)
		// Add reward for including uncles
		uncleReward := getRewardForUncle(reward)
		rewardForUncles := big.NewInt(0).Mul(uncleReward, big.NewInt(int64(len(block.Uncles))))
		reward.Add(reward, rewardForUncles)

	} else if u.config.Network == "ubiq" {
		reward = getConstRewardUbiq(candidate.Height)
		// Add reward for including uncles
		uncleReward := new(big.Int).Div(reward, big32)
		rewardForUncles := big.NewInt(0).Mul(uncleReward, big.NewInt(int64(len(block.Uncles))))
		reward.Add(reward, rewardForUncles)

	} else if u.config.Network == "expanse" {
		reward = getConstRewardExpanse(candidate.Height)
		// Add reward for including uncles
		uncleReward := new(big.Int).Div(reward, big32)
		rewardForUncles := big.NewInt(0).Mul(uncleReward, big.NewInt(int64(len(block.Uncles))))
		reward.Add(reward, rewardForUncles)

	} else if u.config.Network == "etica" {
		reward = getConstRewardetica(candidate.Height)
		// Add reward for including uncles
		uncleReward := new(big.Int).Div(reward, big32)
		rewardForUncles := big.NewInt(0).Mul(uncleReward, big.NewInt(int64(len(block.Uncles))))
		reward.Add(reward, rewardForUncles)

	} else if u.config.Network == "ethereumPow" {
		reward = getConstRewardEthereumpow(candidate.Height)
		// Add reward for including uncles
		uncleReward := new(big.Int).Div(reward, big32)
		rewardForUncles := big.NewInt(0).Mul(uncleReward, big.NewInt(int64(len(block.Uncles))))
		reward.Add(reward, rewardForUncles)

	} else if u.config.Network == "ethereum" || u.config.Network == "ropsten" || u.config.Network == "ethereumFair" {
		reward = getConstRewardEthereum(candidate.Height, u.config)
		// Add reward for including uncles
		uncleReward := new(big.Int).Div(reward, big32)
		rewardForUncles := big.NewInt(0).Mul(uncleReward, big.NewInt(int64(len(block.Uncles))))
		reward.Add(reward, rewardForUncles)
	} else if u.config.Network == "octaspace" {
		reward = getConstRewardOctaspace(candidate.Height)
		// Add reward for including uncles
		uncleReward := new(big.Int).Div(reward, big32)
		rewardForUncles := big.NewInt(0).Mul(uncleReward, big.NewInt(int64(len(block.Uncles))))
		reward.Add(reward, rewardForUncles)
	} else if u.config.Network == "universal" {
		reward = getConstRewardUniversal(candidate.Height)
		// Add reward for including uncles
		uncleReward := new(big.Int).Div(reward, big32)
		rewardForUncles := big.NewInt(0).Mul(uncleReward, big.NewInt(int64(len(block.Uncles))))
		reward.Add(reward, rewardForUncles)
	} else {
		log.Fatalln("Invalid network set", u.config.Network)
	}

	// Add TX fees
	extraTxReward, err := u.getExtraRewardForTx(block)
	if err != nil {
		return fmt.Errorf("Error while fetching TX receipt: %v", err)
	}
	if u.config.KeepTxFees {
		candidate.ExtraReward = extraTxReward
	} else {
		reward.Add(reward, extraTxReward)
	}

	// Remove Burnt Fees, post London (Base Fee Per Gas * Gas Used)
	baseFeePerGas := new(big.Int)
	bigBaseFeePerGas := util.DecodeValueHex(block.BaseFeePerGas)
	baseFeePerGas.SetString(bigBaseFeePerGas, 10)
	log.Println("baseFeePerGas: ", baseFeePerGas)
	log.Println("block.BaseFeePerGas: ", block.BaseFeePerGas)

	//gasUsed := big.NewInt(int64(block.GasUsed))
	gasUsed := new(big.Int)
	bigGasUsed := util.DecodeValueHex(block.GasUsed)
	gasUsed.SetString(bigGasUsed, 10)
	log.Println("gasUsed: ", gasUsed)

	burntFees := new(big.Int).Mul(baseFeePerGas, gasUsed)
	log.Println("BurntFees: ", burntFees)
	reward.Sub(reward, burntFees)

	candidate.Orphan = false
	candidate.Hash = block.Hash
	candidate.Reward = reward
	return nil
}

func handleUncle(height int64, uncle *rpc.GetBlockReply, candidate *storage.BlockData, cfg *UnlockerConfig) error {
	uncleHeight, err := strconv.ParseInt(strings.Replace(uncle.Number, "0x", "", -1), 16, 64)
	if err != nil {
		return err
	}
	var reward *big.Int = big.NewInt(0)
	if cfg.Network == "classic" || cfg.Network == "mordor" {
		era := GetBlockEra(big.NewInt(height), cfg.Ecip1017EraRounds)
		reward = getUncleReward(new(big.Int).SetInt64(uncleHeight), new(big.Int).SetInt64(height), era, getConstReward(era))
	} else if cfg.Network == "ubiq" {
		reward = getUncleRewardUbiq(new(big.Int).SetInt64(uncleHeight), new(big.Int).SetInt64(height), getConstRewardUbiq(height))
	} else if cfg.Network == "expanse" {
		reward = getUncleRewardExpanse(new(big.Int).SetInt64(uncleHeight), new(big.Int).SetInt64(height), getConstRewardExpanse(height))
	} else if cfg.Network == "etica" {
		reward = getUncleRewardEthereum(new(big.Int).SetInt64(uncleHeight), new(big.Int).SetInt64(height), getConstRewardetica(height))
	} else if cfg.Network == "ethereumPow" {
		reward = getUncleRewardEthereumpow(new(big.Int).SetInt64(uncleHeight), new(big.Int).SetInt64(height), getConstRewardEthereumpow(height))
	} else if cfg.Network == "ethereum" || cfg.Network == "ropsten" || cfg.Network == "ethereumFair" {
		reward = getUncleRewardEthereum(new(big.Int).SetInt64(uncleHeight), new(big.Int).SetInt64(height), getConstRewardUbiq(height))
	} else if cfg.Network == "octaspace" {
		reward = getUncleRewardOctaspace(new(big.Int).SetInt64(uncleHeight), new(big.Int).SetInt64(height), getConstRewardOctaspace(height))
	} else if cfg.Network == "universal" {
		reward = getUncleRewardUniversal(new(big.Int).SetInt64(uncleHeight), new(big.Int).SetInt64(height), getConstRewardUniversal(height))
	}

	candidate.Height = height
	candidate.UncleHeight = uncleHeight
	candidate.Orphan = false
	candidate.Hash = uncle.Hash
	candidate.Reward = reward
	return nil
}

func (u *BlockUnlocker) unlockPendingBlocks() {
	if u.halt {
		log.Println("Unlocking suspended due to last critical error:", u.lastFail)
		os.Exit(1)
		return
	}

	current, err := u.rpc.GetPendingBlock()
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Unable to get current blockchain height from node: %v", err)
		return
	}
	currentHeight, err := strconv.ParseInt(strings.Replace(current.Number, "0x", "", -1), 16, 64)
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Can't parse pending block number: %v", err)
		return
	}

	candidates, err := u.backend.GetCandidates(currentHeight - u.config.ImmatureDepth)
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Failed to get block candidates from backend: %v", err)
		return
	}

	if len(candidates) == 0 {
		log.Println("No block candidates to unlock")
		return
	}

	result, err := u.unlockCandidates(candidates)
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Failed to unlock blocks: %v", err)
		return
	}
	log.Printf("Immature %v blocks, %v uncles, %v orphans", result.blocks, result.uncles, result.orphans)

	err = u.backend.WritePendingOrphans(result.orphanedBlocks)
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Failed to insert orphaned blocks into backend: %v", err)
		return
	} else {
		log.Printf("Inserted %v orphaned blocks to backend", result.orphans)
	}

	totalRevenue := new(big.Rat)
	totalMinersProfit := new(big.Rat)
	totalPoolProfit := new(big.Rat)

	for _, block := range result.maturedBlocks {
		revenue, minersProfit, poolProfit, roundRewards, percents, err := u.calculateRewards(block)
		if err != nil {
			u.halt = true
			u.lastFail = err
			log.Printf("Failed to calculate rewards for round %v: %v", block.RoundKey(), err)
			return
		}
		err = u.backend.WriteImmatureBlock(block, roundRewards)
		if err != nil {
			u.halt = true
			u.lastFail = err
			log.Printf("Failed to credit rewards for round %v: %v", block.RoundKey(), err)
			return
		}
		totalRevenue.Add(totalRevenue, revenue)
		totalMinersProfit.Add(totalMinersProfit, minersProfit)
		totalPoolProfit.Add(totalPoolProfit, poolProfit)

		logEntry := fmt.Sprintf(
			"IMMATURE %v: revenue %v, miners profit %v, pool profit: %v",
			block.RoundKey(),
			util.FormatRatReward(revenue),
			util.FormatRatReward(minersProfit),
			util.FormatRatReward(poolProfit),
		)
		entries := []string{logEntry}
		for login, reward := range roundRewards {
			entries = append(entries, fmt.Sprintf("\tREWARD %v: %v: %v Shannon", block.RoundKey(), login, reward))
			per := new(big.Rat)
			if val, ok := percents[login]; ok {
				per = val
			}
			u.backend.WriteReward(login, reward, per, true, block)
		}
		log.Println(strings.Join(entries, "\n"))
	}

	log.Printf(
		"IMMATURE SESSION: revenue %v, miners profit %v, pool profit: %v",
		util.FormatRatReward(totalRevenue),
		util.FormatRatReward(totalMinersProfit),
		util.FormatRatReward(totalPoolProfit),
	)
}

func (u *BlockUnlocker) unlockAndCreditMiners() {
	if u.halt {
		log.Println("Unlocking suspended due to last critical error:", u.lastFail)
		return
	}

	current, err := u.rpc.GetPendingBlock()
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Unable to get current blockchain height from node: %v", err)
		return
	}
	currentHeight, err := strconv.ParseInt(strings.Replace(current.Number, "0x", "", -1), 16, 64)
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Can't parse pending block number: %v", err)
		return
	}

	immature, err := u.backend.GetImmatureBlocks(currentHeight - u.config.Depth)
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Failed to get block candidates from backend: %v", err)
		return
	}

	if len(immature) == 0 {
		log.Println("No immature blocks to credit miners")
		return
	}

	result, err := u.unlockCandidates(immature)
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Failed to unlock blocks: %v", err)
		return
	}
	log.Printf("Unlocked %v blocks, %v uncles, %v orphans", result.blocks, result.uncles, result.orphans)

	for _, block := range result.orphanedBlocks {
		err = u.backend.WriteOrphan(block)
		if err != nil {
			u.halt = true
			u.lastFail = err
			log.Printf("Failed to insert orphaned block into backend: %v", err)
			return
		}
	}
	log.Printf("Inserted %v orphaned blocks to backend", result.orphans)

	totalRevenue := new(big.Rat)
	totalMinersProfit := new(big.Rat)
	totalPoolProfit := new(big.Rat)

	for _, block := range result.maturedBlocks {
		revenue, minersProfit, poolProfit, roundRewards, percents, err := u.calculateRewards(block)
		if err != nil {
			u.halt = true
			u.lastFail = err
			log.Printf("Failed to calculate rewards for round %v: %v", block.RoundKey(), err)
			return
		}
		err = u.backend.WriteMaturedBlock(block, roundRewards)
		if err != nil {
			u.halt = true
			u.lastFail = err
			log.Printf("Failed to credit rewards for round %v: %v", block.RoundKey(), err)
			return
		}
		totalRevenue.Add(totalRevenue, revenue)
		totalMinersProfit.Add(totalMinersProfit, minersProfit)
		totalPoolProfit.Add(totalPoolProfit, poolProfit)

		logEntry := fmt.Sprintf(
			"MATURED %v: revenue %v, miners profit %v, pool profit: %v",
			block.RoundKey(),
			util.FormatRatReward(revenue),
			util.FormatRatReward(minersProfit),
			util.FormatRatReward(poolProfit),
		)
		entries := []string{logEntry}
		for login, reward := range roundRewards {
			entries = append(entries, fmt.Sprintf("\tREWARD %v: %v: %v Shannon", block.RoundKey(), login, reward))
			per := new(big.Rat)
			if val, ok := percents[login]; ok {
				per = val
			}
			u.backend.WriteReward(login, reward, per, false, block)
		}
		log.Println(strings.Join(entries, "\n"))
	}

	log.Printf(
		"MATURE SESSION: revenue %v, miners profit %v, pool profit: %v",
		util.FormatRatReward(totalRevenue),
		util.FormatRatReward(totalMinersProfit),
		util.FormatRatReward(totalPoolProfit),
	)
}

func (u *BlockUnlocker) calculateRewards(block *storage.BlockData) (*big.Rat, *big.Rat, *big.Rat, map[string]int64, map[string]*big.Rat, error) {
	revenue := new(big.Rat).SetInt(block.Reward)
	minersProfit, poolProfit := chargeFee(revenue, u.config.PoolFee)

	shares, err := u.backend.GetRoundShares(block.RoundHeight, block.Nonce)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	totalShares := int64(0)
	for _, val := range shares {
		totalShares += val
	}

	if block.MiningType == "solo" {
		rewards, percents := calculateRewardsForFinder(block.Finder, totalShares, minersProfit)
		if block.ExtraReward != nil {
			extraReward := new(big.Rat).SetInt(block.ExtraReward)
			poolProfit.Add(poolProfit, extraReward)
			revenue.Add(revenue, extraReward)
		}

		if len(u.config.PoolFeeAddress) != 0 {
			address := strings.ToLower(u.config.PoolFeeAddress)
			rewards[address] += weiToShannonInt64(poolProfit)
		}
		return revenue, minersProfit, poolProfit, rewards, percents, nil
	} else {
		rewards, percents := calculateRewardsForShares(shares, totalShares, minersProfit, u)
		if block.ExtraReward != nil {
			extraReward := new(big.Rat).SetInt(block.ExtraReward)
			poolProfit.Add(poolProfit, extraReward)
			revenue.Add(revenue, extraReward)
		}

		if len(u.config.PoolFeeAddress) != 0 {
			address := strings.ToLower(u.config.PoolFeeAddress)
			rewards[address] += weiToShannonInt64(poolProfit)
		}
		return revenue, minersProfit, poolProfit, rewards, percents, nil
	}

}

func calculateRewardsForShares(shares map[string]int64, total int64, reward *big.Rat, u *BlockUnlocker) (map[string]int64, map[string]*big.Rat) {
	rewards := make(map[string]int64)
	percents := make(map[string]*big.Rat)

	for login, n := range shares {

		percents[login] = big.NewRat(n, total)
		workerReward := new(big.Rat).Mul(reward, percents[login])
		rewards[login] += weiToShannonInt64(workerReward)
	}
	return rewards, percents
}

func calculateRewardsForFinder(finder string, total int64, reward *big.Rat) (map[string]int64, map[string]*big.Rat) {
	rewards := make(map[string]int64)
	percents := make(map[string]*big.Rat)

	login := finder
	fmt.Print(total)
	if total == 0 {
		total = 1
	}
	percents[login] = big.NewRat(total, total)
	workerReward := new(big.Rat).Mul(reward, percents[login])
	rewards[login] += weiToShannonInt64(workerReward)

	return rewards, percents
}

// Returns new value after fee deduction and fee value.
func chargeFee(value *big.Rat, fee float64) (*big.Rat, *big.Rat) {
	feePercent := new(big.Rat).SetFloat64(fee / 100)
	feeValue := new(big.Rat).Mul(value, feePercent)
	return new(big.Rat).Sub(value, feeValue), feeValue
}

func weiToShannonInt64(wei *big.Rat) int64 {
	shannon := new(big.Rat).SetInt(util.Shannon)
	inShannon := new(big.Rat).Quo(wei, shannon)
	value, _ := strconv.ParseInt(inShannon.FloatString(0), 10, 64)
	return value
}

// GetRewardByEra gets a block reward at disinflation rate.
// Constants MaxBlockReward, DisinflationRateQuotient, and DisinflationRateDivisor assumed.
func GetBlockWinnerRewardByEra(era *big.Int, blockReward *big.Int) *big.Int {
	if era.Cmp(big.NewInt(0)) == 0 {
		return new(big.Int).Set(blockReward)
	}

	// MaxBlockReward _r_ * (4/5)**era == MaxBlockReward * (4**era) / (5**era)
	// since (q/d)**n == q**n / d**n
	// qed
	var q, d, r *big.Int = new(big.Int), new(big.Int), new(big.Int)

	q.Exp(disinflationRateQuotient, era, nil)
	d.Exp(disinflationRateDivisor, era, nil)

	r.Mul(blockReward, q)
	r.Div(r, d)

	return r
}

// GetBlockEra gets which "Era" a given block is within, given an era length (ecip-1017 has era=5,000,000 blocks)
// Returns a zero-index era number, so "Era 1": 0, "Era 2": 1, "Era 3": 2 ...
func GetBlockEra(blockNum, eraLength *big.Int) *big.Int {
	// If genesis block or impossible negative-numbered block, return zero-val.
	if blockNum.Sign() < 1 {
		return new(big.Int)
	}

	remainder := big.NewInt(0).Mod(big.NewInt(0).Sub(blockNum, big.NewInt(1)), eraLength)
	base := big.NewInt(0).Sub(blockNum, remainder)

	d := big.NewInt(0).Div(base, eraLength)
	dremainder := big.NewInt(0).Mod(d, big.NewInt(1))

	return new(big.Int).Sub(d, dremainder)
}

// etchash
func getConstReward(era *big.Int) *big.Int {
	var blockReward = homesteadReward
	wr := GetBlockWinnerRewardByEra(era, blockReward)
	return wr
}

// etchash
func getRewardForUncle(blockReward *big.Int) *big.Int {
	return new(big.Int).Div(blockReward, big32) //return new(big.Int).Div(reward, new(big.Int).SetInt64(32))
}

// etchash
func getUncleReward(uHeight *big.Int, height *big.Int, era *big.Int, reward *big.Int) *big.Int {
	// Era 1 (index 0):
	//   An extra reward to the winning miner for including uncles as part of the block, in the form of an extra 1/32 (0.15625ETC) per uncle included, up to a maximum of two (2) uncles.
	if era.Cmp(big.NewInt(0)) == 0 {
		r := new(big.Int)
		r.Add(uHeight, big8) // 2,534,998 + 8              = 2,535,006
		r.Sub(r, height)     // 2,535,006 - 2,534,999        = 7
		r.Mul(r, reward)     // 7 * 5e+18               = 35e+18
		r.Div(r, big8)       // 35e+18 / 8                            = 7/8 * 5e+18

		return r
	}
	return getRewardForUncle(reward)
}

// expanse
func getConstRewardExpanse(height int64) *big.Int {
	if height >= byzantiumHardForkHeight {
		return new(big.Int).Set(byzantiumExpanseReward)
	}
	return new(big.Int).Set(homesteadExpanseReward)
}

func getConstRewardEthereumpow(height int64) *big.Int {
	// Rewards)
	// EthereumPow
	return calcBigNumber(2.0)
}

// ubqhash
func getConstRewardUbiq(height int64) *big.Int {
	// Rewards
	reward := new(big.Int).Set(ubiqStartReward)
	headerNumber := big.NewInt(height)

	if headerNumber.Cmp(big.NewInt(358363)) > 0 {
		reward = big.NewInt(7e+18)
		// Year 1
	}
	if headerNumber.Cmp(big.NewInt(716727)) > 0 {
		reward = big.NewInt(6e+18)
		// Year 2
	}
	if headerNumber.Cmp(big.NewInt(1075090)) > 0 {
		reward = big.NewInt(5e+18)
		// Year 3
	}
	if headerNumber.Cmp(big.NewInt(1433454)) > 0 {
		reward = big.NewInt(4e+18)
		// Year 4
	}
	// If Orion use new MP
	if headerNumber.Cmp(big.NewInt(1791793)) >= 0 {
		reward = big.NewInt(15e+17)
	}

	return reward
}

// Octaspace
func getConstRewardOctaspace(height int64) *big.Int {
	// Rewards
	reward := new(big.Int).Set(octaspaceStartReward)
	headerNumber := big.NewInt(height)

	if headerNumber.Cmp(big.NewInt(400_000)) > 0 {
		reward = big.NewInt(500e+16)
		// ArcturusBlock 5.00
	}
	if headerNumber.Cmp(big.NewInt(1_000_000)) > 0 {
		reward = big.NewInt(400e+16)
		// OldenburgBlock 4.00
	}
	if headerNumber.Cmp(big.NewInt(1_500_000)) > 0 {
		reward = big.NewInt(350e+16)
		// ZagamiBlock 3.50
	}
	if headerNumber.Cmp(big.NewInt(2_000_000)) > 0 {
		reward = big.NewInt(300e+16)
		// SpringwaterBlock 3.00
	}
	// PolarisBlock
	if headerNumber.Cmp(big.NewInt(2_500_000)) >= 0 {
		reward = big.NewInt(280e+16)
		// PolarisBlock 2.80
	}

	if headerNumber.Cmp(big.NewInt(3_000_000)) >= 0 {
		reward = big.NewInt(230e+16)
		// MahasimBlock 2.30
	}

	return reward
}

// Octaspace Uncle rw
func getUncleRewardOctaspace(uHeight *big.Int, height *big.Int, reward *big.Int) *big.Int {
	r := new(big.Int)
	r.Add(uHeight, big8)
	r.Sub(r, height)
	r.Mul(r, reward)
	r.Div(r, big8)

	return r
}

func calcBigNumber(reward float64) *big.Int {
	bigReward := new(big.Float).Mul(big.NewFloat(reward), big.NewFloat(1e+18))
	bigRewardInt := new(big.Int)
	bigReward.Int(bigRewardInt)
	return bigRewardInt
}

// etica
func getConstRewardetica(height int64) *big.Int {
	// Rewards)
	// etica
	return calcBigNumber(2.0)
}

// ubqhash expanse
func getUncleRewardUbiq(uHeight *big.Int, height *big.Int, reward *big.Int) *big.Int {

	r := new(big.Int)

	r.Add(uHeight, big2)
	r.Sub(r, height)
	r.Mul(r, reward)
	r.Div(r, big2)
	if r.Cmp(big.NewInt(0)) < 0 {
		// blocks older than the previous block are not rewarded
		r = big.NewInt(0)
	}

	return r
}

// ethash
func getConstRewardEthereum(height int64, cfg *UnlockerConfig) *big.Int {
	// Select the correct block reward based on chain progression
	blockReward := frontierBlockReward
	headerNumber := big.NewInt(height)
	if cfg.ByzantiumFBlock.Cmp(headerNumber) <= 0 {
		blockReward = byzantiumBlockReward
	}
	if cfg.ConstantinopleFBlock.Cmp(headerNumber) <= 0 {
		blockReward = constantinopleBlockReward
	}
	// Accumulate the rewards for the miner and any included uncles
	reward := new(big.Int).Set(blockReward)
	return reward
}

// ethash etica
func getUncleRewardEthereum(uHeight *big.Int, height *big.Int, reward *big.Int) *big.Int {
	r := new(big.Int)
	r.Add(uHeight, big8)
	r.Sub(r, height)
	r.Mul(r, reward)
	r.Div(r, big8)
	if r.Cmp(big.NewInt(0)) < 0 {
		r = big.NewInt(0)
	}

	return r
}

// ethash, etchash, ubqhash

func (u *BlockUnlocker) getExtraRewardForTx(block *rpc.GetBlockReply) (*big.Int, error) {
	amount := new(big.Int)

	blockHeight, err := strconv.ParseInt(strings.Replace(block.Number, "0x", "", -1), 16, 64)
	if err != nil {
		return nil, err
	}
	baseFeePerGas := util.String2Big(block.BaseFeePerGas)

	config := UnlockerConfig{
		IsLondonHardForkEnabled: blockHeight >= londonHardForkHeight,
	}

	for _, tx := range block.Transactions {
		receipt, err := u.rpc.GetTxReceipt(tx.Hash)
		if err != nil {
			log.Println("Error getting transaction receipt:", err)
			continue
		}
		if receipt != nil {
			gasUsed := util.String2Big(receipt.GasUsed)
			gasPrice := util.String2Big(tx.GasPrice)
			if config.IsLondonHardForkEnabled {
				gasPrice = new(big.Int).Sub(gasPrice, baseFeePerGas)
				if gasPrice.Cmp(big.NewInt(0)) < 0 {
					return nil, errors.New("gasPrice less than baseFeePerGas")
				}
			}
			fee := new(big.Int).Mul(gasUsed, gasPrice)
			amount.Add(amount, fee)
		}
	}
	return amount, nil
}

func getUncleRewardEthereumpow(uHeight *big.Int, height *big.Int, reward *big.Int) *big.Int {
	r := new(big.Int)
	r.Add(uHeight, big8)
	r.Sub(r, height)
	r.Mul(r, reward)
	r.Div(r, big8)

	return r
}

//  Universal

func getConstRewardUniversal(height int64) *big.Int {
	if height >= UniversalHardForkHeight {
		return new(big.Int).Set(UniversalBlockReward)
	}
	return new(big.Int).Set(UniversalBlockReward)
}

func getUncleRewardUniversal(uHeight *big.Int, height *big.Int, reward *big.Int) *big.Int {

	return new(big.Int).Set(UniversalUncleReward)

}

// expanse Uncle rw
func getUncleRewardExpanse(uHeight *big.Int, height *big.Int, reward *big.Int) *big.Int {
	r := new(big.Int)
	r.Add(uHeight, big8)
	r.Sub(r, height)
	r.Mul(r, reward)
	r.Div(r, big8)

	return r
}
