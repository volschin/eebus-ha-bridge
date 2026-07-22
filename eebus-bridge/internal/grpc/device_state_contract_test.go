package grpc

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

type completeDeviceStatePayloads struct{}

func (completeDeviceStatePayloads) AttachMeasurementPayload(event *pb.MeasurementEvent, _ string, eventType pb.MeasurementEventType) {
	switch eventType {
	case pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_UPDATED:
		event.Data = &pb.MeasurementEvent_Power{Power: &pb.PowerMeasurement{Watts: 123}}
	case pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_UPDATED:
		event.Data = &pb.MeasurementEvent_Energy{Energy: &pb.EnergyMeasurement{KilowattHours: 45}}
	case pb.MeasurementEventType_MEASUREMENT_EVENT_DEVICE_OPERATING_STATE_UPDATED:
		event.Data = &pb.MeasurementEvent_DeviceDiagnostics{DeviceDiagnostics: &pb.DeviceDiagnosticsData{OperatingState: "running"}}
	case pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_PER_PHASE_UPDATED,
		pb.MeasurementEventType_MEASUREMENT_EVENT_CURRENT_PER_PHASE_UPDATED,
		pb.MeasurementEventType_MEASUREMENT_EVENT_VOLTAGE_PER_PHASE_UPDATED,
		pb.MeasurementEventType_MEASUREMENT_EVENT_FREQUENCY_UPDATED,
		pb.MeasurementEventType_MEASUREMENT_EVENT_ENERGY_PRODUCED_UPDATED:
		event.Data = &pb.MeasurementEvent_Measurements{Measurements: &pb.MeasurementList{Measurements: []*pb.MeasurementEntry{{Type: "typed", Value: 1}}}}
	default:
		event.Data = &pb.MeasurementEvent_Measurement{Measurement: &pb.MeasurementEntry{Type: "temperature", Value: 20}}
	}
}

func (completeDeviceStatePayloads) AttachLPCPayload(event *pb.LPCEvent, _ string, eventType pb.LPCEventType) {
	switch eventType {
	case pb.LPCEventType_LPC_EVENT_LIMIT_UPDATED:
		event.Data = &pb.LPCEvent_LimitUpdate{LimitUpdate: &pb.LoadLimit{ValueWatts: 1000}}
	case pb.LPCEventType_LPC_EVENT_FAILSAFE_UPDATED:
		event.Data = &pb.LPCEvent_FailsafeUpdate{FailsafeUpdate: &pb.FailsafeLimit{ValueWatts: 500}}
	case pb.LPCEventType_LPC_EVENT_HEARTBEAT_TIMEOUT:
		event.Data = &pb.LPCEvent_HeartbeatUpdate{HeartbeatUpdate: &pb.HeartbeatStatus{Running: true, WithinDuration: true}}
	}
}

func (completeDeviceStatePayloads) AttachDHWPayload(event *pb.DHWEvent, _ string) {
	event.Setpoint = &pb.DHWSetpoint{ValueCelsius: 50}
}

func (completeDeviceStatePayloads) AttachDHWSystemFunctionPayload(event *pb.DHWSystemFunctionEvent, _ string) {
	event.State = &pb.DHWSystemFunctionState{OperationMode: "auto"}
}

func (completeDeviceStatePayloads) AttachRoomHeatingPayload(event *pb.RoomHeatingEvent, _ string) bool {
	value := 20.0
	event.State = &pb.RoomHeatingState{CurrentTemperatureCelsius: &value}
	switch event.GetEventType() {
	case pb.RoomHeatingEventType_ROOM_HEATING_EVENT_SETPOINT_UPDATED:
		event.State.Setpoint = &pb.RoomHeatingSetpoint{ValueCelsius: 21}
	case pb.RoomHeatingEventType_ROOM_HEATING_EVENT_SYSTEM_FUNCTION_UPDATED:
		event.State.SystemFunction = &pb.RoomHeatingSystemFunction{OperationMode: "auto"}
	}
	return true
}

