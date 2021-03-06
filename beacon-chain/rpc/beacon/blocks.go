package beacon

import (
	"context"
	"strconv"

	ptypes "github.com/gogo/protobuf/types"
	ethpb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
	"github.com/prysmaticlabs/go-ssz"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/feed"
	blockfeed "github.com/prysmaticlabs/prysm/beacon-chain/core/feed/block"
	statefeed "github.com/prysmaticlabs/prysm/beacon-chain/core/feed/state"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/helpers"
	"github.com/prysmaticlabs/prysm/beacon-chain/db/filters"
	"github.com/prysmaticlabs/prysm/beacon-chain/flags"
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
	"github.com/prysmaticlabs/prysm/shared/pagination"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListBlocks retrieves blocks by root, slot, or epoch.
//
// The server may return multiple blocks in the case that a slot or epoch is
// provided as the filter criteria. The server may return an empty list when
// no blocks in their database match the filter criteria. This RPC should
// not return NOT_FOUND. Only one filter criteria should be used.
func (bs *Server) ListBlocks(
	ctx context.Context, req *ethpb.ListBlocksRequest,
) (*ethpb.ListBlocksResponse, error) {
	if int(req.PageSize) > flags.Get().MaxPageSize {
		return nil, status.Errorf(codes.InvalidArgument, "Requested page size %d can not be greater than max size %d",
			req.PageSize, flags.Get().MaxPageSize)
	}

	switch q := req.QueryFilter.(type) {
	case *ethpb.ListBlocksRequest_Epoch:
		blks, err := bs.BeaconDB.Blocks(ctx, filters.NewFilter().SetStartEpoch(q.Epoch).SetEndEpoch(q.Epoch))
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to get blocks: %v", err)
		}

		numBlks := len(blks)
		if numBlks == 0 {
			return &ethpb.ListBlocksResponse{
				BlockContainers: make([]*ethpb.BeaconBlockContainer, 0),
				TotalSize:       0,
				NextPageToken:   strconv.Itoa(0),
			}, nil
		}

		start, end, nextPageToken, err := pagination.StartAndEndPage(req.PageToken, int(req.PageSize), numBlks)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Could not paginate blocks: %v", err)
		}

		returnedBlks := blks[start:end]
		containers := make([]*ethpb.BeaconBlockContainer, len(returnedBlks))
		for i, b := range returnedBlks {
			root, err := ssz.HashTreeRoot(b.Block)
			if err != nil {
				return nil, err
			}
			containers[i] = &ethpb.BeaconBlockContainer{
				Block:     b,
				BlockRoot: root[:],
			}
		}

		return &ethpb.ListBlocksResponse{
			BlockContainers: containers,
			TotalSize:       int32(numBlks),
			NextPageToken:   nextPageToken,
		}, nil
	case *ethpb.ListBlocksRequest_Root:
		blk, err := bs.BeaconDB.Block(ctx, bytesutil.ToBytes32(q.Root))
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Could not retrieve block: %v", err)
		}
		if blk == nil {
			return &ethpb.ListBlocksResponse{
				BlockContainers: make([]*ethpb.BeaconBlockContainer, 0),
				TotalSize:       0,
				NextPageToken:   strconv.Itoa(0),
			}, nil
		}
		root, err := ssz.HashTreeRoot(blk.Block)
		if err != nil {
			return nil, err
		}

		return &ethpb.ListBlocksResponse{
			BlockContainers: []*ethpb.BeaconBlockContainer{{
				Block:     blk,
				BlockRoot: root[:]},
			},
			TotalSize: 1,
		}, nil

	case *ethpb.ListBlocksRequest_Slot:
		blks, err := bs.BeaconDB.Blocks(ctx, filters.NewFilter().SetStartSlot(q.Slot).SetEndSlot(q.Slot))
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Could not retrieve blocks for slot %d: %v", q.Slot, err)
		}

		numBlks := len(blks)
		if numBlks == 0 {
			return &ethpb.ListBlocksResponse{
				BlockContainers: make([]*ethpb.BeaconBlockContainer, 0),
				TotalSize:       0,
				NextPageToken:   strconv.Itoa(0),
			}, nil
		}

		start, end, nextPageToken, err := pagination.StartAndEndPage(req.PageToken, int(req.PageSize), numBlks)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Could not paginate blocks: %v", err)
		}

		returnedBlks := blks[start:end]
		containers := make([]*ethpb.BeaconBlockContainer, len(returnedBlks))
		for i, b := range returnedBlks {
			root, err := ssz.HashTreeRoot(b.Block)
			if err != nil {
				return nil, err
			}
			containers[i] = &ethpb.BeaconBlockContainer{
				Block:     b,
				BlockRoot: root[:],
			}
		}

		return &ethpb.ListBlocksResponse{
			BlockContainers: containers,
			TotalSize:       int32(numBlks),
			NextPageToken:   nextPageToken,
		}, nil
	case *ethpb.ListBlocksRequest_Genesis:
		genBlk, err := bs.BeaconDB.GenesisBlock(ctx)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Could not retrieve blocks for genesis slot: %v", err)
		}
		if genBlk == nil {
			return nil, status.Error(codes.Internal, "Could not find genesis block")
		}
		root, err := ssz.HashTreeRoot(genBlk.Block)
		if err != nil {
			return nil, err
		}
		containers := []*ethpb.BeaconBlockContainer{
			{
				Block:     genBlk,
				BlockRoot: root[:],
			},
		}

		return &ethpb.ListBlocksResponse{
			BlockContainers: containers,
			TotalSize:       int32(1),
			NextPageToken:   strconv.Itoa(0),
		}, nil
	}

	return nil, status.Error(codes.InvalidArgument, "Must specify a filter criteria for fetching blocks")
}

