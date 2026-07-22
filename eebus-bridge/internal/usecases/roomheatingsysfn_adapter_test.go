package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	mamrhsf "github.com/enbility/eebus-go/usecases/ma/mrhsf"
	ucmocks "github.com/enbility/eebus-go/usecases/mocks"
	spineapi "github.com/enbility/spine-go/api"
	spinemocks "github.com/enbility/spine-go/mocks"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestRoomHeatingSystemFunctionMonitoringUsesMRHSFState(t *testing.T) {
	uc := ucmocks.NewMaMRHSFInterface(t)
	entity := spinemocks.NewEntityRemoteInterface(t)
	uc.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{
		ucapi.HvacOperationModeTypeAuto,
		ucapi.HvacOperationModeTypeOn,
		ucapi.HvacOperationModeTypeOn,
		ucapi.HvacOperationModeType("vendor-specific"),
	}, nil)
	uc.EXPECT().CurrentOperationMode(entity).Return(ucapi.HvacOperationModeTypeAuto, nil)

	wrapper := &RoomHeatingSystemFunctionMonitoring{uc: uc}
	state, err := wrapper.State(entity)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	wantModes := []string{"auto", "on", "vendor-specific"}
	if state.OperationMode != "auto" || state.ModeWritable || len(state.AvailableModes) != len(wantModes) {
		t.Fatalf("State() = %+v, want modes %v", state, wantModes)
	}
	for index := range wantModes {
		if state.AvailableModes[index] != wantModes[index] {
			t.Fatalf("AvailableModes = %v, want %v", state.AvailableModes, wantModes)
		}
	}
}

