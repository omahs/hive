package main

import (
	"encoding/json"
	"io/ioutil"
	"math/big"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/core/rawdb"
	"gopkg.in/inconshreveable/log15.v2"

	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// writeGenesis writes the given genesis specification to <path>/genesis.json.
func writeGenesis(gspec *core.Genesis, path string) error {
	bytes, err := json.MarshalIndent(gspec, "", "  ")
	if err != nil {
		return err
	}
	filename := filepath.Join(path, "genesis.json")
	return ioutil.WriteFile(filename, bytes, 0644)
}

// produceSimpleTestChain creates a chain containing some value transfer transactions.
//
// The first of multiple chains exhibiting different characteristics for differing test
// purposes. This chain involves two accounts that transfer funds to each other in
// alternating blocks. These functions save the chain.rlp and genesis.json to the
// specified path. blockCount indicates the desired chain length in blocks.
func produceSimpleTestChain(path string, blockCount uint) error {
	var (
		key1, _ = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
		key2, _ = crypto.HexToECDSA("8a1f9a8f95be41cd7ccb6168179afb4504aefe388d1e14474d32c45c72ce7b7a")
		addr1   = crypto.PubkeyToAddress(key1.PublicKey)
		addr2   = crypto.PubkeyToAddress(key2.PublicKey)
	)

	signer := types.HomesteadSigner{}
	blockModifier := func(i int, gen *core.BlockGen) {
		gen.OffsetTime(int64((i + 1) * 30))
		if i%2 == 0 {
			tx, _ := types.SignTx(types.NewTransaction(gen.TxNonce(addr1), addr2, big.NewInt(10000), params.TxGas, nil, nil), signer, key1)
			gen.AddTx(tx)
		} else {
			tx, _ := types.SignTx(types.NewTransaction(gen.TxNonce(addr2), addr1, big.NewInt(10000), params.TxGas, nil, nil), signer, key2)
			gen.AddTx(tx)
		}
	}

	gspec := &core.Genesis{
		Config: &params.ChainConfig{
			HomesteadBlock: new(big.Int),
			ChainID:        big.NewInt(1),
			DAOForkBlock:   big.NewInt(0),
			DAOForkSupport: false,
			EIP150Block:    big.NewInt(0),
			//Do not set EIP150Hash because Parity cannot peer with it
			//EIP150Hash:          ethcommon.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000"),
			EIP155Block:         big.NewInt(0),
			EIP158Block:         big.NewInt(0),
			ByzantiumBlock:      big.NewInt(0),
			ConstantinopleBlock: nil,
			EWASMBlock:          nil,
			Ethash:              new(params.EthashConfig),
		},
		Nonce:      0xdeadbeefdeadbeef,
		Timestamp:  0x0,
		GasLimit:   0x8000000,
		Difficulty: big.NewInt(0x10),
		Mixhash:    ethcommon.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000"),
		Coinbase:   ethcommon.HexToAddress("0x0000000000000000000000000000000000000000"),
		Alloc:      core.GenesisAlloc{addr1: {Balance: big.NewInt(1000000)}},
	}

	if err := writeGenesis(gspec, path); err != nil {
		return err
	}
	return generateChainAndSave(gspec, blockCount, path, blockModifier)
}

// produceTestChainFromGenesisFile creates a test chain with no transactions or other
// modifications based on an externally specified genesis file. The blockTimeInSeconds is
// used to manipulate the block difficulty.
func produceTestChainFromGenesisFile(sourceGenesis string, outputPath string, blockCount uint, blockTimeInSeconds uint) error {
	sourceBytes, err := ioutil.ReadFile(sourceGenesis)
	if err != nil {
		log15.Crit("failed to read genesis json", "error", err)
		return err
	}

	var gspec core.Genesis
	if err = json.Unmarshal(sourceBytes, &gspec); err != nil {
		log15.Crit("failed to deserialize genesis json", "error", err)
		return err
	}
	blockModifier := func(i int, gen *core.BlockGen) {
		gen.OffsetTime(int64((i+1)*int(blockTimeInSeconds) - 10))
	}
	return generateChainAndSave(&gspec, blockCount, outputPath, blockModifier)
}

// writeChain exports the given chain to a file.
func writeChain(chain *core.BlockChain, filename string, start uint64) error {
	out, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.ModePerm)
	if err != nil {
		return err
	}
	defer out.Close()
	return chain.ExportN(out, start, chain.CurrentBlock().NumberU64())
}

// GenerateChainAndSave produces a chain based on the genesis-config with
// valid proof of work, and block difficulty modified by the OffsetTime function
// in the per-block post-processing function.
func generateChainAndSave(gspec *core.Genesis, blockCount uint, path string, blockModifier func(i int, gen *core.BlockGen)) error {
	db := rawdb.NewMemoryDatabase()
	genesis := gspec.MustCommit(db)

	eng := newSealingEthash(ethash.Config{PowMode: ethash.ModeNormal}, nil, false)

	// Generate a chain where each block is created, modified, and immediately sealed.
	chain, _ := core.GenerateChain(gspec.Config, genesis, eng, db, int(blockCount), blockModifier)

	// Import the chain. This runs all block validation rules.
	blockchain, err := core.NewBlockChain(db, nil, gspec.Config, eng, vm.Config{}, nil)
	if err != nil {
		log15.Crit("new chain error", "error", err)
		return err
	}
	defer blockchain.Stop()
	if _, err := blockchain.InsertChain(chain); err != nil {
		log15.Crit("insert chain error", "error", err)
		return err
	}

	// Write out the generated blockchain
	if err := writeChain(blockchain, filepath.Join(path, "chain.rlp"), 0); err != nil {
		log15.Crit("error writing chain to file", "error", err)
		return err
	}
	if err := writeChain(blockchain, filepath.Join(path, "chain_nogenesis.rlp"), 1); err != nil {
		log15.Crit("error writing chain to file", "error", err)
		return err
	}
	return nil
}