// GetChainHead retrieves information about the head of the beacon chain from
// the view of the beacon chain node.
//
// This includes the head block slot and root as well as information about
// the most recent finalized and justified slots.
func (bs *Server) GetChainHead(ctx context.Context, _ *ptypes.Empty) (*ethpb.ChainHead, error) {
	return bs.chainHeadRetrieval(ctx)
}

// StreamBlocks to clients every single time a block is received by the beacon node.
func (bs *Server) StreamBlocks(_ *ptypes.Empty, stream ethpb.BeaconChain_StreamBlocksServer) error {
	blocksChannel := make(chan *feed.Event, 1)
	blockSub := bs.BlockNotifier.BlockFeed().Subscribe(blocksChannel)
	defer blockSub.Unsubscribe()
	for {
		select {
		case event := <-blocksChannel:
			if event.Type == blockfeed.ReceivedBlock {
				data, ok := event.Data.(blockfeed.ReceivedBlockData)
				if !ok {
					return status.Errorf(
						codes.FailedPrecondition,
						"Could not subscribe to block feed, received bad data: %v",
						data,
					)
				}
				if data.SignedBlock == nil {
					// One nil block shouldn't stop the stream.
					continue
				}
				if err := stream.Send(data.SignedBlock); err != nil {
					return status.Errorf(codes.Unavailable, "Could not send over stream: %v", err)
				}
			}
		case <-blockSub.Err():
			return status.Error(codes.Aborted, "Subscriber closed, exiting goroutine")
		case <-bs.Ctx.Done():
			return status.Error(codes.Canceled, "Context canceled")
		case <-stream.Context().Done():
			return status.Error(codes.Canceled, "Context canceled")
		}
	}
}

// StreamChainHead to clients every single time the head block and state of the chain change.
func (bs *Server) StreamChainHead(_ *ptypes.Empty, stream ethpb.BeaconChain_StreamChainHeadServer) error {
	stateChannel := make(chan *feed.Event, 1)
	stateSub := bs.StateNotifier.StateFeed().Subscribe(stateChannel)
	defer stateSub.Unsubscribe()
	for {
		select {
		case event := <-stateChannel:
			if event.Type == statefeed.BlockProcessed {
				res, err := bs.chainHeadRetrieval(bs.Ctx)
				if err != nil {
					return status.Errorf(codes.Internal, "Could not retrieve chain head: %v", err)
				}
				if err := stream.Send(res); err != nil {
					return status.Errorf(codes.Unavailable, "Could not send over stream: %v", err)
				}
			}
		case <-stateSub.Err():
			return status.Error(codes.Aborted, "Subscriber closed, exiting goroutine")
		case <-bs.Ctx.Done():
			return status.Error(codes.Canceled, "Context canceled")
		case <-stream.Context().Done():
			return status.Error(codes.Canceled, "Context canceled")
		}
	}
}

// Retrieve chain head information from the DB and the current beacon state.
func (bs *Server) chainHeadRetrieval(ctx context.Context) (*ethpb.ChainHead, error) {
	headBlock := bs.HeadFetcher.HeadBlock()
	if headBlock == nil {
		return nil, status.Error(codes.Internal, "Head block of chain was nil")
	}
	headBlockRoot, err := ssz.HashTreeRoot(headBlock.Block)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not get head block root: %v", err)
	}

	finalizedCheckpoint := bs.FinalizationFetcher.FinalizedCheckpt()
	b, err := bs.BeaconDB.Block(ctx, bytesutil.ToBytes32(finalizedCheckpoint.Root))
	if err != nil || b == nil || b.Block == nil {
		return nil, status.Error(codes.Internal, "Could not get finalized block")
	}
	finalizedSlot := b.Block.Slot

	justifiedCheckpoint := bs.FinalizationFetcher.CurrentJustifiedCheckpt()
	b, err = bs.BeaconDB.Block(ctx, bytesutil.ToBytes32(justifiedCheckpoint.Root))
	if err != nil || b == nil || b.Block == nil {
		return nil, status.Error(codes.Internal, "Could not get justified block")
	}
	justifiedSlot := b.Block.Slot

	prevJustifiedCheckpoint := bs.FinalizationFetcher.PreviousJustifiedCheckpt()
	b, err = bs.BeaconDB.Block(ctx, bytesutil.ToBytes32(prevJustifiedCheckpoint.Root))
	if err != nil || b == nil || b.Block == nil {
		return nil, status.Error(codes.Internal, "Could not get prev justified block")
	}
	prevJustifiedSlot := b.Block.Slot

	return &ethpb.ChainHead{
		HeadSlot:                   headBlock.Block.Slot,
		HeadEpoch:                  helpers.SlotToEpoch(headBlock.Block.Slot),
		HeadBlockRoot:              headBlockRoot[:],
		FinalizedSlot:              finalizedSlot,
		FinalizedEpoch:             finalizedCheckpoint.Epoch,
		FinalizedBlockRoot:         finalizedCheckpoint.Root,
		JustifiedSlot:              justifiedSlot,
		JustifiedEpoch:             justifiedCheckpoint.Epoch,
		JustifiedBlockRoot:         justifiedCheckpoint.Root,
		PreviousJustifiedSlot:      prevJustifiedSlot,
		PreviousJustifiedEpoch:     prevJustifiedCheckpoint.Epoch,
		PreviousJustifiedBlockRoot: prevJustifiedCheckpoint.Root,
	}, nil
}
