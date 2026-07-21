package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	mamdsf "github.com/enbility/eebus-go/usecases/ma/mdsf"
	ucmocks "github.com/enbility/eebus-go/usecases/mocks"
	spineapi "github.com/enbility/spine-go/api"
	spinemocks "github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestDHWSystemFunctionMonitoringUsesMDSFStateAndOptionalOverrunFallback(t *testing.T) {
	uc := ucmocks.NewMaMDSFInterface(t)
	entity := spinemocks.NewEntityRemoteInterface(t)
	uc.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{
		ucapi.HvacOperationModeTypeAuto,
		ucapi.HvacOperationModeTypeOn,
		ucapi.HvacOperationModeTypeOn,
	}, nil)
	uc.EXPECT().CurrentOperationMode(entity).Return(ucapi.HvacOperationModeTypeAuto, nil)
	uc.EXPECT().OverrunStatus(entity).Return(model.HvacOverrunStatusType(""), eebusapi.ErrDataNotAvailable)
	uc.EXPECT().IsOverrunActive(entity).Return(true, nil)

	wrapper := &DHWSystemFunctionMonitoring{uc: uc}
	state, err := wrapper.State(entity)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state.OperationMode != "auto" || state.BoostStatus != "active" ||
		len(state.AvailableModes) != 2 || state.AvailableModes[1] != "on" {
		t.Fatalf("State() = %+v", state)
	}
}

func TestDHWSystemFunctionMonitoringRoutesMDSFEvents(t *testing.T) {
	bus := eebus.NewEventBus()
	channel := bus.Subscribe()
	defer bus.Unsubscribe(channel)
	wrapper := NewDHWSystemFunctionMonitoring(bus, nil, false)

	for _, test := range []struct {
		input eebusapi.EventType
		want  eebus.EventType
	}{
		{mamdsf.UseCaseSupportUpdate, eebus.EventTypeDHWSystemFunctionSupportUpdated},
		{mamdsf.DataUpdateOperationMode, eebus.EventTypeDHWSystemFunctionUpdated},
		{mamdsf.DataUpdateOverrun, eebus.EventTypeDHWSystemFunctionUpdated},
	} {
		wrapper.HandleEvent("ab:cd", nil, nil, test.input)
		select {
		case event := <-channel:
			if event.SKI != "ABCD" || event.Type != test.want {
				t.Fatalf("event = %+v, want SKI ABCD and type %s", event, test.want)
			}
		case <-time.After(time.Second):
			t.Fatalf("no event for %s", test.input)
		}
	}
}

type fakeDHWSysFnReader struct {
	entity   spineapi.EntityRemoteInterface
	state    DHWSystemFunctionState
	stateErr error
}

func (f *fakeDHWSysFnReader) CompatibleEntity(string) eebus.EntityResolution {
	return eebus.EntityResolution{Entity: f.entity, DeviceCount: 1}
}

func (f *fakeDHWSysFnReader) State(spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error) {
	return f.state, f.stateErr
}

type fakeDHWSysFnWriter struct {
	entity       spineapi.EntityRemoteInterface
	state        DHWSystemFunctionState
	boost        *bool
	mode         string
	stateErr     error
	boostErr     error
	operationErr error
}

func (f *fakeDHWSysFnWriter) CompatibleEntity(string) eebus.EntityResolution {
	if f.entity == nil {
		return eebus.EntityResolution{}
	}
	return eebus.EntityResolution{Entity: f.entity, DeviceCount: 1}
}

func (f *fakeDHWSysFnWriter) State(spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error) {
	return f.state, f.stateErr
}

func (f *fakeDHWSysFnWriter) WriteBoost(_ context.Context, _ spineapi.EntityRemoteInterface, active bool) error {
	f.boost = &active
	return f.boostErr
}

func (f *fakeDHWSysFnWriter) WriteOperationMode(_ context.Context, _ spineapi.EntityRemoteInterface, mode string) error {
	f.mode = mode
	return f.operationErr
}

func TestDHWSystemFunctionAdapterCombinesMDSFReadsWithCDSFWriteability(t *testing.T) {
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Ski().Return("ab:cd").Times(3)
	monitoringEntity := spinemocks.NewEntityRemoteInterface(t)
	monitoringEntity.EXPECT().Device().Return(device).Times(3)
	configurationEntity := spinemocks.NewEntityRemoteInterface(t)

	reader := &fakeDHWSysFnReader{entity: monitoringEntity, state: DHWSystemFunctionState{
		OperationMode: "auto", AvailableModes: []string{"auto", "on"}, BoostStatus: "inactive",
	}}
	writer := &fakeDHWSysFnWriter{entity: configurationEntity, state: DHWSystemFunctionState{
		BoostWritable: true, ModeWritable: true,
	}}
	adapter := NewDHWSystemFunctionAdapter(reader, writer)

	state, err := adapter.State(monitoringEntity)
	if err != nil || !state.BoostWritable || !state.ModeWritable || state.OperationMode != "auto" {
		t.Fatalf("State() = %+v, %v", state, err)
	}
	if err := adapter.WriteBoost(context.Background(), monitoringEntity, true); err != nil || writer.boost == nil || !*writer.boost {
		t.Fatalf("WriteBoost() state = %v, error = %v", writer.boost, err)
	}
	if err := adapter.WriteOperationMode(context.Background(), monitoringEntity, "on"); err != nil || writer.mode != "on" {
		t.Fatalf("WriteOperationMode() mode = %q, error = %v", writer.mode, err)
	}
}

