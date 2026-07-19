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

type noProcessOHPCFController struct {
	failingOHPCFController
}

func (noProcessOHPCFController) OptionalPowerConsumptionAvailable(spineapi.EntityRemoteInterface) (bool, error) {
	return false, nil
}
func (noProcessOHPCFController) ConsumptionState(spineapi.EntityRemoteInterface) (ucapi.CompressorPowerConsumptionStateType, error) {
	return ucapi.CompressorPowerConsumptionStateStopped, nil
}

type controlOHPCFController struct {
	failingOHPCFController
	available    bool
	availableErr error
	state        ucapi.CompressorPowerConsumptionStateType
	stateErr     error
	stoppable    bool
	stoppableErr error
	pausable     bool
	pausableErr  error
	calls        []string
}

func (c *controlOHPCFController) OptionalPowerConsumptionAvailable(spineapi.EntityRemoteInterface) (bool, error) {
	return c.available, c.availableErr
}
func (c *controlOHPCFController) ConsumptionState(spineapi.EntityRemoteInterface) (ucapi.CompressorPowerConsumptionStateType, error) {
	return c.state, c.stateErr
}
func (c *controlOHPCFController) ConsumptionIsStoppable(spineapi.EntityRemoteInterface) (bool, error) {
	return c.stoppable, c.stoppableErr
}
func (c *controlOHPCFController) ConsumptionIsPausable(spineapi.EntityRemoteInterface) (bool, error) {
	return c.pausable, c.pausableErr
}
func (c *controlOHPCFController) Schedule(spineapi.EntityRemoteInterface, time.Time) error {
	c.calls = append(c.calls, "schedule")
	return nil
}
func (c *controlOHPCFController) Pause(spineapi.EntityRemoteInterface) error {
	c.calls = append(c.calls, "pause")
	return nil
}
func (c *controlOHPCFController) Resume(spineapi.EntityRemoteInterface) error {
	c.calls = append(c.calls, "resume")
	return nil
}
func (c *controlOHPCFController) Abort(spineapi.EntityRemoteInterface) error {
	c.calls = append(c.calls, "abort")
	return nil
}

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

