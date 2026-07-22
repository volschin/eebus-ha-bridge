package usecases

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucmocks "github.com/enbility/eebus-go/usecases/mocks"
	spineapi "github.com/enbility/spine-go/api"
	spinemocks "github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/stretchr/testify/mock"
)

func writableRoomHeatingSetpoint() RoomHeatingSetpoint {
	return RoomHeatingSetpoint{
		Value: 21, Minimum: 5, Maximum: 30, Step: 0.5, Writable: true,
	}
}

func TestUpstreamRoomHeatingTemperatureWriterValidatesAndAwaitsResult(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHTInterface(t)
	counter := model.MsgCounterType(51)
	client.EXPECT().WriteRoomAirTemperatureSetpoint(entity, 21.5, mock.Anything).RunAndReturn(
		func(_ spineapi.EntityRemoteInterface, _ float64, callback func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
			callback(model.ResultDataType{}, counter)
			return &counter, nil
		},
	)
	writer := &upstreamRoomHeatingTemperatureWriter{
		client: client,
		reader: &phase4RoomHeatingTemperatureReader{state: writableRoomHeatingSetpoint()},
	}

	if err := writer.Write(context.Background(), entity, 21.5); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
}

func TestUpstreamRoomHeatingTemperatureWriterValidatesBeforeSending(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHTInterface(t)
	tests := []struct {
		name  string
		state RoomHeatingSetpoint
		value float64
		want  error
	}{
		{name: "read only", state: RoomHeatingSetpoint{Minimum: 5, Maximum: 30, Step: 0.5}, value: 21, want: ErrRoomHeatingNotWritable},
		{name: "below range", state: writableRoomHeatingSetpoint(), value: 4.5, want: ErrRoomHeatingOutOfRange},
		{name: "above range", state: writableRoomHeatingSetpoint(), value: 30.5, want: ErrRoomHeatingOutOfRange},
		{name: "not finite", state: writableRoomHeatingSetpoint(), value: math.NaN(), want: ErrRoomHeatingOutOfRange},
		{name: "wrong step", state: writableRoomHeatingSetpoint(), value: 21.25, want: ErrRoomHeatingInvalidStep},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := &upstreamRoomHeatingTemperatureWriter{
				client: client,
				reader: &phase4RoomHeatingTemperatureReader{state: test.state},
			}
			if err := writer.Write(context.Background(), entity, test.value); !errors.Is(err, test.want) {
				t.Fatalf("Write() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestUpstreamRoomHeatingTemperatureWriterReturnsDeviceRejection(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHTInterface(t)
	counter := model.MsgCounterType(52)
	description := model.DescriptionType("setpoint blocked")
	client.EXPECT().WriteRoomAirTemperatureSetpoint(entity, 21.5, mock.Anything).RunAndReturn(
		func(_ spineapi.EntityRemoteInterface, _ float64, callback func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
			callback(model.ResultDataType{
				ErrorNumber: ptr(model.ErrorNumberTypeCommandRejected),
				Description: &description,
			}, counter)
			return &counter, nil
		},
	)
	writer := &upstreamRoomHeatingTemperatureWriter{
		client: client,
		reader: &phase4RoomHeatingTemperatureReader{state: writableRoomHeatingSetpoint()},
	}

	err := writer.Write(context.Background(), entity, 21.5)
	if !errors.Is(err, ErrRoomHeatingRejected) || !strings.Contains(err.Error(), string(description)) {
		t.Fatalf("Write() error = %v, want device rejection", err)
	}
}

func TestUpstreamRoomHeatingTemperatureWriterMapsSendFailuresWithoutFallback(t *testing.T) {
	sendErr := errors.New("send failed")
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "not writable", err: eebusapi.ErrNotSupported, want: ErrRoomHeatingNotWritable},
		{name: "cache unavailable", err: eebusapi.ErrDataNotAvailable, want: ErrRoomHeatingDataUnavailable},
		{name: "invalid cached data", err: eebusapi.ErrDataInvalid, want: ErrRoomHeatingDataUnavailable},
		{name: "disconnected", err: eebusapi.ErrDeviceDisconnected, want: ErrRoomHeatingDataUnavailable},
		{name: "transport error", err: sendErr, want: sendErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			entity := spinemocks.NewEntityRemoteInterface(t)
			client := ucmocks.NewCaCRHTInterface(t)
			client.EXPECT().WriteRoomAirTemperatureSetpoint(entity, 21.5, mock.Anything).Return(nil, test.err)
			writer := &upstreamRoomHeatingTemperatureWriter{
				client: client,
				reader: &phase4RoomHeatingTemperatureReader{state: writableRoomHeatingSetpoint()},
			}
			if err := writer.Write(context.Background(), entity, 21.5); !errors.Is(err, test.want) {
				t.Fatalf("Write() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestUpstreamRoomHeatingTemperatureWriterHonoursCancellationAndTimeout(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	counter := model.MsgCounterType(53)

	t.Run("canceled", func(t *testing.T) {
		client := ucmocks.NewCaCRHTInterface(t)
		client.EXPECT().WriteRoomAirTemperatureSetpoint(entity, 21.5, mock.Anything).Return(&counter, nil)
		writer := &upstreamRoomHeatingTemperatureWriter{
			client: client,
			reader: &phase4RoomHeatingTemperatureReader{state: writableRoomHeatingSetpoint()},
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := writer.Write(ctx, entity, 21.5); !errors.Is(err, context.Canceled) {
			t.Fatalf("Write() error = %v, want context.Canceled", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		client := ucmocks.NewCaCRHTInterface(t)
		client.EXPECT().WriteRoomAirTemperatureSetpoint(entity, 21.5, mock.Anything).Return(&counter, nil)
		writer := &upstreamRoomHeatingTemperatureWriter{
			client: client,
			reader: &phase4RoomHeatingTemperatureReader{state: writableRoomHeatingSetpoint()},
		}
		previousTimeout := roomHeatingTemperatureWriteTimeout
		roomHeatingTemperatureWriteTimeout = time.Millisecond
		t.Cleanup(func() { roomHeatingTemperatureWriteTimeout = previousTimeout })
		if err := writer.Write(context.Background(), entity, 21.5); err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("Write() error = %v, want timeout", err)
		}
	})
}

func TestUpstreamRoomHeatingTemperatureWriterRejectsMissingOrForeignCounter(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)

	t.Run("missing", func(t *testing.T) {
		client := ucmocks.NewCaCRHTInterface(t)
		client.EXPECT().WriteRoomAirTemperatureSetpoint(entity, 21.5, mock.Anything).Return(nil, nil)
		writer := &upstreamRoomHeatingTemperatureWriter{
			client: client,
			reader: &phase4RoomHeatingTemperatureReader{state: writableRoomHeatingSetpoint()},
		}
		if err := writer.Write(context.Background(), entity, 21.5); err == nil || !strings.Contains(err.Error(), "no message counter") {
			t.Fatalf("Write() error = %v, want missing counter", err)
		}
	})

	t.Run("foreign", func(t *testing.T) {
		client := ucmocks.NewCaCRHTInterface(t)
		counter := model.MsgCounterType(54)
		client.EXPECT().WriteRoomAirTemperatureSetpoint(entity, 21.5, mock.Anything).RunAndReturn(
			func(_ spineapi.EntityRemoteInterface, _ float64, callback func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
				callback(model.ResultDataType{}, counter+1)
				return &counter, nil
			},
		)
		writer := &upstreamRoomHeatingTemperatureWriter{
			client: client,
			reader: &phase4RoomHeatingTemperatureReader{state: writableRoomHeatingSetpoint()},
		}
		if err := writer.Write(context.Background(), entity, 21.5); err == nil || !strings.Contains(err.Error(), "unexpected message counter") {
			t.Fatalf("Write() error = %v, want foreign counter", err)
		}
	})
}

func TestUpstreamRoomHeatingTemperatureWriterFailsClosedWhenIncomplete(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHTInterface(t)
	readErr := errors.New("read failed")
	writer := &upstreamRoomHeatingTemperatureWriter{
		client: client,
		reader: &phase4RoomHeatingTemperatureReader{err: readErr},
	}
	if err := writer.Write(context.Background(), entity, 21.5); !errors.Is(err, readErr) {
		t.Fatalf("Write() error = %v, want read failure", err)
	}

	for name, incomplete := range map[string]*upstreamRoomHeatingTemperatureWriter{
		"nil writer": nil,
		"nil client": {reader: &phase4RoomHeatingTemperatureReader{}},
		"nil reader": {client: client},
		"nil entity": {client: client, reader: &phase4RoomHeatingTemperatureReader{}},
	} {
		t.Run(name, func(t *testing.T) {
			gotEntity := spineapi.EntityRemoteInterface(entity)
			if name == "nil entity" {
				gotEntity = nil
			}
			if err := incomplete.Write(context.Background(), gotEntity, 21.5); !errors.Is(err, ErrRoomHeatingDataUnavailable) {
				t.Fatalf("Write() error = %v, want ErrRoomHeatingDataUnavailable", err)
			}
		})
	}
}