func (completeDeviceStatePayloads) AttachOHPCFPayload(event *pb.OHPCFEvent, _ string, _ eebus.EventType) bool {
	event.Flexibility = &pb.CompressorFlexibility{Available: true}
	return true
}

func (completeDeviceStatePayloads) RefreshCompressorFlexibility(string) {}

func TestEveryDeclaredEventTypeHasExplicitDeviceStateClassification(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	eventsFile := filepath.Join(filepath.Dir(filename), "..", "eebus", "events.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), eventsFile, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	ast.Inspect(parsed, func(node ast.Node) bool {
		decl, ok := node.(*ast.GenDecl)
		if !ok || decl.Tok != token.CONST {
			return true
		}
		for _, spec := range decl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok || len(valueSpec.Values) != 1 {
				continue
			}
			literal, ok := valueSpec.Values[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			value, unquoteErr := strconv.Unquote(literal.Value)
			if unquoteErr != nil {
				t.Fatal(unquoteErr)
			}
			for _, name := range valueSpec.Names {
				if !strings.HasPrefix(name.Name, "EventType") {
					continue
				}
				count++
				if _, classified := classifyDeviceStateEvent(eebus.EventType(value)); !classified {
					t.Errorf("%s (%s) has no explicit device-state classification", name.Name, value)
				}
			}
		}
		return false
	})
	if count == 0 {
		t.Fatal("event type declaration scan found no constants")
	}
}

func TestEveryStateDeltaProducesTypedAvailablePayload(t *testing.T) {
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	payloads := completeDeviceStatePayloads{}
	service := NewDeviceService(
		eebus.NewCallbacks(bus, false), bus, "local", registry, nil,
		WithDeviceStatePayloads(DeviceStatePayloadSources{
			Monitoring: payloads, LPC: payloads, DHW: payloads, HVAC: payloads, OHPCF: payloads,
		}),
	)

	for _, eventType := range declaredStateDeltaEventTypes(t) {
		t.Run(string(eventType), func(t *testing.T) {
			event := service.deviceStateEnvelope(eebus.Event{
				SKI: testValidSKI, Type: eventType, Revision: 1, OccurredAt: time.Now(),
			})
			wantAvailability := pb.EventAvailability_EVENT_AVAILABILITY_AVAILABLE
			if event.GetAvailability() != wantAvailability {
				t.Fatalf("availability = %s, want %s, envelope = %v", event.GetAvailability(), wantAvailability, event)
			}
			if event.Payload == nil || event.GetResyncRequired() != nil {
				t.Fatalf("state delta lacks typed payload: %v", event)
			}
			if measurement := event.GetMeasurement(); measurement != nil &&
				measurement.GetEventType() == pb.MeasurementEventType_MEASUREMENT_EVENT_UNSPECIFIED &&
				measurement.GetMeasurements() == nil {
				t.Fatalf("measurement event has neither legacy type nor typed list: %v", event)
			}
			if ohpcf := event.GetOhpcf(); ohpcf != nil && ohpcf.GetEventType() != pb.OHPCFEventType_OHPCF_EVENT_STATE_UPDATED &&
				ohpcf.GetEventType() != pb.OHPCFEventType_OHPCF_EVENT_DATA_UPDATED {
				t.Fatalf("OHPCF event breaks legacy enum contract: %v", event)
			}
		})
	}
}

