// Command eebus-contract-testserver exposes deterministic fake EEBUS data
// through the production gRPC server and device-state adapter. It is used only
// by the cross-language CI contract test and needs no SHIP, mDNS, or hardware.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakePayloadSource struct{}

type fakeRecoverySource struct{}

func (fakeRecoverySource) Snapshot(_ string, now time.Time) eebus.RecoverySnapshot {
	return eebus.RecoverySnapshot{
		State:            eebus.RecoveryStateHealthy,
		LastTransitionAt: now.Add(-time.Minute),
	}
}

type fakeProviderDiagnostics struct{}

func (fakeProviderDiagnostics) ProviderDiagnostics(now time.Time) []*pb.ProviderSampleDiagnostics {
	return []*pb.ProviderSampleDiagnostics{{
		Provider:   "grid",
		State:      pb.ProviderSampleState_PROVIDER_SAMPLE_STATE_CURRENT,
		ObservedAt: timestamppb.New(now.Add(-2 * time.Second)),
		ValidUntil: timestamppb.New(now.Add(time.Minute)),
	}}
}

func measurement(id pb.MeasurementId, typ string, value float64, unit string) *pb.MeasurementEntry {
	return &pb.MeasurementEntry{Id: id.Enum(), Type: typ, Value: value, Unit: unit}
}

func (fakePayloadSource) SnapshotMeasurements(ski string) (*pb.MeasurementList, error) {
	power := 600.0
	if eebus.NormalizeSKI(ski) == "2222222222222222222222222222222222222222" {
		power = 700
	}
	return &pb.MeasurementList{Measurements: []*pb.MeasurementEntry{
		measurement(pb.MeasurementId_MEASUREMENT_ID_POWER_CONSUMPTION, "power_consumption", power, "W"),
		measurement(pb.MeasurementId_MEASUREMENT_ID_POWER_L1, "power_l1", 100, "W"),
		measurement(pb.MeasurementId_MEASUREMENT_ID_POWER_L2, "power_l2", 200, "W"),
		measurement(pb.MeasurementId_MEASUREMENT_ID_POWER_L3, "power_l3", 300, "W"),
		measurement(pb.MeasurementId_MEASUREMENT_ID_ENERGY_CONSUMED, "energy_consumed", 42, "kWh"),
		measurement(pb.MeasurementId_MEASUREMENT_ID_ROOM_TEMPERATURE, "room_temperature", 20.5, "degC"),
	}}, nil
}

func (fakePayloadSource) AttachMeasurementPayload(event *pb.MeasurementEvent, ski string, eventType pb.MeasurementEventType) {
	switch eventType {
	case pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_PER_PHASE_UPDATED:
		if eebus.NormalizeSKI(ski) == dropDiagnosticsSKI {
			measurements := make([]*pb.MeasurementEntry, 512)
			for index := range measurements {
				measurements[index] = measurement(pb.MeasurementId_MEASUREMENT_ID_POWER_L1, "power_l1", float64(index), "W")
			}
			event.Data = &pb.MeasurementEvent_Measurements{Measurements: &pb.MeasurementList{Measurements: measurements}}
			return
		}
		event.Data = &pb.MeasurementEvent_Measurements{Measurements: &pb.MeasurementList{Measurements: []*pb.MeasurementEntry{
			measurement(pb.MeasurementId_MEASUREMENT_ID_POWER_L1, "power_l1", 100, "W"),
			measurement(pb.MeasurementId_MEASUREMENT_ID_POWER_L2, "power_l2", 200, "W"),
			measurement(pb.MeasurementId_MEASUREMENT_ID_POWER_L3, "power_l3", 300, "W"),
		}}}
	default:
		event.Data = &pb.MeasurementEvent_Power{Power: &pb.PowerMeasurement{Watts: 600}}
	}
}

func (fakePayloadSource) AttachLPCPayload(event *pb.LPCEvent, _ string, eventType pb.LPCEventType) {
	switch eventType {
	case pb.LPCEventType_LPC_EVENT_LIMIT_UPDATED:
		event.Data = &pb.LPCEvent_LimitUpdate{LimitUpdate: &pb.LoadLimit{
			ValueWatts: 4200, IsActive: true, IsChangeable: true,
		}}
	case pb.LPCEventType_LPC_EVENT_FAILSAFE_UPDATED:
		event.Data = &pb.LPCEvent_FailsafeUpdate{FailsafeUpdate: &pb.FailsafeLimit{ValueWatts: 5000}}
	case pb.LPCEventType_LPC_EVENT_HEARTBEAT_TIMEOUT:
		event.Data = &pb.LPCEvent_HeartbeatUpdate{HeartbeatUpdate: &pb.HeartbeatStatus{Running: true, WithinDuration: true}}
	}
}

func (fakePayloadSource) SnapshotHeartbeat(string) (*pb.HeartbeatStatus, error) {
	return &pb.HeartbeatStatus{Running: true, WithinDuration: true}, nil
}

