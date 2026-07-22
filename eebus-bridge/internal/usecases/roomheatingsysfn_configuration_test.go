package usecases

import (
	"context"
	"errors"
	"testing"

	eebusapi "github.com/enbility/eebus-go/api"
	cacrhsf "github.com/enbility/eebus-go/usecases/ca/crhsf"
	ucmocks "github.com/enbility/eebus-go/usecases/mocks"
	spineapi "github.com/enbility/spine-go/api"
	spinemocks "github.com/enbility/spine-go/mocks"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestUpstreamRoomHeatingSystemFunctionConfigurationSelectsCRHSF(t *testing.T) {
	facade := NewUpstreamRoomHeatingSystemFunctionConfiguration(clientUsecaseLocalEntity(t), false)
	client, ok := facade.UseCase().(*cacrhsf.CRHSF)
	if !ok {
		t.Fatalf("UseCase() = %T, want *crhsf.CRHSF", facade.UseCase())
	}
	if client.EventCB != nil {
		t.Fatal("upstream CRHSF has an event callback; MRHSF must remain the sole state-event owner")
	}
	writer, ok := facade.operationModeWriter.(*upstreamRoomHeatingOperationModeWriter)
	if !ok {
		t.Fatalf("operationModeWriter = %T, want *upstreamRoomHeatingOperationModeWriter", facade.operationModeWriter)
	}
	if writer.client != client {
		t.Fatalf("writer client = %T, want facade CRHSF client", writer.client)
	}
	if _, ok := writer.inspector.(bridgeRoomHeatingSystemFunctionCapabilityInspector); !ok {
		t.Fatalf("writer inspector = %T, want bridge capability inspector", writer.inspector)
	}
}

type phase2RoomHeatingCapabilityInspector struct {
	state  RoomHeatingSystemFunctionState
	entity spineapi.EntityRemoteInterface
	err    error
}

func (i *phase2RoomHeatingCapabilityInspector) State(
	entity spineapi.EntityRemoteInterface,
) (RoomHeatingSystemFunctionState, error) {
	i.entity = entity
	return i.state, i.err
}

type phase2RoomHeatingWriter struct {
	entity spineapi.EntityRemoteInterface
	mode   string
	err    error
}

func (w *phase2RoomHeatingWriter) WriteOperationMode(
	_ context.Context,
	entity spineapi.EntityRemoteInterface,
	mode string,
) error {
	w.entity = entity
	w.mode = mode
	return w.err
}

func TestCRHSFConfigurationFacadeComposesUpstreamEntityAndSelectedStrategies(t *testing.T) {
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Ski().Return("ab:cd").Maybe()
	entity := spinemocks.NewEntityRemoteInterface(t)
	entity.EXPECT().Device().Return(device).Maybe()
	client := ucmocks.NewCaCRHSFInterface(t)
	client.EXPECT().RemoteEntitiesScenarios().Return([]eebusapi.RemoteEntityScenarios{{
		Entity: entity, Scenarios: []uint{1},
	}})
	inspector := &phase2RoomHeatingCapabilityInspector{
		state: RoomHeatingSystemFunctionState{ModeWritable: true},
	}
	writer := &phase2RoomHeatingWriter{}
	facade := newCRHSFConfigurationFacade(
		client,
		crhsfEntityResolver{useCase: client},
		inspector,
		writer,
	)

	resolution := facade.CompatibleEntity("ABCD")
	if resolution.Entity != entity || resolution.DeviceCount != 1 {
		t.Fatalf("CompatibleEntity() = %+v, want upstream CRHSF entity", resolution)
	}
	state, err := facade.State(entity)
	if err != nil || !state.ModeWritable || inspector.entity != entity {
		t.Fatalf("State() = %+v, %v; inspector entity = %p", state, err, inspector.entity)
	}
	if err := facade.WriteOperationMode(context.Background(), entity, "on"); err != nil {
		t.Fatalf("WriteOperationMode() error = %v", err)
	}
	if writer.entity != entity || writer.mode != "on" {
		t.Fatalf("writer entity/mode = %p/%q, want upstream entity/on", writer.entity, writer.mode)
	}
	if facade.UseCase() != client {
		t.Fatalf("UseCase() = %T, want injected upstream client", facade.UseCase())
	}
}

func TestBridgeRoomHeatingCapabilityInspectorPreservesUnavailableAndReadOnly(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	for _, test := range []struct {
		name         string
		legacyState  RoomHeatingSystemFunctionState
		legacyError  error
		wantWritable bool
		wantError    error
	}{
		{
			name:         "writable",
			legacyState:  RoomHeatingSystemFunctionState{OperationMode: "auto", AvailableModes: []string{"auto"}, ModeWritable: true},
			wantWritable: true,
		},
		{
			name:        "negotiated read only",
			legacyState: RoomHeatingSystemFunctionState{OperationMode: "off", AvailableModes: []string{"off"}},
		},
		{
			name:        "cache incomplete",
			legacyError: ErrRoomHeatingSysFnDataUnavailable,
			wantError:   ErrRoomHeatingSysFnDataUnavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			legacy := &phase2RoomHeatingCapabilityInspector{state: test.legacyState, err: test.legacyError}
			state, err := (bridgeRoomHeatingSystemFunctionCapabilityInspector{state: legacy}).State(entity)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("State() error = %v, want %v", err, test.wantError)
			}
			if state.ModeWritable != test.wantWritable {
				t.Fatalf("State() = %+v, want writable=%t", state, test.wantWritable)
			}
			if state.OperationMode != "" || len(state.AvailableModes) != 0 {
				t.Fatalf("State() leaked legacy read ownership: %+v", state)
			}
		})
	}
}

func TestCRHSFConfigurationFacadeFailsClosedWhenIncomplete(t *testing.T) {
	var nilFacade *CRHSFConfigurationFacade
	if nilFacade.UseCase() != nil {
		t.Fatal("nil facade returned a use case")
	}
	if resolution := nilFacade.CompatibleEntity("ABCD"); resolution != (eebus.EntityResolution{}) {
		t.Fatalf("CompatibleEntity() = %+v", resolution)
	}
	if _, err := nilFacade.State(nil); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("State() error = %v, want data unavailable", err)
	}
	if err := nilFacade.WriteOperationMode(context.Background(), nil, "on"); !errors.Is(err, ErrRoomHeatingSysFnNotWritable) {
		t.Fatalf("WriteOperationMode() error = %v, want not writable", err)
	}

	empty := NewUpstreamRoomHeatingSystemFunctionConfiguration(nil, false)
	if empty.UseCase() != nil {
		t.Fatal("nil local entity initialized CRHSF")
	}
	if resolution := empty.CompatibleEntity("ABCD"); resolution != (eebus.EntityResolution{}) {
		t.Fatalf("empty CompatibleEntity() = %+v", resolution)
	}
	if _, err := empty.State(nil); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("empty State() error = %v, want data unavailable", err)
	}
	if err := empty.WriteOperationMode(context.Background(), nil, "on"); !errors.Is(err, ErrRoomHeatingSysFnNotWritable) {
		t.Fatalf("empty WriteOperationMode() error = %v, want not writable", err)
	}
	if resolution := (crhsfEntityResolver{}).CompatibleEntity("ABCD"); resolution != (eebus.EntityResolution{}) {
		t.Fatalf("empty resolver = %+v", resolution)
	}
	if _, err := (bridgeRoomHeatingSystemFunctionCapabilityInspector{}).State(nil); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("empty inspector error = %v", err)
	}
}