func declaredStateDeltaEventTypes(t *testing.T) []eebus.EventType {
	t.Helper()
	result := make([]eebus.EventType, 0)
	// The AST-backed exhaustiveness test above guards the classification itself;
	// this list exercises the complete current payload contract.
	for _, eventType := range []eebus.EventType{
		eebus.EventTypeDeviceConnected, eebus.EventTypeDeviceDisconnected, eebus.EventTypeDeviceTrustRemoved,
		eebus.EventTypeMonitoringPowerUpdated, eebus.EventTypeMonitoringPowerPerPhaseUpdated,
		eebus.EventTypeMonitoringEnergyConsumedUpdated, eebus.EventTypeMonitoringEnergyProducedUpdated,
		eebus.EventTypeMonitoringCurrentsPerPhaseUpdated, eebus.EventTypeMonitoringVoltagePerPhaseUpdated,
		eebus.EventTypeMonitoringFrequencyUpdated, eebus.EventTypeMonitoringFlowTemperatureUpdated,
		eebus.EventTypeMonitoringReturnTemperatureUpdated, eebus.EventTypeMonitoringDeviceOperatingStateUpdated,
		eebus.EventTypeLPCLimitUpdated, eebus.EventTypeLPCFailsafePowerUpdated,
		eebus.EventTypeLPCFailsafeDurationUpdated, eebus.EventTypeLPCHeartbeatUpdated,
		eebus.EventTypeDHWTemperatureUpdated, eebus.EventTypeDHWSetpointUpdated,
		eebus.EventTypeDHWSystemFunctionUpdated, eebus.EventTypeRoomTemperatureUpdated,
		eebus.EventTypeOutdoorTemperatureUpdated, eebus.EventTypeRoomHeatingSetpointUpdated,
		eebus.EventTypeRoomHeatingSystemFunctionUpdated, eebus.EventTypeOHPCFConsumptionStateUpdated,
		eebus.EventTypeOHPCFConsumptionStoppableUpdated, eebus.EventTypeOHPCFConsumptionPausableUpdated,
		eebus.EventTypeOHPCFConsumptionStartTimeUpdated, eebus.EventTypeOHPCFRequestedPowerEstimateUpdated,
		eebus.EventTypeOHPCFRequestedPowerMaxUpdated, eebus.EventTypeOHPCFMinimalRunDurationUpdated,
		eebus.EventTypeOHPCFMinimalPauseDurationUpdated,
	} {
		classification, known := classifyDeviceStateEvent(eventType)
		if !known || classification != deviceStateEventStateDelta {
			t.Fatalf("test fixture contains non-state delta %s", eventType)
		}
		result = append(result, eventType)
	}
	return result
}

func TestUnknownEventRequiresResyncInsteadOfProviderRelabel(t *testing.T) {
	service := NewDeviceService(nil, nil, "local", nil, nil)
	event := service.deviceStateEnvelope(eebus.Event{SKI: testValidSKI, Type: "future.domain", OccurredAt: time.Now()})
	if event.GetResyncRequired().GetReason() != pb.ResyncReason_RESYNC_REASON_UNCLASSIFIED_EVENT {
		t.Fatalf("unknown event = %v", event)
	}
}

func TestClassificationUpdateRequestsFreshSnapshot(t *testing.T) {
	service := NewDeviceService(nil, nil, "local", nil, nil)
	event := service.deviceStateEnvelope(eebus.Event{
		SKI: testValidSKI, Type: eebus.EventTypeDeviceClassificationUpdated, OccurredAt: time.Now(),
	})
	if event.GetResyncRequired().GetReason() != pb.ResyncReason_RESYNC_REASON_INITIAL_STATE_REQUIRED {
		t.Fatalf("classification update envelope = %v", event)
	}
}

type missingMeasurementPayload struct{}

func (missingMeasurementPayload) AttachMeasurementPayload(*pb.MeasurementEvent, string, pb.MeasurementEventType) {
}

func TestMissingDetailMeasurementKeepsLegacyFallbackAndExplicitTarget(t *testing.T) {
	service := NewDeviceService(
		nil,
		nil,
		"local",
		nil,
		nil,
		WithDeviceStatePayloads(DeviceStatePayloadSources{Monitoring: missingMeasurementPayload{}}),
	)
	event := service.deviceStateEnvelope(eebus.Event{
		SKI: testValidSKI, Type: eebus.EventTypeMonitoringPowerPerPhaseUpdated, OccurredAt: time.Now(),
	})
	measurement := event.GetMeasurement()
	if measurement.GetEventType() != pb.MeasurementEventType_MEASUREMENT_EVENT_UNSPECIFIED ||
		measurement.GetUpdateField() != pb.MeasurementUpdateField_MEASUREMENT_UPDATE_FIELD_POWER_PER_PHASE ||
		event.GetAvailability() != pb.EventAvailability_EVENT_AVAILABILITY_TEMPORARILY_UNAVAILABLE {
		t.Fatalf("missing detail event = %v", event)
	}
}