func TestRoomHeatingSystemFunctionMonitoringRoutesOnlyMRHSFEvents(t *testing.T) {
	bus := eebus.NewEventBus()
	events := bus.Subscribe()
	defer bus.Unsubscribe(events)
	wrapper := NewRoomHeatingSystemFunctionMonitoring(bus, nil, false)

	for _, test := range []struct {
		input eebusapi.EventType
		want  eebus.EventType
	}{
		{mamrhsf.UseCaseSupportUpdate, eebus.EventTypeRoomHeatingSystemFunctionSupportUpdated},
		{mamrhsf.DataUpdateOperationMode, eebus.EventTypeRoomHeatingSystemFunctionUpdated},
	} {
		wrapper.HandleEvent("ab:cd", nil, nil, test.input)
		select {
		case event := <-events:
			if event.SKI != "ABCD" || event.Type != test.want {
				t.Fatalf("event = %+v, want SKI ABCD and type %s", event, test.want)
			}
		case <-time.After(time.Second):
			t.Fatalf("no event for %s", test.input)
		}
	}

	wrapper.HandleEvent("ab:cd", nil, nil, eebusapi.EventType("legacy-crhsf-update"))
	select {
	case event := <-events:
		t.Fatalf("unexpected event = %+v", event)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestRoomHeatingSystemFunctionMonitoringMapsReadErrors(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	wrapper := &RoomHeatingSystemFunctionMonitoring{}
	if _, err := wrapper.State(entity); !errors.Is(err, errRoomHeatingSystemFunctionMonitoringNotInitialized) {
		t.Fatalf("State() error = %v, want initialization error", err)
	}

	uc := ucmocks.NewMaMRHSFInterface(t)
	uc.EXPECT().OperationModes(entity).Return(nil, eebusapi.ErrDataNotAvailable)
	wrapper.uc = uc
	if _, err := wrapper.State(entity); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("State() error = %v, want ErrRoomHeatingSysFnDataUnavailable", err)
	}
}

type fakeRoomHeatingSystemFunctionReader struct {
	entity   spineapi.EntityRemoteInterface
	state    RoomHeatingSystemFunctionState
	stateErr error
}

func (f *fakeRoomHeatingSystemFunctionReader) CompatibleEntity(string) eebus.EntityResolution {
	if f.entity == nil {
		return eebus.EntityResolution{}
	}
	return eebus.EntityResolution{Entity: f.entity, DeviceCount: 1}
}

func (f *fakeRoomHeatingSystemFunctionReader) State(
	spineapi.EntityRemoteInterface,
) (RoomHeatingSystemFunctionState, error) {
	return f.state, f.stateErr
}

type fakeRoomHeatingSystemFunctionWriter struct {
	entity      spineapi.EntityRemoteInterface
	state       RoomHeatingSystemFunctionState
	stateEntity spineapi.EntityRemoteInterface
	writeEntity spineapi.EntityRemoteInterface
	mode        string
	stateErr    error
	writeErr    error
}

func (f *fakeRoomHeatingSystemFunctionWriter) CompatibleEntity(string) eebus.EntityResolution {
	if f.entity == nil {
		return eebus.EntityResolution{}
	}
	return eebus.EntityResolution{Entity: f.entity, DeviceCount: 1}
}

func (f *fakeRoomHeatingSystemFunctionWriter) State(
	entity spineapi.EntityRemoteInterface,
) (RoomHeatingSystemFunctionState, error) {
	f.stateEntity = entity
	return f.state, f.stateErr
}

func (f *fakeRoomHeatingSystemFunctionWriter) WriteOperationMode(
	_ context.Context,
	entity spineapi.EntityRemoteInterface,
	mode string,
) error {
	f.writeEntity = entity
	f.mode = mode
	return f.writeErr
}

func TestRoomHeatingSystemFunctionAdapterComposesMRHSFAndCRHSFBySKI(t *testing.T) {
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Ski().Return("ab:cd").Twice()
	monitoringEntity := spinemocks.NewEntityRemoteInterface(t)
	monitoringEntity.EXPECT().Device().Return(device).Twice()
	configurationEntity := spinemocks.NewEntityRemoteInterface(t)

	reader := &fakeRoomHeatingSystemFunctionReader{entity: monitoringEntity, state: RoomHeatingSystemFunctionState{
		OperationMode: "auto", AvailableModes: []string{"auto", "on", "off"},
	}}
	writer := &fakeRoomHeatingSystemFunctionWriter{entity: configurationEntity, state: RoomHeatingSystemFunctionState{
		OperationMode: "off", AvailableModes: []string{"off"}, ModeWritable: true,
	}}
	adapter := NewRoomHeatingSystemFunctionAdapter(reader, writer)

	state, err := adapter.State(monitoringEntity)
	if err != nil || state.OperationMode != "auto" || !state.ModeWritable || len(state.AvailableModes) != 3 {
		t.Fatalf("State() = %+v, %v", state, err)
	}
	if writer.stateEntity != configurationEntity {
		t.Fatal("State() passed the monitoring entity to the CRHSF configuration owner")
	}
	if err := adapter.WriteOperationMode(context.Background(), monitoringEntity, "on"); err != nil {
		t.Fatalf("WriteOperationMode() error = %v", err)
	}
	if writer.writeEntity != configurationEntity || writer.mode != "on" {
		t.Fatalf("WriteOperationMode() entity/mode = %p/%q, want configuration entity/on", writer.writeEntity, writer.mode)
	}
}

func TestRoomHeatingSystemFunctionAdapterFailsClosedWithoutCRHSF(t *testing.T) {
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Ski().Return("ab:cd")
	entity := spinemocks.NewEntityRemoteInterface(t)
	entity.EXPECT().Device().Return(device)
	adapter := NewRoomHeatingSystemFunctionAdapter(&fakeRoomHeatingSystemFunctionReader{}, &fakeRoomHeatingSystemFunctionWriter{})

	if err := adapter.WriteOperationMode(context.Background(), entity, "on"); !errors.Is(err, ErrRoomHeatingSysFnNotWritable) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrRoomHeatingSysFnNotWritable", err)
	}
}

