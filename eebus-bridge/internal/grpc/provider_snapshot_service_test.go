package grpc

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ptrFloat64(value float64) *float64 {
	return &value
}

type recordingGridSnapshotProvider struct {
	current usecases.GridSnapshot
	calls   int
	fail    error
}

func (p *recordingGridSnapshotProvider) PublishGridSnapshot(snapshot usecases.GridSnapshot) error {
	p.calls++
	if p.fail != nil {
		return p.fail
	}
	p.current = snapshot
	return nil
}

func sampleMeta(observed time.Time, invalid bool) *pb.ProviderSampleMeta {
	return &pb.ProviderSampleMeta{
		ObservedAt: timestamppb.New(observed),
		ValidUntil: timestamppb.New(observed.Add(2 * time.Minute)),
		Invalid:    invalid,
	}
}

type recordingPVSnapshotProvider struct {
	snapshotCalls int
	peakCalls     int
	peakPower     float64
	failPeak      error
}

func (p *recordingPVSnapshotProvider) PublishPVSnapshot(usecases.PVSnapshot) error {
	p.snapshotCalls++
	return nil
}

func (p *recordingPVSnapshotProvider) PublishPeakPower(watts float64) error {
	p.peakCalls++
	if p.failPeak != nil {
		return p.failPeak
	}
	p.peakPower = watts
	return nil
}

type recordingBatterySnapshotProvider struct {
	snapshotCalls int
}

func (p *recordingBatterySnapshotProvider) PublishBatterySnapshot(usecases.BatterySnapshot) error {
	p.snapshotCalls++
	return nil
}

func TestPublishGridDataPassesOneCompleteSnapshot(t *testing.T) {
	provider := &recordingGridSnapshotProvider{}
	service := &GridService{mgcp: provider}
	observedAt := time.Now().Add(-time.Minute)
	feedIn := 10.0
	consumed := 20.0

	_, err := service.PublishGridData(context.Background(), &pb.GridData{
		PowerW:     ptrFloat64(-500),
		FeedInWh:   &feedIn,
		ConsumedWh: &consumed,
		Sample:     sampleMeta(observedAt, false),
	})
	if err != nil {
		t.Fatalf("PublishGridData() error = %v", err)
	}

	if provider.calls != 1 {
		t.Fatalf("PublishGridData() provider calls = %d, want 1", provider.calls)
	}
	if provider.current.PowerW != -500 || provider.current.FeedInWh == nil ||
		*provider.current.FeedInWh != feedIn || provider.current.ConsumedWh == nil ||
		*provider.current.ConsumedWh != consumed {
		t.Fatalf("snapshot = %+v", provider.current)
	}
	if !provider.current.Validity.Current(observedAt.Add(time.Minute)) ||
		provider.current.Validity.Current(observedAt.Add(3*time.Minute)) {
		t.Fatalf("snapshot validity = %+v", provider.current.Validity)
	}
}

