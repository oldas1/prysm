package detection

import (
	"context"
	"testing"

	ethpb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
	"github.com/prysmaticlabs/prysm/shared/event"
	"github.com/prysmaticlabs/prysm/shared/testutil"
	logTest "github.com/sirupsen/logrus/hooks/test"
)

type mockNotifier struct{}

func (m *mockNotifier) BlockFeed() *event.Feed {
	return new(event.Feed)
}

func (m *mockNotifier) AttestationFeed() *event.Feed {
	return new(event.Feed)
}

func TestService_DetectIncomingBlocks(t *testing.T) {
	hook := logTest.NewGlobal()
	ds := Service{
		notifier: &mockNotifier{},
	}
	blk := &ethpb.SignedBeaconBlock{
		Block:     &ethpb.BeaconBlock{Slot: 1},
		Signature: make([]byte, 96),
	}
	exitRoutine := make(chan bool)
	blocksChan := make(chan *ethpb.SignedBeaconBlock)
	ctx, cancel := context.WithCancel(context.Background())
	go func(tt *testing.T) {
		ds.detectIncomingBlocks(ctx, blocksChan)
		<-exitRoutine
	}(t)
	blocksChan <- blk
	cancel()
	exitRoutine <- true
	testutil.AssertLogsContain(t, hook, "Running detection on block")
	testutil.AssertLogsContain(t, hook, "Context canceled")
}

func TestService_DetectIncomingAttestations(t *testing.T) {
	hook := logTest.NewGlobal()
	ds := Service{
		notifier: &mockNotifier{},
	}
	att := &ethpb.Attestation{
		Data: &ethpb.AttestationData{
			Slot: 1,
		},
	}
	exitRoutine := make(chan bool)
	attsChan := make(chan *ethpb.Attestation)
	ctx, cancel := context.WithCancel(context.Background())
	go func(tt *testing.T) {
		ds.detectIncomingAttestations(ctx, attsChan)
		<-exitRoutine
	}(t)
	attsChan <- att
	cancel()
	exitRoutine <- true
	testutil.AssertLogsContain(t, hook, "Running detection on attestation")
	testutil.AssertLogsContain(t, hook, "Context canceled")
}
