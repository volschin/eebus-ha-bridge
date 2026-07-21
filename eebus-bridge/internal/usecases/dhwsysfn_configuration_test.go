package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	spinemocks "github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/stretchr/testify/mock"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

func TestAwaitDHWWriteHandlesSynchronousAndAsynchronousCallbacks(t *testing.T) {
	counter := model.MsgCounterType(7)
	for _, test := range []struct {
		name string
		call func(dhwResultCallback)
	}{
		{
			name: "synchronous",
			call: func(callback dhwResultCallback) {
				callback(model.ResultDataType{ErrorNumber: ptr(model.ErrorNumberTypeNoError)}, counter)
			},
		},
		{
			name: "asynchronous",
			call: func(callback dhwResultCallback) {
				go callback(model.ResultDataType{ErrorNumber: ptr(model.ErrorNumberTypeNoError)}, counter)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := awaitDHWWrite(context.Background(), "DHW test", func(callback dhwResultCallback) (*model.MsgCounterType, error) {
				test.call(callback)
				return &counter, nil
			})
			if err != nil {
				t.Fatalf("awaitDHWWrite() error = %v", err)
			}
		})
	}
}

func TestAwaitDHWWriteReportsRejectionAndSendFailure(t *testing.T) {
	counter := model.MsgCounterType(7)
	description := model.DescriptionType("not commissioned")
	err := awaitDHWWrite(context.Background(), "DHW boost", func(callback dhwResultCallback) (*model.MsgCounterType, error) {
		callback(model.ResultDataType{
			ErrorNumber: ptr(model.ErrorNumberTypeCommandRejected),
			Description: &description,
		}, counter)
		return &counter, nil
	})
	if !errors.Is(err, ErrDHWSysFnRejected) || !strings.Contains(err.Error(), string(description)) {
		t.Fatalf("awaitDHWWrite() rejection = %v", err)
	}

	sendErr := errors.New("send failed")
	err = awaitDHWWrite(context.Background(), "DHW boost", func(dhwResultCallback) (*model.MsgCounterType, error) {
		return nil, sendErr
	})
	if !errors.Is(err, sendErr) {
		t.Fatalf("awaitDHWWrite() send error = %v, want %v", err, sendErr)
	}
}

