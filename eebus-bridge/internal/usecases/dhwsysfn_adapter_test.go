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
	entity spineapi.EntityRemoteInterface
	state  DHWSystemFunctionState
}

func (f *fakeDHWSysFnReader) CompatibleEntity(string) eebus.EntityResolution {
	return eebus.EntityResolution{Entity: f.entity, DeviceCount: 1}
}

func (f *fakeDHWSysFnReader) State(spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error) {
	return f.state, nil
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