func TestRoomHeatingSystemFunctionAdapterPrevalidatesMRHSFModes(t *testing.T) {
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Ski().Return("ab:cd")
	monitoringEntity := spinemocks.NewEntityRemoteInterface(t)
	monitoringEntity.EXPECT().Device().Return(device)
	configurationEntity := spinemocks.NewEntityRemoteInterface(t)
	reader := &fakeRoomHeatingSystemFunctionReader{state: RoomHeatingSystemFunctionState{
		AvailableModes: []string{"auto", "on"},
	}}
	writer := &fakeRoomHeatingSystemFunctionWriter{entity: configurationEntity}

	err := NewRoomHeatingSystemFunctionAdapter(reader, writer).WriteOperationMode(
		context.Background(), monitoringEntity, "off",
	)
	if !errors.Is(err, ErrRoomHeatingSysFnInvalidMode) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrRoomHeatingSysFnInvalidMode", err)
	}
	if writer.writeEntity != nil {
		t.Fatal("configuration writer was called for a mode absent from MRHSF")
	}
}

func TestRoomHeatingSystemFunctionAdapterFailsClosedWhenMRHSFStateIsUnavailable(t *testing.T) {
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Ski().Return("ab:cd")
	monitoringEntity := spinemocks.NewEntityRemoteInterface(t)
	monitoringEntity.EXPECT().Device().Return(device)
	configurationEntity := spinemocks.NewEntityRemoteInterface(t)
	reader := &fakeRoomHeatingSystemFunctionReader{stateErr: ErrRoomHeatingSysFnDataUnavailable}
	writer := &fakeRoomHeatingSystemFunctionWriter{entity: configurationEntity}

	err := NewRoomHeatingSystemFunctionAdapter(reader, writer).WriteOperationMode(
		context.Background(), monitoringEntity, "on",
	)
	if !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrRoomHeatingSysFnDataUnavailable", err)
	}
	if writer.writeEntity != nil {
		t.Fatal("configuration writer was called with unavailable MRHSF state")
	}
}

func TestRoomHeatingSystemFunctionAdapterKeepsMRHSFReadsWhenCRHSFStateIsUnavailable(t *testing.T) {
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Ski().Return("ab:cd")
	monitoringEntity := spinemocks.NewEntityRemoteInterface(t)
	monitoringEntity.EXPECT().Device().Return(device)
	configurationEntity := spinemocks.NewEntityRemoteInterface(t)
	reader := &fakeRoomHeatingSystemFunctionReader{state: RoomHeatingSystemFunctionState{OperationMode: "auto"}}
	writer := &fakeRoomHeatingSystemFunctionWriter{
		entity:   configurationEntity,
		state:    RoomHeatingSystemFunctionState{ModeWritable: true},
		stateErr: ErrRoomHeatingSysFnDataUnavailable,
	}

	state, err := NewRoomHeatingSystemFunctionAdapter(reader, writer).State(monitoringEntity)
	if err != nil || state.OperationMode != "auto" || state.ModeWritable {
		t.Fatalf("State() = %+v, %v", state, err)
	}
}

func TestRoomHeatingSystemFunctionMonitoringMapsCurrentModeErrors(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	uc := ucmocks.NewMaMRHSFInterface(t)
	uc.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{ucapi.HvacOperationModeTypeAuto}, nil)
	uc.EXPECT().CurrentOperationMode(entity).Return(ucapi.HvacOperationModeType(""), eebusapi.ErrDataNotAvailable)

	wrapper := &RoomHeatingSystemFunctionMonitoring{uc: uc}
	if _, err := wrapper.State(entity); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("State() error = %v, want ErrRoomHeatingSysFnDataUnavailable", err)
	}
}

func TestRoomHeatingSystemFunctionMonitoringResolvesCompatibleEntity(t *testing.T) {
	uc := ucmocks.NewMaMRHSFInterface(t)
	uc.EXPECT().RemoteEntitiesScenarios().Return(nil)

	wrapper := &RoomHeatingSystemFunctionMonitoring{uc: uc}
	if resolution := wrapper.CompatibleEntity("ab:cd"); resolution.Entity != nil || resolution.DeviceCount != 0 {
		t.Fatalf("CompatibleEntity() = %+v, want empty resolution", resolution)
	}
}

