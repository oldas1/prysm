package blockchain

import (
	"bytes"
	"context"
	"encoding/hex"
	"io/ioutil"
	"reflect"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gogo/protobuf/proto"
	ethpb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
	ssz "github.com/prysmaticlabs/go-ssz"
	"github.com/prysmaticlabs/prysm/beacon-chain/cache/depositcache"
	b "github.com/prysmaticlabs/prysm/beacon-chain/core/blocks"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/feed"
	statefeed "github.com/prysmaticlabs/prysm/beacon-chain/core/feed/state"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/helpers"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/state"
	"github.com/prysmaticlabs/prysm/beacon-chain/db"
	"github.com/prysmaticlabs/prysm/beacon-chain/db/filters"
	testDB "github.com/prysmaticlabs/prysm/beacon-chain/db/testing"
	"github.com/prysmaticlabs/prysm/beacon-chain/forkchoice/protoarray"
	"github.com/prysmaticlabs/prysm/beacon-chain/operations/attestations"
	"github.com/prysmaticlabs/prysm/beacon-chain/p2p"
	"github.com/prysmaticlabs/prysm/beacon-chain/powchain"
	beaconstate "github.com/prysmaticlabs/prysm/beacon-chain/state"
	protodb "github.com/prysmaticlabs/prysm/proto/beacon/db"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	"github.com/prysmaticlabs/prysm/shared/event"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/prysmaticlabs/prysm/shared/testutil"
	"github.com/sirupsen/logrus"
	logTest "github.com/sirupsen/logrus/hooks/test"
)

func init() {
	logrus.SetLevel(logrus.DebugLevel)
	logrus.SetOutput(ioutil.Discard)
}

type store struct {
	headRoot []byte
}

func (s *store) OnBlock(ctx context.Context, b *ethpb.SignedBeaconBlock) (*beaconstate.BeaconState, error) {
	return nil, nil
}

func (s *store) OnBlockCacheFilteredTree(ctx context.Context, b *ethpb.SignedBeaconBlock) (*beaconstate.BeaconState, error) {
	return nil, nil
}

func (s *store) OnBlockInitialSyncStateTransition(ctx context.Context, b *ethpb.SignedBeaconBlock) (*beaconstate.BeaconState, error) {
	return nil, nil
}

func (s *store) OnAttestation(ctx context.Context, a *ethpb.Attestation) ([]uint64, error) {
	return nil, nil
}

func (s *store) GenesisStore(ctx context.Context, justifiedCheckpoint *ethpb.Checkpoint, finalizedCheckpoint *ethpb.Checkpoint) error {
	return nil
}

func (s *store) FinalizedCheckpt() *ethpb.Checkpoint {
	return nil
}

func (s *store) JustifiedCheckpt() *ethpb.Checkpoint {
	return nil
}

func (s *store) Head(ctx context.Context) ([]byte, error) {
	return s.headRoot, nil
}

type mockBeaconNode struct {
	stateFeed *event.Feed
}

// StateFeed mocks the same method in the beacon node.
func (mbn *mockBeaconNode) StateFeed() *event.Feed {
	if mbn.stateFeed == nil {
		mbn.stateFeed = new(event.Feed)
	}
	return mbn.stateFeed
}

type mockBroadcaster struct {
	broadcastCalled bool
}

func (mb *mockBroadcaster) Broadcast(_ context.Context, _ proto.Message) error {
	mb.broadcastCalled = true
	return nil
}

var _ = p2p.Broadcaster(&mockBroadcaster{})

