package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	ucmocks "github.com/enbility/eebus-go/usecases/mocks"
	spineapi "github.com/enbility/spine-go/api"
	spinemocks "github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/stretchr/testify/mock"
)

func TestUpstreamRoomHeatingOperationModeWriterWritesAndAwaitsResult(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHSFInterface(t)
	client.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{
		ucapi.HvacOperationModeTypeAuto,
		ucapi.HvacOperationModeTypeOn,
		ucapi.HvacOperationModeTypeOff,
	}, nil)
	counter := model.MsgCounterType(41)
	client.EXPECT().WriteOperationMode(entity, ucapi.HvacOperationModeTypeOn, mock.Anything).RunAndReturn(
		func(_ spineapi.EntityRemoteInterface, _ ucapi.HvacOperationModeType, callback func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
			callback(model.ResultDataType{}, counter)
			return &counter, nil
		},
	)
	writer := &upstreamRoomHeatingOperationModeWriter{
		client: client,
		inspector: &phase2RoomHeatingCapabilityInspector{state: RoomHeatingSystemFunctionState{
			ModeWritable: true,
		}},
	}

	if err := writer.WriteOperationMode(context.Background(), entity, "on"); err != nil {
		t.Fatalf("WriteOperationMode() error = %v", err)
	}
}

func TestUpstreamRoomHeatingOperationModeWriterPrevalidatesCRHSFRelation(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHSFInterface(t)
	client.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{
		ucapi.HvacOperationModeTypeAuto,
		ucapi.HvacOperationModeTypeOn,
	}, nil)
	writer := &upstreamRoomHeatingOperationModeWriter{
		client: client,
		inspector: &phase2RoomHeatingCapabilityInspector{state: RoomHeatingSystemFunctionState{
			ModeWritable: true,
		}},
	}

	if err := writer.WriteOperationMode(context.Background(), entity, "off"); !errors.Is(err, ErrRoomHeatingSysFnInvalidMode) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrRoomHeatingSysFnInvalidMode", err)
	}
}

func TestUpstreamRoomHeatingOperationModeWriterReturnsDeviceRejection(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHSFInterface(t)
	client.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{ucapi.HvacOperationModeTypeOff}, nil)
	counter := model.MsgCounterType(42)
	description := model.DescriptionType("mode blocked")
	client.EXPECT().WriteOperationMode(entity, ucapi.HvacOperationModeTypeOff, mock.Anything).RunAndReturn(
		func(_ spineapi.EntityRemoteInterface, _ ucapi.HvacOperationModeType, callback func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
			callback(model.ResultDataType{
				ErrorNumber: ptr(model.ErrorNumberTypeCommandRejected),
				Description: &description,
			}, counter)
			return &counter, nil
		},
	)
	writer := &upstreamRoomHeatingOperationModeWriter{
		client: client,
		inspector: &phase2RoomHeatingCapabilityInspector{state: RoomHeatingSystemFunctionState{
			ModeWritable: true,
		}},
	}

	err := writer.WriteOperationMode(context.Background(), entity, "off")
	if !errors.Is(err, ErrRoomHeatingSysFnRejected) || !strings.Contains(err.Error(), string(description)) {
		t.Fatalf("WriteOperationMode() error = %v, want device rejection", err)
	}
}

func TestUpstreamRoomHeatingOperationModeWriterHonoursCancellation(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHSFInterface(t)
	client.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{ucapi.HvacOperationModeTypeAuto}, nil)
	counter := model.MsgCounterType(43)
	client.EXPECT().WriteOperationMode(entity, ucapi.HvacOperationModeTypeAuto, mock.Anything).Return(&counter, nil)
	writer := &upstreamRoomHeatingOperationModeWriter{
		client: client,
		inspector: &phase2RoomHeatingCapabilityInspector{state: RoomHeatingSystemFunctionState{
			ModeWritable: true,
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := writer.WriteOperationMode(ctx, entity, "auto"); !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteOperationMode() error = %v, want context.Canceled", err)
	}
}

func TestUpstreamRoomHeatingOperationModeWriterMapsSendFailuresWithoutFallback(t *testing.T) {
	sendErr := errors.New("send failed")
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "not writable", err: eebusapi.ErrNotSupported, want: ErrRoomHeatingSysFnNotWritable},
		{name: "cache unavailable", err: eebusapi.ErrDataNotAvailable, want: ErrRoomHeatingSysFnDataUnavailable},
		{name: "disconnected", err: eebusapi.ErrDeviceDisconnected, want: ErrRoomHeatingSysFnDataUnavailable},
		{name: "transport error", err: sendErr, want: sendErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			entity := spinemocks.NewEntityRemoteInterface(t)
			client := ucmocks.NewCaCRHSFInterface(t)
			client.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{ucapi.HvacOperationModeTypeOn}, nil)
			client.EXPECT().WriteOperationMode(entity, ucapi.HvacOperationModeTypeOn, mock.Anything).Return(nil, test.err).Once()
			writer := &upstreamRoomHeatingOperationModeWriter{
				client: client,
				inspector: &phase2RoomHeatingCapabilityInspector{state: RoomHeatingSystemFunctionState{
					ModeWritable: true,
				}},
			}

			if err := writer.WriteOperationMode(context.Background(), entity, "on"); !errors.Is(err, test.want) {
				t.Fatalf("WriteOperationMode() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestUpstreamRoomHeatingOperationModeWriterFailsClosedBeforeSending(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHSFInterface(t)
	writer := &upstreamRoomHeatingOperationModeWriter{
		client:    client,
		inspector: &phase2RoomHeatingCapabilityInspector{},
	}
	if err := writer.WriteOperationMode(context.Background(), entity, "auto"); !errors.Is(err, ErrRoomHeatingSysFnNotWritable) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrRoomHeatingSysFnNotWritable", err)
	}

	inspectionErr := errors.New("inspection failed")
	writer.inspector = &phase2RoomHeatingCapabilityInspector{err: inspectionErr}
	if err := writer.WriteOperationMode(context.Background(), entity, "auto"); !errors.Is(err, inspectionErr) {
		t.Fatalf("WriteOperationMode() inspection error = %v, want %v", err, inspectionErr)
	}

	for name, incomplete := range map[string]*upstreamRoomHeatingOperationModeWriter{
		"nil writer":    nil,
		"nil client":    {inspector: &phase2RoomHeatingCapabilityInspector{}},
		"nil inspector": {client: client},
		"nil entity":    {client: client, inspector: &phase2RoomHeatingCapabilityInspector{}},
	} {
		t.Run(name, func(t *testing.T) {
			var gotEntity spineapi.EntityRemoteInterface = entity
			if name == "nil entity" {
				gotEntity = nil
			}
			if err := incomplete.WriteOperationMode(context.Background(), gotEntity, "auto"); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
				t.Fatalf("WriteOperationMode() error = %v, want ErrRoomHeatingSysFnDataUnavailable", err)
			}
		})
	}
}