func TestRoomHeatingSystemFunctionAdapterFailsClosedWithoutMonitoring(t *testing.T) {
	var adapter *RoomHeatingSystemFunctionAdapter
	if resolution := adapter.CompatibleEntity("ab:cd"); resolution.Entity != nil {
		t.Fatalf("CompatibleEntity() = %+v, want empty resolution", resolution)
	}
	if _, err := adapter.State(nil); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("State() error = %v, want ErrRoomHeatingSysFnDataUnavailable", err)
	}

	empty := NewRoomHeatingSystemFunctionAdapter(nil, nil)
	if resolution := empty.CompatibleEntity("ab:cd"); resolution.Entity != nil {
		t.Fatalf("CompatibleEntity() = %+v, want empty resolution", resolution)
	}
	if _, err := empty.State(nil); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("State() error = %v, want ErrRoomHeatingSysFnDataUnavailable", err)
	}
	if err := empty.WriteOperationMode(context.Background(), nil, "on"); !errors.Is(err, ErrRoomHeatingSysFnNotWritable) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrRoomHeatingSysFnNotWritable", err)
	}
}

func TestRoomHeatingSystemFunctionAdapterDelegatesResolutionAndReadErrors(t *testing.T) {
	monitoringEntity := spinemocks.NewEntityRemoteInterface(t)
	reader := &fakeRoomHeatingSystemFunctionReader{
		entity:   monitoringEntity,
		stateErr: ErrRoomHeatingSysFnDataUnavailable,
	}
	adapter := NewRoomHeatingSystemFunctionAdapter(reader, &fakeRoomHeatingSystemFunctionWriter{})

	if resolution := adapter.CompatibleEntity("ab:cd"); resolution.Entity != monitoringEntity {
		t.Fatalf("CompatibleEntity() = %+v, want the monitoring entity", resolution)
	}
	if _, err := adapter.State(monitoringEntity); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("State() error = %v, want ErrRoomHeatingSysFnDataUnavailable", err)
	}
}

func TestRoomHeatingSystemFunctionAdapterKeepsMRHSFReadsWithoutCRHSFEntity(t *testing.T) {
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Ski().Return("ab:cd")
	monitoringEntity := spinemocks.NewEntityRemoteInterface(t)
	monitoringEntity.EXPECT().Device().Return(device)
	reader := &fakeRoomHeatingSystemFunctionReader{state: RoomHeatingSystemFunctionState{OperationMode: "auto"}}

	adapter := NewRoomHeatingSystemFunctionAdapter(reader, &fakeRoomHeatingSystemFunctionWriter{})
	state, err := adapter.State(monitoringEntity)
	if err != nil || state.OperationMode != "auto" || state.ModeWritable {
		t.Fatalf("State() = %+v, %v", state, err)
	}

	detached := spinemocks.NewEntityRemoteInterface(t)
	detached.EXPECT().Device().Return(nil)
	if err := adapter.WriteOperationMode(context.Background(), detached, "on"); !errors.Is(err, ErrRoomHeatingSysFnNotWritable) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrRoomHeatingSysFnNotWritable", err)
	}
}

func TestRoomHeatingSystemFunctionMonitoringOptionalPaths(t *testing.T) {
	wrapper := NewRoomHeatingSystemFunctionMonitoring(nil, eebus.NewDeviceRegistry(), true)
	wrapper.Setup(nil)
	if wrapper.UseCase() != nil {
		t.Fatal("UseCase() initialized for nil local entity")
	}
	if resolution := wrapper.CompatibleEntity("ab:cd"); resolution.Entity != nil {
		t.Fatalf("CompatibleEntity() = %+v", resolution)
	}
	wrapper.HandleEvent("ab:cd", nil, nil, mamrhsf.DataUpdateOperationMode)
	wrapper.HandleEvent("ab:cd", nil, nil, eebusapi.EventType("unknown"))
}
