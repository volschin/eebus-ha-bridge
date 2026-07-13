package grpc

import (
	"context"
	"errors"
	"testing"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"github.com/volschin/eebus-bridge/internal/usecases"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeDHWController struct {
	entity       spineapi.EntityRemoteInterface
	state        usecases.DHWSetpoint
	stateErr     error
	writeErr     error
	writtenValue float64
}

func (f *fakeDHWController) CompatibleEntity(string) spineapi.EntityRemoteInterface {
	return f.entity
}

func (f *fakeDHWController) State(spineapi.EntityRemoteInterface) (usecases.DHWSetpoint, error) {
	return f.state, f.stateErr
}

func (f *fakeDHWController) Write(_ context.Context, _ spineapi.EntityRemoteInterface, value float64) error {
	f.writtenValue = value
	return f.writeErr
}

func TestDHWServiceGetAndSet(t *testing.T) {
	controller := &fakeDHWController{
		entity: mocks.NewEntityRemoteInterface(t),
		state: usecases.DHWSetpoint{
			Value: 46, Minimum: 35, Maximum: 70, Step: 1, Writable: true,
		},
	}
	service := NewDHWService(controller, eebus.NewEventBus())

	response, err := service.GetDHWSetpoint(context.Background(), &pb.DeviceRequest{Ski: "test"})
	if err != nil {
		t.Fatalf("GetDHWSetpoint() error = %v", err)
	}
	if response.ValueCelsius != 46 || response.MinCelsius != 35 || response.MaxCelsius != 70 ||
		response.StepCelsius != 1 || !response.Writable {
		t.Errorf("GetDHWSetpoint() = %+v", response)
	}

	if _, err := service.SetDHWSetpoint(
		context.Background(),
		&pb.SetDHWSetpointRequest{Ski: "test", ValueCelsius: 47},
	); err != nil {
		t.Fatalf("SetDHWSetpoint() error = %v", err)
	}
	if controller.writtenValue != 47 {
		t.Errorf("written value = %v, want 47", controller.writtenValue)
	}
}

func TestDHWServiceMapsValidationErrors(t *testing.T) {
	controller := &fakeDHWController{
		entity:   mocks.NewEntityRemoteInterface(t),
		writeErr: fmtWrap(usecases.ErrDHWOutOfRange),
	}
	service := NewDHWService(controller, eebus.NewEventBus())

	_, err := service.SetDHWSetpoint(
		context.Background(),
		&pb.SetDHWSetpointRequest{Ski: "test", ValueCelsius: 80},
	)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SetDHWSetpoint() code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestDHWServiceRequiresControllerAndSKI(t *testing.T) {
	service := NewDHWService(nil, eebus.NewEventBus())
	if _, err := service.GetDHWSetpoint(context.Background(), &pb.DeviceRequest{}); status.Code(err) != codes.Unavailable {
		t.Errorf("GetDHWSetpoint() code = %v, want Unavailable", status.Code(err))
	}
	if _, err := service.SetDHWSetpoint(context.Background(), &pb.SetDHWSetpointRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("SetDHWSetpoint() code = %v, want InvalidArgument", status.Code(err))
	}
}

func fmtWrap(err error) error { return errors.Join(errors.New("validation failed"), err) }