func setupBeaconChain(t *testing.T, beaconDB db.Database) *Service {
	endpoint := "ws://127.0.0.1"
	ctx := context.Background()
	var web3Service *powchain.Service
	var err error
	bState, _ := testutil.DeterministicGenesisState(t, 10)
	err = beaconDB.SavePowchainData(ctx, &protodb.ETH1ChainData{
		BeaconState: bState.InnerStateUnsafe(),
		Trie:        &protodb.SparseMerkleTrie{},
		CurrentEth1Data: &protodb.LatestETH1Data{
			BlockHash: make([]byte, 32),
		},
		ChainstartData: &protodb.ChainStartData{
			Eth1Data: &ethpb.Eth1Data{
				DepositRoot:  make([]byte, 32),
				DepositCount: 0,
				BlockHash:    make([]byte, 32),
			},
		},
		DepositContainers: []*protodb.DepositContainer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	web3Service, err = powchain.NewService(ctx, &powchain.Web3ServiceConfig{
		BeaconDB:        beaconDB,
		ETH1Endpoint:    endpoint,
		DepositContract: common.Address{},
	})
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}

	cfg := &Config{
		BeaconBlockBuf:    0,
		BeaconDB:          beaconDB,
		DepositCache:      depositcache.NewDepositCache(),
		ChainStartFetcher: web3Service,
		P2p:               &mockBroadcaster{},
		StateNotifier:     &mockBeaconNode{},
		AttPool:           attestations.NewPool(),
		ForkChoiceStore:   protoarray.New(0, 0, params.BeaconConfig().ZeroHash),
	}
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}
	chainService, err := NewService(ctx, cfg)
	if err != nil {
		t.Fatalf("unable to setup chain service: %v", err)
	}
	chainService.genesisTime = time.Unix(1, 0) // non-zero time

	return chainService
}

func TestChainStartStop_Uninitialized(t *testing.T) {
	hook := logTest.NewGlobal()
	db := testDB.SetupDB(t)
	defer testDB.TeardownDB(t, db)
	chainService := setupBeaconChain(t, db)

	// Listen for state events.
	stateSubChannel := make(chan *feed.Event, 1)
	stateSub := chainService.stateNotifier.StateFeed().Subscribe(stateSubChannel)

	// Test the chain start state notifier.
	genesisTime := time.Unix(1, 0)
	chainService.Start()
	event := &feed.Event{
		Type: statefeed.ChainStarted,
		Data: &statefeed.ChainStartedData{
			StartTime: genesisTime,
		},
	}
	// Send in a loop to ensure it is delivered (busy wait for the service to subscribe to the state feed).
	for sent := 1; sent == 1; {
		sent = chainService.stateNotifier.StateFeed().Send(event)
		if sent == 1 {
			// Flush our local subscriber.
			<-stateSubChannel
		}
	}

	// Now wait for notification the state is ready.
	for stateInitialized := false; stateInitialized == false; {
		recv := <-stateSubChannel
		if recv.Type == statefeed.Initialized {
			stateInitialized = true
		}
	}
	stateSub.Unsubscribe()

	beaconState, err := db.HeadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if beaconState == nil || beaconState.Slot() != 0 {
		t.Error("Expected canonical state feed to send a state with genesis block")
	}
	if err := chainService.Stop(); err != nil {
		t.Fatalf("Unable to stop chain service: %v", err)
	}
	// The context should have been canceled.
	if chainService.ctx.Err() != context.Canceled {
		t.Error("Context was not canceled")
	}
	testutil.AssertLogsContain(t, hook, "Waiting")
	testutil.AssertLogsContain(t, hook, "Initialized beacon chain genesis state")
}

