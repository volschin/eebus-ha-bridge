package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	ucmocks "github.com/enbility/eebus-go/usecases/mocks"
	spineapi "github.com/enbility/spine-go/api"
	spinemocks "github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/stretchr/testify/mock"
)

func TestUpstreamDHWOperationModeWriterWritesAndRefreshes(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCDSFInterface(t)
	counter := model.MsgCounterType(21)
	client.EXPECT().WriteOperationMode(entity, ucapi.HvacOperationModeTypeEco, mock.Anything).RunAndReturn(
		func(_ spineapi.EntityRemoteInterface, _ ucapi.HvacOperationModeType, callback func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
			callback(model.ResultDataType{}, counter)
			return &counter, nil
		},
	)

	refreshes := 0
	writer := &upstreamDHWOperationModeWriter{
		client: client,
		inspector: facadeCapabilityInspector{state: DHWSystemFunctionState{
			ModeWritable:   true,
			AvailableModes: []string{"auto", "eco", "off"},
		}},
		request: func(gotEntity spineapi.EntityRemoteInterface, function model.FunctionType) {
			refreshes++
			if gotEntity != entity || function != model.FunctionTypeHvacSystemFunctionListData {
				t.Fatalf("refresh = (%v, %s), want selected entity and HvacSystemFunctionListData", gotEntity, function)
			}
		},
	}

	if err := writer.WriteOperationMode(context.Background(), entity, "eco"); err != nil {
		t.Fatalf("WriteOperationMode() error = %v", err)
	}
	if refreshes != 1 {
		t.Fatalf("refresh count = %d, want one after accepted write", refreshes)
	}
}

func TestUpstreamDHWOperationModeWriterPrevalidatesRelationSafeModes(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCDSFInterface(t)
	writer := &upstreamDHWOperationModeWriter{
		client: client,
		inspector: facadeCapabilityInspector{state: DHWSystemFunctionState{
			ModeWritable:   true,
			AvailableModes: []string{"auto", "eco"},
		}},
	}

	if err := writer.WriteOperationMode(context.Background(), entity, "off"); !errors.Is(err, ErrDHWSysFnInvalidMode) {
		t.Fatalf("unrelated WriteOperationMode() error = %v, want ErrDHWSysFnInvalidMode", err)
	}

	writer.inspector = facadeCapabilityInspector{state: DHWSystemFunctionState{
		ModeWritable:   true,
		AvailableModes: []string{"auto", "eco", "eco"},
	}}
	if err := writer.WriteOperationMode(context.Background(), entity, "eco"); !errors.Is(err, ErrDHWSysFnDataUnavailable) {
		t.Fatalf("ambiguous WriteOperationMode() error = %v, want ErrDHWSysFnDataUnavailable", err)
	}
}

func TestUpstreamDHWOperationModeWriterReturnsDeviceRejectionWithoutRefresh(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCDSFInterface(t)
	counter := model.MsgCounterType(22)
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

	refreshes := 0
	writer := &upstreamDHWOperationModeWriter{
		client: client,
		inspector: facadeCapabilityInspector{state: DHWSystemFunctionState{
			ModeWritable: true, AvailableModes: []string{"off"},
		}},
		request: func(spineapi.EntityRemoteInterface, model.FunctionType) { refreshes++ },
	}
	err := writer.WriteOperationMode(context.Background(), entity, "off")
	if !errors.Is(err, ErrDHWSysFnRejected) || !strings.Contains(err.Error(), string(description)) {
		t.Fatalf("WriteOperationMode() error = %v, want device rejection", err)
	}
	if refreshes != 0 {
		t.Fatalf("refresh count = %d, want zero after rejection", refreshes)
	}
}

func TestUpstreamDHWOperationModeWriterHonoursCancellationWithoutRefresh(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCDSFInterface(t)
	counter := model.MsgCounterType(23)
	client.EXPECT().WriteOperationMode(entity, ucapi.HvacOperationModeTypeAuto, mock.Anything).Return(&counter, nil)

	refreshes := 0
	writer := &upstreamDHWOperationModeWriter{
		client: client,
		inspector: facadeCapabilityInspector{state: DHWSystemFunctionState{
			ModeWritable: true, AvailableModes: []string{"auto"},
		}},
		request: func(spineapi.EntityRemoteInterface, model.FunctionType) { refreshes++ },
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writer.WriteOperationMode(ctx, entity, "auto"); !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteOperationMode() error = %v, want context.Canceled", err)
	}
	if refreshes != 0 {
		t.Fatalf("refresh count = %d, want zero after cancellation", refreshes)
	}
}

func TestUpstreamDHWOperationModeWriterMapsSendFailuresWithoutFallbackOrRefresh(t *testing.T) {
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
			client.EXPECT().WriteOperationMode(entity, ucapi.HvacOperationModeTypeOn, mock.Anything).Return(nil, test.err).Once()
			refreshes := 0
			writer := &upstreamDHWOperationModeWriter{
				client: client,
				inspector: facadeCapabilityInspector{state: DHWSystemFunctionState{
					ModeWritable: true, AvailableModes: []string{"on"},
				}},
				request: func(spineapi.EntityRemoteInterface, model.FunctionType) { refreshes++ },
			}

			if err := writer.WriteOperationMode(context.Background(), entity, "on"); !errors.Is(err, test.want) {
				t.Fatalf("WriteOperationMode() error = %v, want %v", err, test.want)
			}
			if refreshes != 0 {
				t.Fatalf("refresh count = %d, want zero after send failure", refreshes)
			}
		})
	}
}

func TestUpstreamDHWOperationModeWriterFailsClosedBeforeSending(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	client := ucmocks.NewCaCDSFInterface(t)
	writer := &upstreamDHWOperationModeWriter{
		client:    client,
		inspector: facadeCapabilityInspector{state: DHWSystemFunctionState{ModeWritable: false}},
	}
	if err := writer.WriteOperationMode(context.Background(), entity, "auto"); !errors.Is(err, ErrDHWSysFnNotWritable) {
		t.Fatalf("WriteOperationMode() error = %v, want ErrDHWSysFnNotWritable", err)
	}

	inspectionErr := errors.New("inspection failed")
	writer.inspector = facadeCapabilityInspector{err: inspectionErr}
	if err := writer.WriteOperationMode(context.Background(), entity, "auto"); !errors.Is(err, inspectionErr) {
		t.Fatalf("WriteOperationMode() inspection error = %v, want %v", err, inspectionErr)
	}

	for name, incomplete := range map[string]*upstreamDHWOperationModeWriter{
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
			if err := incomplete.WriteOperationMode(context.Background(), gotEntity, "auto"); !errors.Is(err, ErrDHWSysFnDataUnavailable) {
				t.Fatalf("WriteOperationMode() error = %v, want ErrDHWSysFnDataUnavailable", err)
			}
		})
	}
}