func (fakePayloadSource) AttachDHWPayload(event *pb.DHWEvent, _ string) {
	event.Setpoint = &pb.DHWSetpoint{ValueCelsius: 50, MinCelsius: 35, MaxCelsius: 65, Writable: true}
}

func (fakePayloadSource) AttachDHWSystemFunctionPayload(event *pb.DHWSystemFunctionEvent, _ string) {
	event.State = &pb.DHWSystemFunctionState{OperationMode: "auto"}
}

func (fakePayloadSource) AttachRoomHeatingPayload(event *pb.RoomHeatingEvent, _ string) bool {
	current := 20.5
	event.State = &pb.RoomHeatingState{
		CurrentTemperatureCelsius: &current,
		Setpoint:                  &pb.RoomHeatingSetpoint{ValueCelsius: 21, Writable: true},
	}
	return true
}

func (fakePayloadSource) AttachOHPCFPayload(event *pb.OHPCFEvent, _ string, _ eebus.EventType) bool {
	estimate := 1500.0
	event.Flexibility = &pb.CompressorFlexibility{
		Available: true, RequestedPowerEstimateW: &estimate,
		State: pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_AVAILABLE,
	}
	return true
}

func (fakePayloadSource) RefreshCompressorFlexibility(string) {}

type fakeTrust struct {
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
}

const dropDiagnosticsSKI = "3333333333333333333333333333333333333333"

func (f fakeTrust) RegisterSKI(ski string) error {
	f.registry.AddDevice(ski, eebus.DeviceInfo{})
	f.registry.MarkConnected(ski)
	for _, capability := range eebus.AllCapabilities {
		f.registry.RecordCapabilitySupport(ski, capability, true)
		f.registry.RecordCapabilityRead(ski, capability, nil)
	}
	for _, eventType := range []eebus.EventType{
		eebus.EventTypeDeviceConnected,
		eebus.EventTypeMonitoringPowerPerPhaseUpdated,
		eebus.EventTypeLPCLimitUpdated,
		eebus.EventTypeDHWSetpointUpdated,
		eebus.EventTypeRoomHeatingSetpointUpdated,
		eebus.EventTypeOHPCFConsumptionStateUpdated,
		eebus.EventTypeLPCUseCaseSupportUpdated,
		eebus.EventTypeMGCPConsumerUpdated,
	} {
		f.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
	}
	if eebus.NormalizeSKI(ski) == dropDiagnosticsSKI {
		// Flood the already-open cross-language stream faster than its bounded
		// EventBus channel can drain. This deterministically exercises the
		// production drop -> one coalesced resync path without a test-only RPC.
		for range 256 {
			f.bus.Publish(eebus.Event{SKI: ski, Type: eebus.EventTypeMonitoringPowerPerPhaseUpdated})
		}
	}
	return nil
}

func (fakeTrust) UnregisterSKI(string) error { return nil }

func main() {
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()
	registry.SetLocalCapabilityEnabled(eebus.CapabilityOHPCF, true)
	payloads := fakePayloadSource{}
	service := bridgegrpc.NewDeviceService(
		eebus.NewCallbacks(bus, false),
		bus,
		"0000000000000000000000000000000000000000",
		registry,
		fakeTrust{bus: bus, registry: registry},
		bridgegrpc.WithServerInfo(&pb.ServerInfo{
			ApiMajor: 1, ApiMinor: 0, BridgeBuildVersion: "contract-test",
			Features: []pb.FeatureId{
				pb.FeatureId_FEATURE_EXPLICIT_CAPABILITIES,
				pb.FeatureId_FEATURE_CONSOLIDATED_DEVICE_STREAM,
				pb.FeatureId_FEATURE_PROVIDER_SAMPLE_INVALIDATION,
				pb.FeatureId_FEATURE_DEVICE_SNAPSHOT,
				pb.FeatureId_FEATURE_TYPED_MEASUREMENTS,
				pb.FeatureId_FEATURE_OPERATIONAL_DIAGNOSTICS,
			},
		}),
		bridgegrpc.WithDeviceStatePayloads(bridgegrpc.DeviceStatePayloadSources{
			Monitoring: payloads, LPC: payloads, DHW: payloads, HVAC: payloads, OHPCF: payloads,
		}),
		bridgegrpc.WithOperationalDiagnostics(fakeRecoverySource{}, fakeProviderDiagnostics{}),
	)
	server := bridgegrpc.NewServer("127.0.0.1", 0, false)
	pb.RegisterDeviceServiceServer(server.GRPCServer(), service)
	errors := make(chan error, 1)
	go func() { errors <- server.Start() }()
	if err := server.WaitReady(context.Background()); err != nil {
		panic(err)
	}
	server.SetHealthy(true)
	fmt.Printf("READY %s\n", server.Addr())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err := <-errors:
		if err != nil {
			panic(err)
		}
	}
	server.Stop()
}