func TestUpstreamRoomHeatingOperationModeWriterTimesOut(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHSFInterface(t)
	client.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{ucapi.HvacOperationModeTypeOn}, nil)
	counter := model.MsgCounterType(44)
	client.EXPECT().WriteOperationMode(entity, ucapi.HvacOperationModeTypeOn, mock.Anything).Return(&counter, nil)
	writer := &upstreamRoomHeatingOperationModeWriter{
		client: client,
		inspector: &phase2RoomHeatingCapabilityInspector{state: RoomHeatingSystemFunctionState{
			ModeWritable: true,
		}},
	}
	previousTimeout := roomHeatingSystemFunctionWriteTimeout
	roomHeatingSystemFunctionWriteTimeout = time.Millisecond
	t.Cleanup(func() { roomHeatingSystemFunctionWriteTimeout = previousTimeout })

	err := writer.WriteOperationMode(context.Background(), entity, "on")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("WriteOperationMode() error = %v, want timeout", err)
	}
}

func TestUpstreamRoomHeatingOperationModeWriterMapsRelationLookupError(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHSFInterface(t)
	client.EXPECT().OperationModes(entity).Return(nil, eebusapi.ErrDataNotAvailable)
	writer := &upstreamRoomHeatingOperationModeWriter{
		client: client,
		inspector: &phase2RoomHeatingCapabilityInspector{state: RoomHeatingSystemFunctionState{
			ModeWritable: true,
		}},
	}

	if err := writer.WriteOperationMode(context.Background(), entity, "on"); !errors.Is(err, ErrRoomHeatingSysFnDataUnavailable) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrRoomHeatingSysFnDataUnavailable", err)
	}
}

func TestUpstreamRoomHeatingOperationModeWriterRejectsMissingMessageCounter(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHSFInterface(t)
	client.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{ucapi.HvacOperationModeTypeOn}, nil)
	client.EXPECT().WriteOperationMode(entity, ucapi.HvacOperationModeTypeOn, mock.Anything).Return(nil, nil)
	writer := &upstreamRoomHeatingOperationModeWriter{
		client: client,
		inspector: &phase2RoomHeatingCapabilityInspector{state: RoomHeatingSystemFunctionState{
			ModeWritable: true,
		}},
	}

	err := writer.WriteOperationMode(context.Background(), entity, "on")
	if err == nil || !strings.Contains(err.Error(), "no message counter") {
		t.Fatalf("WriteOperationMode() error = %v, want missing message counter", err)
	}
}

func TestUpstreamRoomHeatingOperationModeWriterRejectsForeignMessageCounter(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCRHSFInterface(t)
	client.EXPECT().OperationModes(entity).Return([]ucapi.HvacOperationModeType{ucapi.HvacOperationModeTypeOn}, nil)
	counter := model.MsgCounterType(45)
	client.EXPECT().WriteOperationMode(entity, ucapi.HvacOperationModeTypeOn, mock.Anything).RunAndReturn(
		func(_ spineapi.EntityRemoteInterface, _ ucapi.HvacOperationModeType, callback func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
			callback(model.ResultDataType{}, model.MsgCounterType(99))
			return &counter, nil
		},
	)
	writer := &upstreamRoomHeatingOperationModeWriter{
		client: client,
		inspector: &phase2RoomHeatingCapabilityInspector{state: RoomHeatingSystemFunctionState{
			ModeWritable: true,
		}},
	}

	err := writer.WriteOperationMode(context.Background(), entity, "on")
	if err == nil || !strings.Contains(err.Error(), "unexpected message counter") {
		t.Fatalf("WriteOperationMode() error = %v, want unexpected message counter", err)
	}
}
