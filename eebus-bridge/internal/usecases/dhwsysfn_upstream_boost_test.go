package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"

	eebusapi "github.com/enbility/eebus-go/api"
	ucmocks "github.com/enbility/eebus-go/usecases/mocks"
	spineapi "github.com/enbility/spine-go/api"
	spinemocks "github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/stretchr/testify/mock"
)

func TestUpstreamCDSFSelectsUpstreamWriteStrategies(t *testing.T) {
	client := ucmocks.NewCaCDSFInterface(t)
	facade := newUpstreamDHWSystemFunctionConfiguration(client)

	if _, ok := facade.boostWriter.(*upstreamDHWBoostWriter); !ok {
		t.Fatalf("boost writer = %T, want *upstreamDHWBoostWriter", facade.boostWriter)
	}
	if _, ok := facade.operationModeWriter.(*upstreamDHWOperationModeWriter); !ok {
		t.Fatalf("operation-mode writer = %T, want *upstreamDHWOperationModeWriter", facade.operationModeWriter)
	}
}

func TestUpstreamDHWBoostWriterStartsStopsAndAwaitsResults(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCDSFInterface(t)
	counter := model.MsgCounterType(17)
	client.EXPECT().StartOneTimeDhw(entity, mock.Anything).RunAndReturn(
		func(_ spineapi.EntityRemoteInterface, callback func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
			callback(model.ResultDataType{}, counter)
			return &counter, nil
		},
	)
	client.EXPECT().StopOneTimeDhw(entity, mock.Anything).RunAndReturn(
		func(_ spineapi.EntityRemoteInterface, callback func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
			callback(model.ResultDataType{}, counter)
			return &counter, nil
		},
	)

	writer := &upstreamDHWBoostWriter{
		client:    client,
		inspector: facadeCapabilityInspector{state: DHWSystemFunctionState{BoostWritable: true}},
	}

	if err := writer.WriteBoost(context.Background(), entity, true); err != nil {
		t.Fatalf("start WriteBoost() error = %v", err)
	}
	if err := writer.WriteBoost(context.Background(), entity, false); err != nil {
		t.Fatalf("stop WriteBoost() error = %v", err)
	}
}

func TestUpstreamDHWBoostWriterReturnsDeviceRejection(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCDSFInterface(t)
	counter := model.MsgCounterType(18)
	description := model.DescriptionType("boost blocked")
	client.EXPECT().StartOneTimeDhw(entity, mock.Anything).RunAndReturn(
		func(_ spineapi.EntityRemoteInterface, callback func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
			callback(model.ResultDataType{
				ErrorNumber: ptr(model.ErrorNumberTypeCommandRejected),
				Description: &description,
			}, counter)
			return &counter, nil
		},
	)

	writer := &upstreamDHWBoostWriter{
		client:    client,
		inspector: facadeCapabilityInspector{state: DHWSystemFunctionState{BoostWritable: true}},
	}
	err := writer.WriteBoost(context.Background(), entity, true)
	if !errors.Is(err, ErrDHWSysFnRejected) || !strings.Contains(err.Error(), string(description)) {
		t.Fatalf("WriteBoost() error = %v, want device rejection", err)
	}
}

func TestUpstreamDHWBoostWriterHonoursCancellation(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCDSFInterface(t)
	counter := model.MsgCounterType(19)
	client.EXPECT().StartOneTimeDhw(entity, mock.Anything).Return(&counter, nil)

	writer := &upstreamDHWBoostWriter{
		client:    client,
		inspector: facadeCapabilityInspector{state: DHWSystemFunctionState{BoostWritable: true}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writer.WriteBoost(ctx, entity, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteBoost() error = %v, want context.Canceled", err)
	}
}

func TestUpstreamDHWBoostWriterMapsSendFailuresWithoutFallback(t *testing.T) {
	sendErr := errors.New("send failed")
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "not writable", err: eebusapi.ErrNotSupported, want: ErrDHWSysFnNotWritable},
		{name: "cache unavailable", err: eebusapi.ErrDataNotAvailable, want: ErrDHWSysFnDataUnavailable},
		{name: "transport error", err: sendErr, want: sendErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			entity := spinemocks.NewEntityRemoteInterface(t)
			client := ucmocks.NewCaCDSFInterface(t)
			client.EXPECT().StartOneTimeDhw(entity, mock.Anything).Return(nil, test.err).Once()
			writer := &upstreamDHWBoostWriter{
				client:    client,
				inspector: facadeCapabilityInspector{state: DHWSystemFunctionState{BoostWritable: true}},
			}

			if err := writer.WriteBoost(context.Background(), entity, true); !errors.Is(err, test.want) {
				t.Fatalf("WriteBoost() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestUpstreamDHWBoostWriterFailsClosedBeforeSending(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCDSFInterface(t)
	writer := &upstreamDHWBoostWriter{
		client:    client,
		inspector: facadeCapabilityInspector{state: DHWSystemFunctionState{BoostWritable: false}},
	}
	if err := writer.WriteBoost(context.Background(), entity, true); !errors.Is(err, ErrDHWSysFnNotWritable) {
		t.Fatalf("WriteBoost() error = %v, want ErrDHWSysFnNotWritable", err)
	}

	inspectionErr := errors.New("inspection failed")
	writer.inspector = facadeCapabilityInspector{err: inspectionErr}
	if err := writer.WriteBoost(context.Background(), entity, true); !errors.Is(err, inspectionErr) {
		t.Fatalf("WriteBoost() inspection error = %v, want %v", err, inspectionErr)
	}

	for name, incomplete := range map[string]*upstreamDHWBoostWriter{
		"nil writer":    nil,
		"nil client":    {inspector: facadeCapabilityInspector{}},
		"nil inspector": {client: client},
		"nil entity":    {client: client, inspector: facadeCapabilityInspector{}},
	} {
		t.Run(name, func(t *testing.T) {
			var gotEntity spineapi.EntityRemoteInterface = entity
			if name == "nil entity" {
				gotEntity = nil
			}
			if err := incomplete.WriteBoost(context.Background(), gotEntity, true); !errors.Is(err, ErrDHWSysFnDataUnavailable) {
				t.Fatalf("WriteBoost() error = %v, want ErrDHWSysFnDataUnavailable", err)
			}
		})
	}
}
