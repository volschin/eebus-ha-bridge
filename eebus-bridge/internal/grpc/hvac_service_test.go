package grpc

import (
	"context"
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeRoomHeatingTemp struct {
	entity   spineapi.EntityRemoteInterface
	entities map[string]spineapi.EntityRemoteInterface
	state    usecases.RoomHeatingSetpoint
	states   map[spineapi.EntityRemoteInterface]usecases.RoomHeatingSetpoint
	err      error
}

type failingTemperatureReader struct{ err error }

func (f failingTemperatureReader) Temperature(string) (float64, error) { return 0, f.err }

func (f *fakeRoomHeatingTemp) CompatibleEntity(ski string) eebus.EntityResolution {
	if f.entities == nil {
		return eebus.EntityResolution{Entity: f.entity, DeviceCount: 1}
	}
	if eebus.NormalizeSKI(ski) == "" {
		return eebus.EntityResolution{DeviceCount: len(f.entities)}
	}
	entity := f.entities[eebus.NormalizeSKI(ski)]
	if entity == nil {
		return eebus.EntityResolution{}
	}
	return eebus.EntityResolution{Entity: entity, DeviceCount: 1}
}
func (f *fakeRoomHeatingTemp) State(entity spineapi.EntityRemoteInterface) (usecases.RoomHeatingSetpoint, error) {
	if f.states != nil {
		return f.states[entity], f.err
	}
	return f.state, f.err
}

func TestHVACServiceIsolatesCompatibleDevices(t *testing.T) {
	deviceA := mocks.NewEntityRemoteInterface(t)
	deviceB := mocks.NewEntityRemoteInterface(t)
	temp := &fakeRoomHeatingTemp{
		entities: map[string]spineapi.EntityRemoteInterface{
			eebus.NormalizeSKI(testValidSKI):      deviceA,
			eebus.NormalizeSKI(testOtherValidSKI): deviceB,
		},
		states: map[spineapi.EntityRemoteInterface]usecases.RoomHeatingSetpoint{
			deviceA: {Value: 19},
			deviceB: {Value: 23},
		},
	}
	svc := NewHVACService(temp, nil, nil, nil)

	if _, err := svc.GetRoomHeating(context.Background(), &pb.DeviceRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("empty SKI error = %v, want FailedPrecondition", err)
	}
	for _, test := range []struct {
		ski  string
		want float64
	}{
		{ski: testValidSKI, want: 19},
		{ski: eebus.NormalizeSKI(testOtherValidSKI), want: 23},
	} {
		state, err := svc.GetRoomHeating(context.Background(), &pb.DeviceRequest{Ski: test.ski})
		if err != nil {
			t.Fatalf("GetRoomHeating(%q): %v", test.ski, err)
		}
		if state.Setpoint == nil || state.Setpoint.ValueCelsius != test.want {
			t.Errorf("GetRoomHeating(%q) = %+v, want setpoint %v", test.ski, state, test.want)
		}
	}
	if _, err := svc.GetRoomHeating(context.Background(), &pb.DeviceRequest{Ski: testUnknownValidSKI}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown explicit SKI error = %v, want NotFound", err)
	}
}
func (f *fakeRoomHeatingTemp) Write(context.Context, spineapi.EntityRemoteInterface, float64) error {
	return f.err
}

func TestHVACServiceGetRoomHeatingReturnsNotFoundWithoutCompatibleEntity(t *testing.T) {
	svc := NewHVACService(&fakeRoomHeatingTemp{}, nil, nil, nil)
	_, err := svc.GetRoomHeating(context.Background(), &pb.DeviceRequest{Ski: testUnknownValidSKI})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("GetRoomHeating() error = %v, want NotFound", err)
	}
}

func TestHVACServiceGetRoomHeatingReturnsSetpoint(t *testing.T) {
	entity := mocks.NewEntityRemoteInterface(t)
	temp := &fakeRoomHeatingTemp{
		entity: entity,
		state:  usecases.RoomHeatingSetpoint{Value: 21, Minimum: 5, Maximum: 30, Step: 0.5, Writable: true},
	}
	state, err := NewHVACService(temp, nil, nil, nil).GetRoomHeating(
		context.Background(),
		&pb.DeviceRequest{Ski: testValidSKI},
	)
	if err != nil {
		t.Fatalf("GetRoomHeating() error = %v", err)
	}
	if state.Setpoint == nil || state.Setpoint.ValueCelsius != 21 {
		t.Errorf("GetRoomHeating() = %+v", state)
	}
}

func TestHVACAggregateAllPartReadsFailedIsUnavailable(t *testing.T) {
	entity := mocks.NewEntityRemoteInterface(t)
	registry := eebus.NewDeviceRegistry()
	temp := &fakeRoomHeatingTemp{entity: entity, err: usecases.ErrRoomHeatingDataUnavailable}
	svc := NewHVACService(temp, nil, failingTemperatureReader{err: usecases.ErrRoomHeatingDataUnavailable}, nil, registry)

	state, err := svc.GetRoomHeating(context.Background(), &pb.DeviceRequest{Ski: testValidSKI})
	if state != nil || status.Code(err) != codes.Unavailable {
		t.Fatalf("GetRoomHeating() = (%+v, %v), want nil/Unavailable", state, err)
	}
	capabilities, _ := registry.DeviceCapabilities(testValidSKI)
	for _, capability := range capabilities {
		if capability.ID == eebus.CapabilityRoomHeating {
			if capability.State != eebus.CapabilityStateTemporarilyUnavailable || capability.Reason != eebus.CapabilityReasonReadFailed {
				t.Fatalf("room heating capability = %+v", capability)
			}
			return
		}
	}
	t.Fatal("room heating capability missing")
}

func TestHVACServiceSetRoomHeatingTemperatureRequiresSKI(t *testing.T) {
	svc := NewHVACService(&fakeRoomHeatingTemp{}, nil, nil, nil)
	_, err := svc.SetRoomHeatingTemperature(
		context.Background(),
		&pb.SetRoomHeatingTemperatureRequest{ValueCelsius: 21},
	)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SetRoomHeatingTemperature() error = %v, want InvalidArgument", err)
	}
}

func TestHVACServiceMapsOutOfRangeToInvalidArgument(t *testing.T) {
	entity := mocks.NewEntityRemoteInterface(t)
	temp := &fakeRoomHeatingTemp{entity: entity, err: usecases.ErrRoomHeatingOutOfRange}
	svc := NewHVACService(temp, nil, nil, nil)
	_, err := svc.SetRoomHeatingTemperature(
		context.Background(),
		&pb.SetRoomHeatingTemperatureRequest{Ski: testValidSKI, ValueCelsius: 99},
	)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SetRoomHeatingTemperature() error = %v, want InvalidArgument", err)
	}
}
