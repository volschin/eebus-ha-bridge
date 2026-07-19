package grpc

import (
	"context"
	"errors"
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type failingOHPCFController struct {
	entity spineapi.EntityRemoteInterface
	err    error
}

func (f failingOHPCFController) CompatibleEntity(string) eebus.EntityResolution {
	return eebus.EntityResolution{Entity: f.entity, DeviceCount: 1}
}
func (f failingOHPCFController) OptionalPowerConsumptionAvailable(spineapi.EntityRemoteInterface) (bool, error) {
	return false, f.err
}
func (f failingOHPCFController) RequestedPowerEstimate(spineapi.EntityRemoteInterface) (float64, error) {
	return 0, f.err
}
func (f failingOHPCFController) RequestedPowerMax(spineapi.EntityRemoteInterface) (float64, error) {
	return 0, f.err
}
func (f failingOHPCFController) ConsumptionIsStoppable(spineapi.EntityRemoteInterface) (bool, error) {
	return false, f.err
}
func (f failingOHPCFController) ConsumptionIsPausable(spineapi.EntityRemoteInterface) (bool, error) {
	return false, f.err
}
func (f failingOHPCFController) ConsumptionState(spineapi.EntityRemoteInterface) (ucapi.CompressorPowerConsumptionStateType, error) {
	return "", f.err
}
func (f failingOHPCFController) ConsumptionStartTime(spineapi.EntityRemoteInterface) (time.Time, error) {
	return time.Time{}, f.err
}
func (f failingOHPCFController) MinimalRunDuration(spineapi.EntityRemoteInterface) (time.Duration, error) {
	return 0, f.err
}
func (f failingOHPCFController) MinimalPauseDuration(spineapi.EntityRemoteInterface) (time.Duration, error) {
	return 0, f.err
}
func (f failingOHPCFController) Schedule(spineapi.EntityRemoteInterface, time.Time) error {
	return f.err
}
func (f failingOHPCFController) Pause(spineapi.EntityRemoteInterface) error  { return f.err }
func (f failingOHPCFController) Resume(spineapi.EntityRemoteInterface) error { return f.err }
func (f failingOHPCFController) Abort(spineapi.EntityRemoteInterface) error  { return f.err }

func TestOHPCFServiceGetFlexibilityContracts(t *testing.T) {
	ctx := context.Background()
	empty := NewOHPCFService(nil, eebus.NewEventBus(), eebus.NewDeviceRegistry())
	for name, request := range map[string]*pb.DeviceRequest{
		"nil":             nil,
		"malformed SKI":   {Ski: "invalid"},
		"not initialized": {Ski: testValidSKI},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := empty.GetCompressorFlexibility(ctx, request)
			if err == nil {
				t.Fatal("GetCompressorFlexibility returned nil error")
			}
		})
	}

	entity := mocks.NewEntityRemoteInterface(t)
	controller := partialOHPCFController{entity: entity}
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	service := NewOHPCFService(nil, eebus.NewEventBus(), registry, WithOHPCFController(controller))
	result, err := service.GetCompressorFlexibility(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatal(err)
	}
	if !result.GetAvailable() || result.GetRequestedPowerEstimateW() != 1000 || result.GetRequestedPowerMaxW() != 2000 ||
		!result.GetIsPausable() || result.GetState() != pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_RUNNING ||
		result.GetMinimalRunSeconds() != 60 || result.GetMinimalPauseSeconds() != 120 || result.GetStartTime() == nil {
		t.Fatalf("flexibility = %+v", result)
	}

	failing := NewOHPCFService(nil, eebus.NewEventBus(), registry, WithOHPCFController(failingOHPCFController{
		entity: entity, err: eebusapi.ErrDataNotAvailable,
	}))
	if _, err := failing.GetCompressorFlexibility(ctx, &pb.DeviceRequest{Ski: testValidSKI}); status.Code(err) != codes.Unavailable {
		t.Fatalf("all-failed status = %s, error=%v", status.Code(err), err)
	}
}

