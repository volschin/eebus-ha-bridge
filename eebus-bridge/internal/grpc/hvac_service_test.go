package grpc

import (
	"context"
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeRoomHeatingTemp struct {
	entity spineapi.EntityRemoteInterface
	state  usecases.RoomHeatingSetpoint
	err    error
}

func (f *fakeRoomHeatingTemp) CompatibleEntity(string) spineapi.EntityRemoteInterface {
	return f.entity
}
func (f *fakeRoomHeatingTemp) State(spineapi.EntityRemoteInterface) (usecases.RoomHeatingSetpoint, error) {
	return f.state, f.err
}
func (f *fakeRoomHeatingTemp) Write(context.Context, spineapi.EntityRemoteInterface, float64) error {
	return f.err
}

func TestHVACServiceGetRoomHeatingReturnsNotFoundWithoutCompatibleEntity(t *testing.T) {
	svc := NewHVACService(&fakeRoomHeatingTemp{}, nil, nil, nil)
	_, err := svc.GetRoomHeating(context.Background(), &pb.DeviceRequest{Ski: "missing"})
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
		&pb.DeviceRequest{Ski: "test"},
	)
	if err != nil {
		t.Fatalf("GetRoomHeating() error = %v", err)
	}
	if state.Setpoint == nil || state.Setpoint.ValueCelsius != 21 {
		t.Errorf("GetRoomHeating() = %+v", state)
	}
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
		&pb.SetRoomHeatingTemperatureRequest{Ski: "test", ValueCelsius: 99},
	)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SetRoomHeatingTemperature() error = %v, want InvalidArgument", err)
	}
}