func TestChainStartStop_Initialized(t *testing.T) {
	hook := logTest.NewGlobal()
	ctx := context.Background()
	db := testDB.SetupDB(t)
	defer testDB.TeardownDB(t, db)

	chainService := setupBeaconChain(t, db)

	genesisBlk := b.NewGenesisBlock([]byte{})
	blkRoot, err := ssz.HashTreeRoot(genesisBlk.Block)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveBlock(ctx, genesisBlk); err != nil {
		t.Fatal(err)
	}
	s, err := beaconstate.InitializeFromProto(&pb.BeaconState{Slot: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveState(ctx, s, blkRoot); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveHeadBlockRoot(ctx, blkRoot); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveGenesisBlockRoot(ctx, blkRoot); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveJustifiedCheckpoint(ctx, &ethpb.Checkpoint{Root: blkRoot[:]}); err != nil {
		t.Fatal(err)
	}

	// Test the start function.
	chainService.Start()

	if err := chainService.Stop(); err != nil {
		t.Fatalf("unable to stop chain service: %v", err)
	}

	// The context should have been canceled.
	if chainService.ctx.Err() != context.Canceled {
		t.Error("context was not canceled")
	}
	testutil.AssertLogsContain(t, hook, "data already exists")
}

func TestChainService_InitializeBeaconChain(t *testing.T) {
	db := testDB.SetupDB(t)
	defer testDB.TeardownDB(t, db)
	ctx := context.Background()

	bc := setupBeaconChain(t, db)
	var err error

	// Set up 10 deposits pre chain start for validators to register
	count := uint64(10)
	deposits, _, _ := testutil.DeterministicDepositsAndKeys(count)
	trie, _, err := testutil.DepositTrieFromDeposits(deposits)
	if err != nil {
		t.Fatal(err)
	}
	hashTreeRoot := trie.HashTreeRoot()
	genState, err := state.EmptyGenesisState()
	if err != nil {
		t.Fatal(err)
	}
	genState.SetEth1Data(&ethpb.Eth1Data{
		DepositRoot:  hashTreeRoot[:],
		DepositCount: uint64(len(deposits)),
	})
	genState, err = b.ProcessDeposits(ctx, genState, &ethpb.BeaconBlockBody{Deposits: deposits})
	if err != nil {
		t.Fatal(err)
	}
	if err := bc.initializeBeaconChain(ctx, time.Unix(0, 0), genState, &ethpb.Eth1Data{
		DepositRoot: hashTreeRoot[:],
	}); err != nil {
		t.Fatal(err)
	}

	s, err := bc.beaconDB.State(ctx, bc.headRoot())
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range s.Validators() {
		if !db.HasValidatorIndex(ctx, v.PublicKey) {
			t.Errorf("Validator %s missing from db", hex.EncodeToString(v.PublicKey))
		}
	}

	if _, err := bc.HeadState(ctx); err != nil {
		t.Error(err)
	}
	if bc.HeadBlock() == nil {
		t.Error("Head state can't be nil after initialize beacon chain")
	}
	if bc.headRoot() == params.BeaconConfig().ZeroHash {
		t.Error("Canonical root for slot 0 can't be zeros after initialize beacon chain")
	}
}

func TestChainService_InitializeChainInfo(t *testing.T) {
	db := testDB.SetupDB(t)
	defer testDB.TeardownDB(t, db)
	ctx := context.Background()

	genesis := b.NewGenesisBlock([]byte{})
	genesisRoot, err := ssz.HashTreeRoot(genesis.Block)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveGenesisBlockRoot(ctx, genesisRoot); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveBlock(ctx, genesis); err != nil {
		t.Fatal(err)
	}

	finalizedSlot := params.BeaconConfig().SlotsPerEpoch*2 + 1
	headBlock := &ethpb.SignedBeaconBlock{Block: &ethpb.BeaconBlock{Slot: finalizedSlot, ParentRoot: genesisRoot[:]}}
	headState, err := beaconstate.InitializeFromProto(&pb.BeaconState{Slot: finalizedSlot})
	if err != nil {
		t.Fatal(err)
	}
	headRoot, _ := ssz.HashTreeRoot(headBlock.Block)
	if err := db.SaveState(ctx, headState, headRoot); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveBlock(ctx, headBlock); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveFinalizedCheckpoint(ctx, &ethpb.Checkpoint{
		Epoch: helpers.SlotToEpoch(finalizedSlot),
		Root:  headRoot[:],
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveBlock(ctx, headBlock); err != nil {
		t.Fatal(err)
	}
	c := &Service{beaconDB: db}
	if err := c.initializeChainInfo(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.HeadBlock(), headBlock) {
		t.Error("head block incorrect")
	}
	s, err := c.HeadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.InnerStateUnsafe(), headState.InnerStateUnsafe()) {
		t.Error("head state incorrect")
	}
	if headBlock.Block.Slot != c.HeadSlot() {
		t.Error("head slot incorrect")
	}
	r, err := c.HeadRoot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(headRoot[:], r) {
		t.Error("head slot incorrect")
	}
	if c.genesisRoot != genesisRoot {
		t.Error("genesis block root incorrect")
	}
}

func TestChainService_SaveHeadNoDB(t *testing.T) {
	db := testDB.SetupDB(t)
	defer testDB.TeardownDB(t, db)
	ctx := context.Background()
	s := &Service{
		beaconDB: db,
	}
	b := &ethpb.SignedBeaconBlock{Block: &ethpb.BeaconBlock{Slot: 1}}
	r, _ := ssz.HashTreeRoot(b)
	state := &pb.BeaconState{}
	newState, err := beaconstate.InitializeFromProto(state)
	s.beaconDB.SaveState(ctx, newState, r)
	if err := s.saveHeadNoDB(ctx, b, r); err != nil {
		t.Fatal(err)
	}

	newB, err := s.beaconDB.HeadBlock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(newB, b) {
		t.Error("head block should not be equal")
	}
}

func TestChainService_PruneOldStates(t *testing.T) {
	db := testDB.SetupDB(t)
	defer testDB.TeardownDB(t, db)
	ctx := context.Background()
	s := &Service{
		beaconDB: db,
	}

	for i := 0; i < 100; i++ {
		block := &ethpb.BeaconBlock{Slot: uint64(i)}
		if err := s.beaconDB.SaveBlock(ctx, &ethpb.SignedBeaconBlock{Block: block}); err != nil {
			t.Fatal(err)
		}
		r, err := ssz.HashTreeRoot(block)
		if err != nil {
			t.Fatal(err)
		}
		state := &pb.BeaconState{Slot: uint64(i)}
		newState, err := beaconstate.InitializeFromProto(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.beaconDB.SaveState(ctx, newState, r); err != nil {
			t.Fatal(err)
		}
	}

	// Delete half of the states.
	if err := s.pruneGarbageState(ctx, 50); err != nil {
		t.Fatal(err)
	}

	filter := filters.NewFilter().SetStartSlot(1).SetEndSlot(100)
	roots, err := s.beaconDB.BlockRoots(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}

	for i := 1; i < 50; i++ {
		s, err := s.beaconDB.State(ctx, roots[i])
		if err != nil {
			t.Fatal(err)
		}
		if s != nil {
			t.Errorf("wanted nil for slot %d", i)
		}
	}
}

func TestHasBlock_ForkChoiceAndDB(t *testing.T) {
	ctx := context.Background()
	db := testDB.SetupDB(t)
	defer testDB.TeardownDB(t, db)
	s := &Service{
		forkChoiceStore:  protoarray.New(0, 0, [32]byte{}),
		finalizedCheckpt: &ethpb.Checkpoint{},
		beaconDB:         db,
	}
	block := &ethpb.SignedBeaconBlock{Block: &ethpb.BeaconBlock{Body: &ethpb.BeaconBlockBody{}}}
	r, _ := ssz.HashTreeRoot(block.Block)
	bs := &pb.BeaconState{FinalizedCheckpoint: &ethpb.Checkpoint{}, CurrentJustifiedCheckpoint: &ethpb.Checkpoint{}}
	state, _ := beaconstate.InitializeFromProto(bs)
	if err := s.insertBlockToForkChoiceStore(ctx, block.Block, r, state); err != nil {
		t.Fatal(err)
	}

	if s.hasBlock(ctx, [32]byte{}) {
		t.Error("Should not have block")
	}

	if !s.hasBlock(ctx, r) {
		t.Error("Should have block")
	}
}

func BenchmarkHasBlockDB(b *testing.B) {
	db := testDB.SetupDB(b)
	defer testDB.TeardownDB(b, db)
	ctx := context.Background()
	s := &Service{
		beaconDB: db,
	}
	block := &ethpb.SignedBeaconBlock{Block: &ethpb.BeaconBlock{}}
	if err := s.beaconDB.SaveBlock(ctx, block); err != nil {
		b.Fatal(err)
	}
	r, _ := ssz.HashTreeRoot(block.Block)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !s.beaconDB.HasBlock(ctx, r) {
			b.Fatal("Block is not in DB")
		}
	}
}

func BenchmarkHasBlockForkChoiceStore(b *testing.B) {
	ctx := context.Background()
	db := testDB.SetupDB(b)
	defer testDB.TeardownDB(b, db)
	s := &Service{
		forkChoiceStore:  protoarray.New(0, 0, [32]byte{}),
		finalizedCheckpt: &ethpb.Checkpoint{},
		beaconDB:         db,
	}
	block := &ethpb.SignedBeaconBlock{Block: &ethpb.BeaconBlock{Body: &ethpb.BeaconBlockBody{}}}
	r, _ := ssz.HashTreeRoot(block.Block)
	bs := &pb.BeaconState{FinalizedCheckpoint: &ethpb.Checkpoint{}, CurrentJustifiedCheckpoint: &ethpb.Checkpoint{}}
	state, _ := beaconstate.InitializeFromProto(bs)
	if err := s.insertBlockToForkChoiceStore(ctx, block.Block, r, state); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !s.forkChoiceStore.HasNode(r) {
			b.Fatal("Block is not in fork choice store")
		}
	}
}