func TestOHPCFServiceControlContracts(t *testing.T) {
	ctx := context.Background()
	entity := mocks.NewEntityRemoteInterface(t)
	service := NewOHPCFService(nil, eebus.NewEventBus(), nil, WithOHPCFController(partialOHPCFController{entity: entity}))

	invalid := []*pb.ControlCompressorRequest{
		nil,
		{},
		{Ski: testValidSKI},
	}
	for _, request := range invalid {
		if _, err := service.ControlCompressorFlexibility(ctx, request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("request %+v status = %s, error=%v", request, status.Code(err), err)
		}
	}

	start := timestamppb.New(time.Now().Add(time.Minute))
	for _, request := range []*pb.ControlCompressorRequest{
		{Ski: testValidSKI, Action: pb.OHPCFAction_OHPCF_ACTION_SCHEDULE},
		{Ski: testValidSKI, Action: pb.OHPCFAction_OHPCF_ACTION_SCHEDULE, StartTime: start},
		{Ski: testValidSKI, Action: pb.OHPCFAction_OHPCF_ACTION_PAUSE},
		{Ski: testValidSKI, Action: pb.OHPCFAction_OHPCF_ACTION_RESUME},
		{Ski: testValidSKI, Action: pb.OHPCFAction_OHPCF_ACTION_ABORT},
	} {
		if _, err := service.ControlCompressorFlexibility(ctx, request); err != nil {
			t.Fatalf("action %s: %v", request.Action, err)
		}
	}

	notInitialized := NewOHPCFService(nil, eebus.NewEventBus(), nil)
	if _, err := notInitialized.ControlCompressorFlexibility(ctx, &pb.ControlCompressorRequest{
		Ski: testValidSKI, Action: pb.OHPCFAction_OHPCF_ACTION_PAUSE,
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("not initialized status = %s", status.Code(err))
	}
	want := errors.New("control failed")
	failing := NewOHPCFService(nil, eebus.NewEventBus(), nil, WithOHPCFController(failingOHPCFController{entity: entity, err: want}))
	if _, err := failing.ControlCompressorFlexibility(ctx, &pb.ControlCompressorRequest{
		Ski: testValidSKI, Action: pb.OHPCFAction_OHPCF_ACTION_ABORT,
	}); status.Code(err) != codes.Internal {
		t.Fatalf("control failure status = %s, error=%v", status.Code(err), err)
	}
}

func TestOHPCFConversionAndTargetClassification(t *testing.T) {
	states := map[ucapi.CompressorPowerConsumptionStateType]pb.CompressorPowerConsumptionState{
		ucapi.CompressorPowerConsumptionStateAvailable: pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_AVAILABLE,
		ucapi.CompressorPowerConsumptionStateScheduled: pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_SCHEDULED,
		ucapi.CompressorPowerConsumptionStateRunning:   pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_RUNNING,
		ucapi.CompressorPowerConsumptionStatePaused:    pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_PAUSED,
		ucapi.CompressorPowerConsumptionStateCompleted: pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_COMPLETED,
		ucapi.CompressorPowerConsumptionStateStopped:   pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_STOPPED,
		"unknown": pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_UNSPECIFIED,
	}
	for input, want := range states {
		if got := convertCompressorState(input); got != want {
			t.Fatalf("convertCompressorState(%q) = %s, want %s", input, got, want)
		}
	}

	events := []eebus.EventType{
		eebus.EventTypeOHPCFConsumptionStateUpdated,
		eebus.EventTypeOHPCFConsumptionStoppableUpdated,
		eebus.EventTypeOHPCFConsumptionPausableUpdated,
		eebus.EventTypeOHPCFConsumptionStartTimeUpdated,
		eebus.EventTypeOHPCFRequestedPowerEstimateUpdated,
		eebus.EventTypeOHPCFRequestedPowerMaxUpdated,
		eebus.EventTypeOHPCFMinimalRunDurationUpdated,
		eebus.EventTypeOHPCFMinimalPauseDurationUpdated,
	}
	for _, event := range events {
		if got := ohpcfTargetRead(event); got == 0 {
			t.Fatalf("target read for %q is zero", event)
		}
	}
	if got := ohpcfTargetRead("unknown"); got != 0 {
		t.Fatalf("unknown target read = %d", got)
	}
	if !isOHPCFOptionalAbsent(eebusapi.ErrDataInvalid) || !isOHPCFOptionalAbsent(eebusapi.ErrDataNotAvailable) || isOHPCFOptionalAbsent(errors.New("other")) {
		t.Fatal("optional-absence classification is incorrect")
	}
}
