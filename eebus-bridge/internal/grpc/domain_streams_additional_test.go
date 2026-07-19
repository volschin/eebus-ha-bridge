package grpc

import (
	"context"
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
	grpcclient "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type fakeRoomSystemFunctionController struct {
	entity spineapi.EntityRemoteInterface
	state  usecases.RoomHeatingSystemFunctionState
}

func (f *fakeRoomSystemFunctionController) CompatibleEntity(string) eebus.EntityResolution {
	return eebus.EntityResolution{Entity: f.entity, DeviceCount: 1}
}

func (f *fakeRoomSystemFunctionController) State(spineapi.EntityRemoteInterface) (usecases.RoomHeatingSystemFunctionState, error) {
	return f.state, nil
}

func (*fakeRoomSystemFunctionController) WriteOperationMode(context.Context, spineapi.EntityRemoteInterface, string) error {
	return nil
}

func TestDomainEventStreamsTranslateAndAttachPayloads(t *testing.T) {
	bus := eebus.NewEventBus()
	entity := mocks.NewEntityRemoteInterface(t)
	dhw := NewDHWService(
		&fakeDHWController{entity: entity, state: usecases.DHWSetpoint{Value: 46, Writable: true}},
		&fakeDHWSysFnController{entity: entity, state: usecases.DHWSystemFunctionState{
			BoostStatus: "active", OperationMode: "auto", AvailableModes: []string{"auto", "off"}, ModeWritable: true,
		}},
		bus,
	)
	hvac := NewHVACService(
		&fakeRoomHeatingTemp{entity: entity, state: usecases.RoomHeatingSetpoint{Value: 21, Writable: true}},
		&fakeRoomSystemFunctionController{entity: entity, state: usecases.RoomHeatingSystemFunctionState{
			OperationMode: "auto", AvailableModes: []string{"auto", "off"}, ModeWritable: true,
		}},
		fixedTemperatureReader{value: 20.5},
		bus,
	)
	ohpcf := NewOHPCFService(nil, bus, nil, WithOHPCFController(partialOHPCFController{entity: entity}))

	server := NewServer("127.0.0.1", 0, false)
	pb.RegisterDHWServiceServer(server.GRPCServer(), dhw)
	pb.RegisterHVACServiceServer(server.GRPCServer(), hvac)
	pb.RegisterOHPCFServiceServer(server.GRPCServer(), ohpcf)
	server.SetHealthy(true)
	go func() { _ = server.Start() }()
	t.Cleanup(server.Stop)
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer readyCancel()
	if err := server.WaitReady(readyCtx); err != nil {
		t.Fatalf("server readiness: %v", err)
	}

	connection, err := grpcclient.NewClient(server.Addr(), grpcclient.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	streamCtx, streamCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer streamCancel()
	request := &pb.DeviceRequest{Ski: testValidSKI}
	dhwEvents, err := pb.NewDHWServiceClient(connection).SubscribeDHWEvents(streamCtx, request)
	if err != nil {
		t.Fatal(err)
	}
	dhwSystemEvents, err := pb.NewDHWServiceClient(connection).SubscribeDHWSystemFunctionEvents(streamCtx, request)
	if err != nil {
		t.Fatal(err)
	}
	hvacEvents, err := pb.NewHVACServiceClient(connection).SubscribeRoomHeatingEvents(streamCtx, request)
	if err != nil {
		t.Fatal(err)
	}
	ohpcfEvents, err := pb.NewOHPCFServiceClient(connection).SubscribeOHPCFEvents(streamCtx, request)
	if err != nil {
		t.Fatal(err)
	}

	// Allow all server handlers to install their EventBus subscriptions.
	time.Sleep(50 * time.Millisecond)

	for _, eventType := range []eebus.EventType{
		eebus.EventTypeDHWUseCaseSupportUpdated,
		eebus.EventTypeDHWSetpointUpdated,
	} {
		bus.Publish(eebus.Event{SKI: testValidSKI, Type: eventType})
		event, recvErr := dhwEvents.Recv()
		if recvErr != nil || event.GetSetpoint() == nil {
			t.Fatalf("DHW event %q = (%+v, %v)", eventType, event, recvErr)
		}
	}
	for _, eventType := range []eebus.EventType{
		eebus.EventTypeDHWSystemFunctionSupportUpdated,
		eebus.EventTypeDHWSystemFunctionUpdated,
	} {
		bus.Publish(eebus.Event{SKI: testValidSKI, Type: eventType})
		event, recvErr := dhwSystemEvents.Recv()
		if recvErr != nil || event.GetState() == nil {
			t.Fatalf("DHW system event %q = (%+v, %v)", eventType, event, recvErr)
		}
	}
	for _, eventType := range []eebus.EventType{
		eebus.EventTypeRoomHeatingUseCaseSupportUpdated,
		eebus.EventTypeRoomHeatingSystemFunctionSupportUpdated,
		eebus.EventTypeRoomTemperatureUpdated,
		eebus.EventTypeRoomHeatingSetpointUpdated,
		eebus.EventTypeRoomHeatingSystemFunctionUpdated,
	} {
		bus.Publish(eebus.Event{SKI: testValidSKI, Type: eventType})
		event, recvErr := hvacEvents.Recv()
		if recvErr != nil || event.GetState() == nil {
			t.Fatalf("HVAC event %q = (%+v, %v)", eventType, event, recvErr)
		}
	}
	for _, eventType := range []eebus.EventType{
		eebus.EventTypeOHPCFUseCaseSupportUpdated,
		eebus.EventTypeOHPCFConsumptionStateUpdated,
		eebus.EventTypeOHPCFConsumptionStoppableUpdated,
		eebus.EventTypeOHPCFConsumptionPausableUpdated,
		eebus.EventTypeOHPCFConsumptionStartTimeUpdated,
		eebus.EventTypeOHPCFRequestedPowerEstimateUpdated,
		eebus.EventTypeOHPCFRequestedPowerMaxUpdated,
		eebus.EventTypeOHPCFMinimalRunDurationUpdated,
		eebus.EventTypeOHPCFMinimalPauseDurationUpdated,
	} {
		bus.Publish(eebus.Event{SKI: testValidSKI, Type: eventType})
		event, recvErr := ohpcfEvents.Recv()
		if recvErr != nil || event.GetFlexibility() == nil {
			t.Fatalf("OHPCF event %q = (%+v, %v)", eventType, event, recvErr)
		}
	}
}
