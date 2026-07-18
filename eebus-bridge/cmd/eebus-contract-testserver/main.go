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

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
)

type fakePayloadSource struct{}

func (fakePayloadSource) AttachMeasurementPayload(event *pb.MeasurementEvent, _ string, eventType pb.MeasurementEventType) {
	switch eventType {
	case pb.MeasurementEventType_MEASUREMENT_EVENT_POWER_PER_PHASE_UPDATED:
		event.Data = &pb.MeasurementEvent_Measurements{Measurements: &pb.MeasurementList{Measurements: []*pb.MeasurementEntry{
			{Type: "power_l1", Value: 100, Unit: "W"},
			{Type: "power_l2", Value: 200, Unit: "W"},
			{Type: "power_l3", Value: 300, Unit: "W"},
		}}}
	default:
		event.Data = &pb.MeasurementEvent_Power{Power: &pb.PowerMeasurement{Watts: 600}}
	}
}

func (fakePayloadSource) AttachLPCPayload(event *pb.LPCEvent, _ string, _ pb.LPCEventType) {
	event.Data = &pb.LPCEvent_LimitUpdate{LimitUpdate: &pb.LoadLimit{
		ValueWatts: 4200, IsActive: true, IsChangeable: true,
	}}
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

type fakeTrust struct {
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
}

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
			},
		}),
		bridgegrpc.WithDeviceStatePayloads(bridgegrpc.DeviceStatePayloadSources{
			Monitoring: payloads, LPC: payloads, DHW: payloads, HVAC: payloads, OHPCF: payloads,
		}),
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