func TestDHWSystemFunctionAdapterFailsWritesClosedWithoutCDSFNegotiation(t *testing.T) {
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Ski().Return("ab:cd")
	entity := spinemocks.NewEntityRemoteInterface(t)
	entity.EXPECT().Device().Return(device)
	adapter := NewDHWSystemFunctionAdapter(&fakeDHWSysFnReader{}, &fakeDHWSysFnWriter{})

	if err := adapter.WriteBoost(context.Background(), entity, true); !errors.Is(err, ErrDHWSysFnNotWritable) {
		t.Fatalf("WriteBoost() error = %v, want ErrDHWSysFnNotWritable", err)
	}
}

func TestDHWSystemFunctionMonitoringReportsStateReadErrors(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	wrapper := &DHWSystemFunctionMonitoring{}
	if _, err := wrapper.State(entity); !errors.Is(err, errDHWSystemFunctionMonitoringNotInitialized) {
		t.Fatalf("State() error = %v, want initialization error", err)
	}

	operationModesErr := errors.New("operation modes unavailable")
	operationModesUC := ucmocks.NewMaMDSFInterface(t)
	operationModesUC.EXPECT().OperationModes(entity).Return(nil, operationModesErr)
	wrapper.uc = operationModesUC
	if _, err := wrapper.State(entity); !errors.Is(err, ErrDHWSysFnDataUnavailable) {
		t.Fatalf("State() operation modes error = %v, want ErrDHWSysFnDataUnavailable", err)
	}

	currentModeErr := errors.New("current mode unavailable")
	currentModeUC := ucmocks.NewMaMDSFInterface(t)
	currentModeUC.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{ucapi.HvacOperationModeTypeAuto}, nil)
	currentModeUC.EXPECT().CurrentOperationMode(entity).Return(ucapi.HvacOperationModeType(""), currentModeErr)
	wrapper.uc = currentModeUC
	if _, err := wrapper.State(entity); !errors.Is(err, ErrDHWSysFnDataUnavailable) {
		t.Fatalf("State() current mode error = %v, want ErrDHWSysFnDataUnavailable", err)
	}
}

func TestDHWSystemFunctionMonitoringUsesReportedOverrunStatus(t *testing.T) {
	uc := ucmocks.NewMaMDSFInterface(t)
	entity := spinemocks.NewEntityRemoteInterface(t)
	uc.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{ucapi.HvacOperationModeTypeAuto}, nil)
	uc.EXPECT().CurrentOperationMode(entity).Return(ucapi.HvacOperationModeTypeAuto, nil)
	uc.EXPECT().OverrunStatus(entity).Return(model.HvacOverrunStatusTypeActive, nil)
	uc.EXPECT().IsOverrunActive(entity).Return(true, nil)

	state, err := (&DHWSystemFunctionMonitoring{uc: uc}).State(entity)
	if err != nil || state.BoostStatus != "active" {
		t.Fatalf("State() = %+v, %v", state, err)
	}
}

func TestDHWSystemFunctionMonitoringUsesInactiveSignalOverStaleActiveStatus(t *testing.T) {
	uc := ucmocks.NewMaMDSFInterface(t)
	entity := spinemocks.NewEntityRemoteInterface(t)
	uc.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{ucapi.HvacOperationModeTypeAuto}, nil)
	uc.EXPECT().CurrentOperationMode(entity).Return(ucapi.HvacOperationModeTypeAuto, nil)
	uc.EXPECT().OverrunStatus(entity).Return(model.HvacOverrunStatusTypeActive, nil)
	uc.EXPECT().IsOverrunActive(entity).Return(false, nil)

	state, err := (&DHWSystemFunctionMonitoring{uc: uc}).State(entity)
	if err != nil || state.BoostStatus != "inactive" {
		t.Fatalf("State() = %+v, %v", state, err)
	}
}