func TestPublishGridDataFailureLeavesPreviousSnapshot(t *testing.T) {
	previous := usecases.GridSnapshot{PowerW: 1}
	provider := &recordingGridSnapshotProvider{
		current: previous,
		fail:    errors.New("second field failed"),
	}
	service := &GridService{mgcp: provider}

	_, err := service.PublishGridData(context.Background(), &pb.GridData{
		PowerW: ptrFloat64(2),
		Sample: sampleMeta(time.Now(), false),
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("PublishGridData() code = %v, want Internal", status.Code(err))
	}
	if provider.current.PowerW != previous.PowerW {
		t.Fatalf("current snapshot = %+v, want previous %+v", provider.current, previous)
	}
}

func TestPublishGridDataInvalidatesSnapshot(t *testing.T) {
	provider := &recordingGridSnapshotProvider{
		current: usecases.GridSnapshot{
			PowerW: 1,
			Validity: usecases.ProviderValidity{
				ObservedAt: time.Unix(1, 0),
				ValidUntil: time.Unix(100, 0),
			},
		},
	}
	service := &GridService{mgcp: provider}

	_, err := service.PublishGridData(context.Background(), &pb.GridData{
		Sample: sampleMeta(time.Unix(200, 0), true),
	})
	if err != nil {
		t.Fatalf("PublishGridData() error = %v", err)
	}
	if provider.calls != 1 || !provider.current.Validity.Invalid {
		t.Fatalf("current snapshot = %+v, calls=%d", provider.current, provider.calls)
	}
}

func TestPublishGridDataRejectsMissingSampleTimestampsBeforeProviderCall(t *testing.T) {
	provider := &recordingGridSnapshotProvider{}
	service := &GridService{mgcp: provider}

	_, err := service.PublishGridData(context.Background(), &pb.GridData{
		PowerW: ptrFloat64(1),
		Sample: &pb.ProviderSampleMeta{
			ValidUntil: timestamppb.New(time.Unix(200, 0)),
		},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PublishGridData() code = %v, want InvalidArgument", status.Code(err))
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

func TestPublishGridDataRejectsExpiredSampleMetadataBeforeProviderCall(t *testing.T) {
	provider := &recordingGridSnapshotProvider{}
	service := &GridService{mgcp: provider}
	observedAt := time.Now().Add(-2 * time.Minute)

	_, err := service.PublishGridData(context.Background(), &pb.GridData{
		PowerW: ptrFloat64(1),
		Sample: &pb.ProviderSampleMeta{
			ObservedAt: timestamppb.New(observedAt),
			ValidUntil: timestamppb.New(observedAt.Add(time.Minute)),
		},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PublishGridData() code = %v, want InvalidArgument", status.Code(err))
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

func TestPublishGridDataRejectsNonPositiveSampleWindowBeforeProviderCall(t *testing.T) {
	provider := &recordingGridSnapshotProvider{}
	service := &GridService{mgcp: provider}
	observedAt := time.Now().Add(time.Minute)

	_, err := service.PublishGridData(context.Background(), &pb.GridData{
		PowerW: ptrFloat64(1),
		Sample: &pb.ProviderSampleMeta{
			ObservedAt: timestamppb.New(observedAt),
			ValidUntil: timestamppb.New(observedAt),
		},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PublishGridData() code = %v, want InvalidArgument", status.Code(err))
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

func TestPublishGridDataRejectsFutureObservedAtBeforeProviderCall(t *testing.T) {
	provider := &recordingGridSnapshotProvider{}
	service := &GridService{mgcp: provider}
	observedAt := time.Now().Add(time.Minute)

	_, err := service.PublishGridData(context.Background(), &pb.GridData{
		PowerW: ptrFloat64(1),
		Sample: &pb.ProviderSampleMeta{
			ObservedAt: timestamppb.New(observedAt),
			ValidUntil: timestamppb.New(observedAt.Add(time.Minute)),
		},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PublishGridData() code = %v, want InvalidArgument", status.Code(err))
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

func TestPublishGridDataRejectsMissingPowerBeforeProviderCall(t *testing.T) {
	provider := &recordingGridSnapshotProvider{}
	service := &GridService{mgcp: provider}

	_, err := service.PublishGridData(context.Background(), &pb.GridData{
		Sample: sampleMeta(time.Now(), false),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PublishGridData() code = %v, want InvalidArgument", status.Code(err))
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

func TestPublishPVDataRejectsDeprecatedPeakPowerBeforeProviderCall(t *testing.T) {
	provider := &recordingPVSnapshotProvider{}
	service := &VisualizationService{vapd: provider}
	peak := 5000.0

	_, err := service.PublishPVData(context.Background(), &pb.PVData{
		PowerW:     ptrFloat64(1000),
		PeakPowerW: &peak,
		Sample:     sampleMeta(time.Now(), false),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PublishPVData() code = %v, want InvalidArgument", status.Code(err))
	}
	if provider.snapshotCalls != 0 || provider.peakCalls != 0 {
		t.Fatalf("provider calls snapshot=%d peak=%d, want none", provider.snapshotCalls, provider.peakCalls)
	}
}

func TestPublishPVDataRejectsMissingPowerBeforeProviderCall(t *testing.T) {
	provider := &recordingPVSnapshotProvider{}
	service := &VisualizationService{vapd: provider}

	_, err := service.PublishPVData(context.Background(), &pb.PVData{
		Sample: sampleMeta(time.Now(), false),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PublishPVData() code = %v, want InvalidArgument", status.Code(err))
	}
	if provider.snapshotCalls != 0 || provider.peakCalls != 0 {
		t.Fatalf("provider calls snapshot=%d peak=%d, want none", provider.snapshotCalls, provider.peakCalls)
	}
}

func TestPublishBatteryDataRejectsMissingPowerBeforeProviderCall(t *testing.T) {
	provider := &recordingBatterySnapshotProvider{}
	service := &VisualizationService{vabd: provider}

	_, err := service.PublishBatteryData(context.Background(), &pb.BatteryData{
		Sample: sampleMeta(time.Now(), false),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PublishBatteryData() code = %v, want InvalidArgument", status.Code(err))
	}
	if provider.snapshotCalls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.snapshotCalls)
	}
}

func TestPublishPVPeakPowerPublishesStaticConfigurationOnly(t *testing.T) {
	provider := &recordingPVSnapshotProvider{}
	service := &VisualizationService{vapd: provider}

	_, err := service.PublishPVPeakPower(context.Background(), &pb.PVPeakPowerData{
		PeakPowerW: 5000,
	})
	if err != nil {
		t.Fatalf("PublishPVPeakPower() error = %v", err)
	}
	if provider.peakCalls != 1 || provider.peakPower != 5000 {
		t.Fatalf("peak calls=%d peak=%g, want one 5000W update", provider.peakCalls, provider.peakPower)
	}
	if provider.snapshotCalls != 0 {
		t.Fatalf("snapshot calls = %d, want 0", provider.snapshotCalls)
	}
}