type partialOHPCFController struct {
	entity       spineapi.EntityRemoteInterface
	availableErr error
	stoppableErr error
	startErr     error
	estimateErr  error
	maxErr       error
}

func (f partialOHPCFController) CompatibleEntity(string) eebus.EntityResolution {
	return eebus.EntityResolution{Entity: f.entity, DeviceCount: 1}
}
func (f partialOHPCFController) OptionalPowerConsumptionAvailable(spineapi.EntityRemoteInterface) (bool, error) {
	return true, f.availableErr
}
func (f partialOHPCFController) RequestedPowerEstimate(spineapi.EntityRemoteInterface) (float64, error) {
	return 1000, f.estimateErr
}
func (f partialOHPCFController) RequestedPowerMax(spineapi.EntityRemoteInterface) (float64, error) {
	return 2000, f.maxErr
}
func (f partialOHPCFController) ConsumptionIsStoppable(spineapi.EntityRemoteInterface) (bool, error) {
	return false, f.stoppableErr
}
func (partialOHPCFController) ConsumptionIsPausable(spineapi.EntityRemoteInterface) (bool, error) {
	return true, nil
}
func (partialOHPCFController) ConsumptionState(spineapi.EntityRemoteInterface) (ucapi.CompressorPowerConsumptionStateType, error) {
	return ucapi.CompressorPowerConsumptionStateRunning, nil
}
func (f partialOHPCFController) ConsumptionStartTime(spineapi.EntityRemoteInterface) (time.Time, error) {
	return time.Unix(123, 0), f.startErr
}
func (partialOHPCFController) MinimalRunDuration(spineapi.EntityRemoteInterface) (time.Duration, error) {
	return time.Minute, nil
}
func (partialOHPCFController) MinimalPauseDuration(spineapi.EntityRemoteInterface) (time.Duration, error) {
	return 2 * time.Minute, nil
}
func (partialOHPCFController) Schedule(spineapi.EntityRemoteInterface, time.Time) error { return nil }
func (partialOHPCFController) Pause(spineapi.EntityRemoteInterface) error               { return nil }
func (partialOHPCFController) Resume(spineapi.EntityRemoteInterface) error              { return nil }
func (partialOHPCFController) Abort(spineapi.EntityRemoteInterface) error               { return nil }