func TestResolvedDHWBoostStatusReconcilesDetailedAndActiveSignals(t *testing.T) {
	unavailable := errors.New("unavailable")
	tests := []struct {
		name      string
		status    model.HvacOverrunStatusType
		statusErr error
		active    bool
		activeErr error
		want      string
	}{
		{"matching running", model.HvacOverrunStatusTypeRunning, nil, true, nil, "running"},
		{"stale finished while active", model.HvacOverrunStatusTypeFinished, nil, true, nil, "active"},
		{"matching finished", model.HvacOverrunStatusTypeFinished, nil, false, nil, "finished"},
		{"stale running while inactive", model.HvacOverrunStatusTypeRunning, nil, false, nil, "inactive"},
		{"detailed status fallback", model.HvacOverrunStatusTypeRunning, nil, false, unavailable, "running"},
		{"no status available", "", unavailable, false, unavailable, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resolvedDHWBoostStatus(test.status, test.statusErr, test.active, test.activeErr)
			if got != test.want {
				t.Fatalf("resolvedDHWBoostStatus() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDHWSystemFunctionMonitoringHandlesOptionalPaths(t *testing.T) {
	wrapper := NewDHWSystemFunctionMonitoring(nil, eebus.NewDeviceRegistry(), true)
	wrapper.Setup(nil)
	if wrapper.UseCase() != nil {
		t.Fatal("UseCase() initialized for nil local entity")
	}
	if resolution := wrapper.CompatibleEntity("ab:cd"); resolution.Entity != nil || resolution.DeviceCount != 0 {
		t.Fatalf("CompatibleEntity() = %+v for uninitialized use case", resolution)
	}

	wrapper.HandleEvent("ab:cd", nil, nil, mamdsf.DataUpdateOperationMode)
	wrapper.HandleEvent("ab:cd", nil, nil, eebusapi.EventType("unknown"))
}

func TestDHWSystemFunctionAdapterHandlesUnavailableComponents(t *testing.T) {
	var nilAdapter *DHWSystemFunctionAdapter
	if resolution := nilAdapter.CompatibleEntity("ab:cd"); resolution.Entity != nil {
		t.Fatalf("CompatibleEntity() = %+v for nil adapter", resolution)
	}
	if _, err := nilAdapter.State(nil); !errors.Is(err, ErrDHWSysFnDataUnavailable) {
		t.Fatalf("State() error = %v, want ErrDHWSysFnDataUnavailable", err)
	}
	if err := nilAdapter.WriteOperationMode(context.Background(), nil, "auto"); !errors.Is(err, ErrDHWSysFnNotWritable) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrDHWSysFnNotWritable", err)
	}

	readErr := errors.New("monitoring failed")
	adapter := NewDHWSystemFunctionAdapter(&fakeDHWSysFnReader{stateErr: readErr}, nil)
	if _, err := adapter.State(nil); !errors.Is(err, readErr) {
		t.Fatalf("State() error = %v, want monitoring error", err)
	}

	reader := &fakeDHWSysFnReader{state: DHWSystemFunctionState{OperationMode: "auto"}}
	adapter = NewDHWSystemFunctionAdapter(reader, nil)
	state, err := adapter.State(nil)
	if err != nil || state.OperationMode != "auto" {
		t.Fatalf("State() = %+v, %v without configuration", state, err)
	}
	if resolution := adapter.CompatibleEntity("ab:cd"); resolution.DeviceCount != 1 {
		t.Fatalf("CompatibleEntity() = %+v", resolution)
	}

	nilDeviceEntity := spinemocks.NewEntityRemoteInterface(t)
	nilDeviceEntity.EXPECT().Device().Return(nil)
	adapter = NewDHWSystemFunctionAdapter(reader, &fakeDHWSysFnWriter{})
	if err := adapter.WriteOperationMode(context.Background(), nilDeviceEntity, "auto"); !errors.Is(err, ErrDHWSysFnNotWritable) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrDHWSysFnNotWritable", err)
	}
}

func TestDHWSystemFunctionAdapterIgnoresConfigurationStateErrors(t *testing.T) {
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Ski().Return("ab:cd")
	monitoringEntity := spinemocks.NewEntityRemoteInterface(t)
	monitoringEntity.EXPECT().Device().Return(device)
	configurationEntity := spinemocks.NewEntityRemoteInterface(t)

	reader := &fakeDHWSysFnReader{state: DHWSystemFunctionState{OperationMode: "auto"}}
	writer := &fakeDHWSysFnWriter{
		entity:   configurationEntity,
		state:    DHWSystemFunctionState{BoostWritable: true, ModeWritable: true},
		stateErr: errors.New("configuration state unavailable"),
	}
	state, err := NewDHWSystemFunctionAdapter(reader, writer).State(monitoringEntity)
	if err != nil || state.BoostWritable || state.ModeWritable {
		t.Fatalf("State() = %+v, %v", state, err)
	}
}
