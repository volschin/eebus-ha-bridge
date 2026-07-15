package usecases

import (
	"errors"
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/stretchr/testify/mock"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestDeviceOperatingStateReadsResponse(t *testing.T) {
	reader, local, remote := deviceOperatingStateHarness(t)
	local.On("AddResponseCallback", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		callback := args.Get(1).(func(spineapi.ResponseMessage))
		callback(spineapi.ResponseMessage{Data: diagnosisState("normalOperation")})
	}).Return(nil)
	remote.On("DataCopy", model.FunctionTypeDeviceDiagnosisStateData).Return(nil).Maybe()

	state, err := reader.OperatingState("test-ski")
	if err != nil {
		t.Fatalf("OperatingState() error = %v", err)
	}
	if state != "normalOperation" {
		t.Fatalf("OperatingState() = %q, want normalOperation", state)
	}
}

func TestDeviceOperatingStateReadsCacheFallback(t *testing.T) {
	reader, local, remote := deviceOperatingStateHarness(t)
	local.On("AddResponseCallback", mock.Anything, mock.Anything).Return(nil)
	remote.On("DataCopy", model.FunctionTypeDeviceDiagnosisStateData).Return(diagnosisState("standby"))
	setDeviceOperatingStateReadTimeout(t, time.Millisecond)

	state, err := reader.OperatingState("test-ski")
	if err != nil {
		t.Fatalf("OperatingState() error = %v", err)
	}
	if state != "standby" {
		t.Fatalf("OperatingState() = %q, want standby", state)
	}
}

func TestDeviceOperatingStateReturnsUnavailableOnTimeout(t *testing.T) {
	reader, local, remote := deviceOperatingStateHarness(t)
	local.On("AddResponseCallback", mock.Anything, mock.Anything).Return(nil)
	remote.On("DataCopy", model.FunctionTypeDeviceDiagnosisStateData).Return(nil)
	setDeviceOperatingStateReadTimeout(t, time.Millisecond)

	_, err := reader.OperatingState("test-ski")
	if !errors.Is(err, ErrDeviceOperatingStateUnavailable) {
		t.Fatalf("OperatingState() error = %v, want ErrDeviceOperatingStateUnavailable", err)
	}
}

func TestDeviceOperatingStatePassesThroughUnknownState(t *testing.T) {
	reader, local, remote := deviceOperatingStateHarness(t)
	local.On("AddResponseCallback", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		callback := args.Get(1).(func(spineapi.ResponseMessage))
		callback(spineapi.ResponseMessage{Data: diagnosisState("futureVendorState")})
	}).Return(nil)
	remote.On("DataCopy", model.FunctionTypeDeviceDiagnosisStateData).Return(nil).Maybe()

	state, err := reader.OperatingState("test-ski")
	if err != nil {
		t.Fatalf("OperatingState() error = %v", err)
	}
	if state != "futureVendorState" {
		t.Fatalf("OperatingState() = %q, want futureVendorState", state)
	}
}

func TestDeviceOperatingStateHandleEventPublishesFromPayload(t *testing.T) {
	bus := eebus.NewEventBus()
	// No local/remote feature mocks: HandleEvent must publish from the payload
	// alone, without issuing a network read.
	reader := NewDeviceOperatingState(bus, eebus.NewDeviceRegistry(), false)
	ch := bus.Subscribe()
	t.Cleanup(func() { bus.Unsubscribe(ch) })

	reader.HandleEvent(spineapi.EventPayload{
		Ski:        "test-ski",
		Entity:     mocks.NewEntityRemoteInterface(t),
		EventType:  spineapi.EventTypeDataChange,
		ChangeType: spineapi.ElementChangeUpdate,
		Data:       diagnosisState("normalOperation"),
	})

	select {
	case evt := <-ch:
		if evt.Type != eebus.EventTypeMonitoringDeviceOperatingStateUpdated || evt.SKI != "TESTSKI" {
			t.Fatalf("event = %+v", evt)
		}
	default:
		t.Fatal("expected published event")
	}
}

func TestDeviceOperatingStateHandleEventIgnoresUnrelatedUpdates(t *testing.T) {
	reader := NewDeviceOperatingState(eebus.NewEventBus(), eebus.NewDeviceRegistry(), false)

	reader.HandleEvent(spineapi.EventPayload{
		Entity:     mocks.NewEntityRemoteInterface(t),
		EventType:  spineapi.EventTypeDataChange,
		ChangeType: spineapi.ElementChangeUpdate,
		Data:       &model.MeasurementListDataType{},
	})
	reader.HandleEvent(spineapi.EventPayload{
		Entity:     mocks.NewEntityRemoteInterface(t),
		EventType:  spineapi.EventTypeDataChange,
		ChangeType: spineapi.ElementChangeAdd,
		Data:       diagnosisState("normalOperation"),
	})
}

func deviceOperatingStateHarness(
	t *testing.T,
) (*DeviceOperatingState, *mocks.FeatureLocalInterface, *mocks.FeatureRemoteInterface) {
	t.Helper()
	remote := mocks.NewFeatureRemoteInterface(t)
	remote.On("Type").Return(model.FeatureTypeTypeDeviceDiagnosis)
	remote.On("Role").Return(model.RoleTypeServer)
	remote.On("String").Return("remote device diagnosis").Maybe()

	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeServer).Return(remote)
	entity.On("Address").Return(&model.EntityAddressType{})
	entity.On("EntityType").Return(model.EntityTypeTypeHeatPumpAppliance)
	entity.On("Features").Return([]spineapi.FeatureRemoteInterface{remote})

	registry := eebus.NewDeviceRegistry()
	registry.AddDevice("test-ski", eebus.DeviceInfo{SKI: "test-ski"})
	registry.UpsertObservation("test-ski", nil, entity, "device_diagnosis")

	local := mocks.NewFeatureLocalInterface(t)
	local.On("String").Return("local device diagnosis").Maybe()
	local.On(
		"RequestRemoteData",
		model.FunctionTypeDeviceDiagnosisStateData,
		nil,
		nil,
		remote,
	).Return(ptr(model.MsgCounterType(1)), (*model.ErrorType)(nil))
	localEntity := mocks.NewEntityLocalInterface(t)
	localEntity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeClient).Return(local)

	reader := NewDeviceOperatingState(eebus.NewEventBus(), registry, false)
	reader.localEntity = localEntity
	return reader, local, remote
}

func diagnosisState(value string) *model.DeviceDiagnosisStateDataType {
	state := model.DeviceDiagnosisOperatingStateType(value)
	return &model.DeviceDiagnosisStateDataType{OperatingState: &state}
}

func setDeviceOperatingStateReadTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	original := deviceOperatingStateReadTimeout
	deviceOperatingStateReadTimeout = timeout
	t.Cleanup(func() { deviceOperatingStateReadTimeout = original })
}