func TestOHPCFEnvelopeAvailabilityTracksTheUpdatedField(t *testing.T) {
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	registry.AddDevice(testValidSKI, eebus.DeviceInfo{})
	controller := partialOHPCFController{
		entity:       mocks.NewEntityRemoteInterface(t),
		stoppableErr: errors.New("stoppable cache miss"),
	}
	ohpcf := NewOHPCFService(nil, bus, registry, WithOHPCFController(controller))
	service := NewDeviceService(nil, bus, "local", registry, nil, WithDeviceStatePayloads(DeviceStatePayloadSources{OHPCF: ohpcf}))

	missing := service.deviceStateEnvelope(eebus.Event{
		SKI: testValidSKI, Type: eebus.EventTypeOHPCFConsumptionStoppableUpdated, OccurredAt: time.Now(),
	})
	if missing.GetAvailability() != pb.EventAvailability_EVENT_AVAILABILITY_TEMPORARILY_UNAVAILABLE {
		t.Fatalf("stoppable availability = %s", missing.GetAvailability())
	}
	if missing.GetOhpcf().GetEventType() != pb.OHPCFEventType_OHPCF_EVENT_DATA_UPDATED {
		t.Fatalf("legacy event type = %s", missing.GetOhpcf().GetEventType())
	}
	if missing.GetOhpcf().GetFlexibility() == nil {
		t.Fatalf("legacy-stream clients must keep the partial payload: %v", missing)
	}
	absentPermissionController := controller
	absentPermissionController.stoppableErr = eebusapi.ErrDataNotAvailable
	absentPermissionService := NewDeviceService(
		nil,
		bus,
		"local",
		registry,
		nil,
		WithDeviceStatePayloads(DeviceStatePayloadSources{
			OHPCF: NewOHPCFService(nil, bus, registry, WithOHPCFController(absentPermissionController)),
		}),
	)
	absentPermission := absentPermissionService.deviceStateEnvelope(eebus.Event{
		SKI: testValidSKI, Type: eebus.EventTypeOHPCFConsumptionStoppableUpdated, OccurredAt: time.Now(),
	})
	if absentPermission.GetAvailability() != pb.EventAvailability_EVENT_AVAILABILITY_AVAILABLE ||
		absentPermission.GetOhpcf().GetFlexibility().GetIsStoppable() {
		t.Fatalf("absent optional stoppable permission = %v", absentPermission)
	}
	missingStateSource := controller
	missingStateSource.availableErr = errors.New("availability cache miss")
	missingStateService := NewDeviceService(
		nil,
		bus,
		"local",
		registry,
		nil,
		WithDeviceStatePayloads(DeviceStatePayloadSources{
			OHPCF: NewOHPCFService(nil, bus, registry, WithOHPCFController(missingStateSource)),
		}),
	)
	missingState := missingStateService.deviceStateEnvelope(eebus.Event{
		SKI: testValidSKI, Type: eebus.EventTypeOHPCFConsumptionStateUpdated, OccurredAt: time.Now(),
	})
	if missingState.GetAvailability() != pb.EventAvailability_EVENT_AVAILABILITY_TEMPORARILY_UNAVAILABLE {
		t.Fatalf("state without derived availability = %v", missingState)
	}

	completeController := controller
	completeController.stoppableErr = nil
	completeService := NewDeviceService(
		nil,
		bus,
		"local",
		registry,
		nil,
		WithDeviceStatePayloads(DeviceStatePayloadSources{
			OHPCF: NewOHPCFService(nil, bus, registry, WithOHPCFController(completeController)),
		}),
	)
	available := completeService.deviceStateEnvelope(eebus.Event{
		SKI: testValidSKI, Type: eebus.EventTypeOHPCFConsumptionStartTimeUpdated, OccurredAt: time.Now(),
	})
	if available.GetAvailability() != pb.EventAvailability_EVENT_AVAILABILITY_AVAILABLE ||
		available.GetOhpcf().GetFlexibility().GetStartTime() == nil ||
		available.GetOhpcf().GetUpdateField() != pb.OHPCFUpdateField_OHPCF_UPDATE_FIELD_START_TIME {
		t.Fatalf("start-time event = %v", available)
	}
	capabilities, _ := registry.DeviceCapabilities(testValidSKI)
	foundOHPCF := false
	for _, capability := range capabilities {
		if capability.ID == eebus.CapabilityOHPCF {
			foundOHPCF = true
			if capability.State != eebus.CapabilityStateAvailable {
				t.Fatalf("OHPCF capability after successful payload read = %+v", capability)
			}
		}
	}
	if !foundOHPCF {
		t.Fatal("OHPCF capability missing after successful payload read")
	}

	clearedController := completeController
	clearedController.startErr = eebusapi.ErrDataInvalid
	clearedService := NewDeviceService(
		nil,
		bus,
		"local",
		registry,
		nil,
		WithDeviceStatePayloads(DeviceStatePayloadSources{
			OHPCF: NewOHPCFService(nil, bus, registry, WithOHPCFController(clearedController)),
		}),
	)
	cleared := clearedService.deviceStateEnvelope(eebus.Event{
		SKI: testValidSKI, Type: eebus.EventTypeOHPCFConsumptionStartTimeUpdated, OccurredAt: time.Now(),
	})
	if cleared.GetAvailability() != pb.EventAvailability_EVENT_AVAILABILITY_AVAILABLE ||
		cleared.GetOhpcf().GetFlexibility().GetStartTime() != nil {
		t.Fatalf("cleared start-time event = %v", cleared)
	}
}