func TestOHPCFNoProcessRemainsAvailableInReadsEventsAndSnapshots(t *testing.T) {
	ctx := context.Background()
	entity := mocks.NewEntityRemoteInterface(t)
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	controller := noProcessOHPCFController{failingOHPCFController{
		entity: entity,
		err:    eebusapi.ErrDataNotAvailable,
	}}
	service := NewOHPCFService(nil, eebus.NewEventBus(), registry, WithOHPCFController(controller))

	result, err := service.GetCompressorFlexibility(ctx, &pb.DeviceRequest{Ski: testValidSKI})
	if err != nil {
		t.Fatal(err)
	}
	if result.GetAvailable() || result.GetState() != pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_STOPPED ||
		result.GetIsStoppable() || result.GetIsPausable() || result.RequestedPowerEstimateW != nil ||
		result.RequestedPowerMaxW != nil || result.StartTime != nil {
		t.Fatalf("no-process flexibility = %+v", result)
	}

	event := &pb.OHPCFEvent{}
	if !service.AttachOHPCFPayload(event, testValidSKI, eebus.EventTypeOHPCFConsumptionStateUpdated) {
		t.Fatalf("no-process state event was marked unavailable: %+v", event)
	}
	permissionEvent := &pb.OHPCFEvent{}
	if !service.AttachOHPCFPayload(permissionEvent, testValidSKI, eebus.EventTypeOHPCFConsumptionStoppableUpdated) {
		t.Fatalf("absent optional permission was marked unavailable: %+v", permissionEvent)
	}

	snapshot, err := NewDeviceSnapshotAssembler(registry, DeviceStatePayloadSources{OHPCF: service}).Build(testValidSKI, 1)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.GetCompressorFlexibilityState() != pb.SnapshotValueState_SNAPSHOT_VALUE_STATE_AVAILABLE ||
		snapshot.GetCompressorFlexibility().GetAvailable() ||
		snapshot.GetCompressorFlexibility().GetState() != pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_STOPPED {
		t.Fatalf("no-process snapshot = %+v", snapshot)
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
	allowed := []struct {
		name       string
		controller *controlOHPCFController
		request    *pb.ControlCompressorRequest
		wantCall   string
	}{
		{
			name: "schedule available process", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, available: true,
			},
			request: &pb.ControlCompressorRequest{Ski: testValidSKI, Action: pb.OHPCFAction_OHPCF_ACTION_SCHEDULE, StartTime: start}, wantCall: "schedule",
		},
		{
			name: "pause running pausable process", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, state: ucapi.CompressorPowerConsumptionStateRunning, pausable: true,
			},
			request: &pb.ControlCompressorRequest{Ski: testValidSKI, Action: pb.OHPCFAction_OHPCF_ACTION_PAUSE}, wantCall: "pause",
		},
		{
			name: "resume paused process", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, state: ucapi.CompressorPowerConsumptionStatePaused,
			},
			request: &pb.ControlCompressorRequest{Ski: testValidSKI, Action: pb.OHPCFAction_OHPCF_ACTION_RESUME}, wantCall: "resume",
		},
		{
			name: "abort running stoppable process", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, state: ucapi.CompressorPowerConsumptionStateRunning, stoppable: true,
			},
			request: &pb.ControlCompressorRequest{Ski: testValidSKI, Action: pb.OHPCFAction_OHPCF_ACTION_ABORT}, wantCall: "abort",
		},
	}
	for _, test := range allowed {
		t.Run(test.name, func(t *testing.T) {
			service := NewOHPCFService(nil, eebus.NewEventBus(), nil, WithOHPCFController(test.controller))
			if _, err := service.ControlCompressorFlexibility(ctx, test.request); err != nil {
				t.Fatal(err)
			}
			if len(test.controller.calls) != 1 || test.controller.calls[0] != test.wantCall {
				t.Fatalf("write calls = %v, want [%s]", test.controller.calls, test.wantCall)
			}
		})
	}

	rejected := []struct {
		name       string
		controller *controlOHPCFController
		action     pb.OHPCFAction
	}{
		{
			name: "schedule without offer", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, available: false,
			}, action: pb.OHPCFAction_OHPCF_ACTION_SCHEDULE,
		},
		{
			name: "pause non-running process", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, state: ucapi.CompressorPowerConsumptionStateScheduled, pausable: true,
			}, action: pb.OHPCFAction_OHPCF_ACTION_PAUSE,
		},
		{
			name: "pause process without permission", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, state: ucapi.CompressorPowerConsumptionStateRunning,
			}, action: pb.OHPCFAction_OHPCF_ACTION_PAUSE,
		},
		{
			name: "resume non-paused process", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, state: ucapi.CompressorPowerConsumptionStateRunning,
			}, action: pb.OHPCFAction_OHPCF_ACTION_RESUME,
		},
		{
			name: "abort process without permission", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, state: ucapi.CompressorPowerConsumptionStateRunning,
			}, action: pb.OHPCFAction_OHPCF_ACTION_ABORT,
		},
		{
			name: "abort stopped process", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, state: ucapi.CompressorPowerConsumptionStateStopped, stoppable: true,
			}, action: pb.OHPCFAction_OHPCF_ACTION_ABORT,
		},
	}
	for _, test := range rejected {
		t.Run(test.name, func(t *testing.T) {
			service := NewOHPCFService(nil, eebus.NewEventBus(), nil, WithOHPCFController(test.controller))
			_, err := service.ControlCompressorFlexibility(ctx, &pb.ControlCompressorRequest{Ski: testValidSKI, Action: test.action})
			if status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("status = %s, error=%v", status.Code(err), err)
			}
			if len(test.controller.calls) != 0 {
				t.Fatalf("rejected action reached device: %v", test.controller.calls)
			}
		})
	}

	readFailures := []struct {
		name       string
		controller *controlOHPCFController
		action     pb.OHPCFAction
		wantCode   codes.Code
	}{
		{
			name: "schedule availability read unavailable", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, availableErr: eebusapi.ErrDataNotAvailable,
			}, action: pb.OHPCFAction_OHPCF_ACTION_SCHEDULE, wantCode: codes.Unavailable,
		},
		{
			name: "pause state read unavailable", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, stateErr: eebusapi.ErrDataNotAvailable,
			}, action: pb.OHPCFAction_OHPCF_ACTION_PAUSE, wantCode: codes.Unavailable,
		},
		{
			name: "pause permission read fails", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, state: ucapi.CompressorPowerConsumptionStateRunning, pausableErr: errors.New("permission read failed"),
			}, action: pb.OHPCFAction_OHPCF_ACTION_PAUSE, wantCode: codes.Internal,
		},
		{
			name: "resume state read unavailable", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, stateErr: eebusapi.ErrDataNotAvailable,
			}, action: pb.OHPCFAction_OHPCF_ACTION_RESUME, wantCode: codes.Unavailable,
		},
		{
			name: "abort permission read fails", controller: &controlOHPCFController{
				failingOHPCFController: failingOHPCFController{entity: entity}, state: ucapi.CompressorPowerConsumptionStateRunning, stoppableErr: errors.New("permission read failed"),
			}, action: pb.OHPCFAction_OHPCF_ACTION_ABORT, wantCode: codes.Internal,
		},
	}
	for _, test := range readFailures {
		t.Run(test.name, func(t *testing.T) {
			service := NewOHPCFService(nil, eebus.NewEventBus(), nil, WithOHPCFController(test.controller))
			_, err := service.ControlCompressorFlexibility(ctx, &pb.ControlCompressorRequest{Ski: testValidSKI, Action: test.action})
			if status.Code(err) != test.wantCode {
				t.Fatalf("status = %s, want %s, error=%v", status.Code(err), test.wantCode, err)
			}
			if len(test.controller.calls) != 0 {
				t.Fatalf("failed precondition read reached device: %v", test.controller.calls)
			}
		})
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