func TestAwaitDHWWriteHonoursCancellationAndTimeout(t *testing.T) {
	counter := model.MsgCounterType(7)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := awaitDHWWrite(ctx, "DHW boost", func(dhwResultCallback) (*model.MsgCounterType, error) {
		return &counter, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("awaitDHWWrite() cancellation = %v, want context.Canceled", err)
	}

	previousTimeout := dhwSystemFunctionWriteTimeout
	dhwSystemFunctionWriteTimeout = 10 * time.Millisecond
	t.Cleanup(func() { dhwSystemFunctionWriteTimeout = previousTimeout })
	err = awaitDHWWrite(context.Background(), "DHW boost", func(dhwResultCallback) (*model.MsgCounterType, error) {
		return &counter, nil
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("awaitDHWWrite() timeout = %v", err)
	}
}

func TestAwaitDHWWriteRejectsMissingMessageCounter(t *testing.T) {
	err := awaitDHWWrite(context.Background(), "DHW boost", func(dhwResultCallback) (*model.MsgCounterType, error) {
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "no message counter") {
		t.Fatalf("awaitDHWWrite() missing counter = %v", err)
	}
}

func TestAwaitDHWWriteRejectsMismatchedMessageCounter(t *testing.T) {
	counter := model.MsgCounterType(7)
	err := awaitDHWWrite(context.Background(), "DHW boost", func(callback dhwResultCallback) (*model.MsgCounterType, error) {
		callback(model.ResultDataType{}, counter+1)
		return &counter, nil
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected message counter") {
		t.Fatalf("awaitDHWWrite() mismatched counter = %v", err)
	}
}

type facadeEntityResolver struct {
	entity spineapi.EntityRemoteInterface
}

func (r facadeEntityResolver) CompatibleEntity(string) eebus.EntityResolution {
	return eebus.EntityResolution{Entity: r.entity, DeviceCount: 1}
}

type facadeCapabilityInspector struct {
	state DHWSystemFunctionState
}

func (i facadeCapabilityInspector) State(spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error) {
	return i.state, nil
}

type facadeBoostWriter struct {
	active *bool
	calls  int
	err    error
}

func (w *facadeBoostWriter) WriteBoost(_ context.Context, _ spineapi.EntityRemoteInterface, active bool) error {
	w.active = &active
	w.calls++
	return w.err
}

type facadeModeWriter struct {
	mode  string
	calls int
}

func (w *facadeModeWriter) WriteOperationMode(_ context.Context, _ spineapi.EntityRemoteInterface, mode string) error {
	w.mode = mode
	w.calls++
	return nil
}

func TestCDSFConfigurationFacadeSelectsWritersIndependently(t *testing.T) {
	entity := spinemocks.NewEntityRemoteInterface(t)
	boost := &facadeBoostWriter{}
	mode := &facadeModeWriter{}
	facade := newCDSFConfigurationFacade(
		facadeEntityResolver{entity: entity},
		facadeCapabilityInspector{state: DHWSystemFunctionState{BoostWritable: true, ModeWritable: true}},
		boost,
		mode,
	)

	if facade.CompatibleEntity("ab:cd").Entity != entity {
		t.Fatal("CompatibleEntity() did not delegate to the selected resolver")
	}
	state, err := facade.State(entity)
	if err != nil || !state.BoostWritable || !state.ModeWritable {
		t.Fatalf("State() = %+v, %v", state, err)
	}
	if err := facade.WriteBoost(context.Background(), entity, true); err != nil {
		t.Fatalf("WriteBoost() error = %v", err)
	}
	if err := facade.WriteOperationMode(context.Background(), entity, "off"); err != nil {
		t.Fatalf("WriteOperationMode() error = %v", err)
	}
	if boost.active == nil || !*boost.active || mode.mode != "off" {
		t.Fatalf("selected writers received boost=%v mode=%q", boost.active, mode.mode)
	}
}

func TestCDSFConfigurationFacadeDoesNotFallbackBetweenWriters(t *testing.T) {
	writeErr := errors.New("selected boost writer failed")
	boost := &facadeBoostWriter{err: writeErr}
	mode := &facadeModeWriter{}
	facade := newCDSFConfigurationFacade(nil, nil, boost, mode)

	err := facade.WriteBoost(context.Background(), nil, true)
	if !errors.Is(err, writeErr) {
		t.Fatalf("WriteBoost() error = %v, want %v", err, writeErr)
	}
	if boost.calls != 1 || mode.calls != 0 {
		t.Fatalf("writer calls = boost %d, mode %d; want 1, 0", boost.calls, mode.calls)
	}
}

func TestLegacyDHWWriterRefreshesAcceptedWriteExactlyOnce(t *testing.T) {
	feature := dhwSysFnFeature(t, true, true, nil)
	localEntity, entity, _ := dhwSysFnWriteHarness(t, feature)
	local := localEntity.FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeClient)
	refreshCount := 0
	writer := &legacyDHWSystemFunctionWriter{
		localHvacFeature: func() spineapi.FeatureLocalInterface { return local },
		request: func(gotEntity spineapi.EntityRemoteInterface, function model.FunctionType) {
			refreshCount++
			if gotEntity != entity || function != model.FunctionTypeHvacOverrunListData {
				t.Fatalf("refresh = (%v, %s), want selected entity and HvacOverrunListData", gotEntity, function)
			}
		},
		inspector: cachedDHWSystemFunctionCapabilityInspector{},
	}

	if err := writer.WriteBoost(context.Background(), entity, true); err != nil {
		t.Fatalf("WriteBoost() error = %v", err)
	}
	if refreshCount != 1 {
		t.Fatalf("refresh count = %d, want 1", refreshCount)
	}
}

func TestLegacyDHWWriterReportsCallbackRegistrationFailureWithoutRefresh(t *testing.T) {
	feature := dhwSysFnFeature(t, true, true, nil)
	localAddress := &model.FeatureAddressType{}
	registrationErr := errors.New("callback registration failed")
	local := spinemocks.NewFeatureLocalInterface(t)
	local.EXPECT().Address().Return(localAddress)
	local.EXPECT().AddResponseCallback(mock.Anything, mock.Anything).Return(registrationErr)

	counter := model.MsgCounterType(9)
	sender := spinemocks.NewSenderInterface(t)
	sender.EXPECT().Write(localAddress, mock.Anything, mock.Anything).Return(&counter, nil)
	device := spinemocks.NewDeviceRemoteInterface(t)
	device.EXPECT().Sender().Return(sender)
	entity := spinemocks.NewEntityRemoteInterface(t)
	entity.EXPECT().FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(feature).Times(2)
	entity.EXPECT().Device().Return(device)
	refreshCount := 0
	writer := &legacyDHWSystemFunctionWriter{
		localHvacFeature: func() spineapi.FeatureLocalInterface { return local },
		request: func(spineapi.EntityRemoteInterface, model.FunctionType) {
			refreshCount++
		},
		inspector: cachedDHWSystemFunctionCapabilityInspector{},
	}

	err := writer.WriteBoost(context.Background(), entity, true)
	if !errors.Is(err, registrationErr) {
		t.Fatalf("WriteBoost() error = %v, want %v", err, registrationErr)
	}
	if refreshCount != 0 {
		t.Fatalf("refresh count = %d, want 0", refreshCount)
	}
}
