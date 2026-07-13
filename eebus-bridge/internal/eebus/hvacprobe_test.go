package eebus

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/stretchr/testify/mock"
)

// collectLogf returns a threadsafe log collector for probe output.
func collectLogf() (func(format string, args ...any), func() []string) {
	var (
		mu    sync.Mutex
		lines []string
	)
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	get := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(lines))
		copy(out, lines)
		return out
	}
	return logf, get
}

// buildProbeDeviceMock returns a remote-device mock with one DHWCircuit entity
// exposing a Setpoint server feature, mirroring the VR940 dump (entity 4).
func buildProbeDeviceMock(t *testing.T, ski string, setpoint spineapi.FeatureRemoteInterface) *mocks.DeviceRemoteInterface {
	t.Helper()

	entityAddr := &model.EntityAddressType{Entity: []model.AddressEntityType{4}}
	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("Address").Return(entityAddr).Maybe()
	entity.On("EntityType").Return(model.EntityTypeTypeDHWCircuit).Maybe()
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeServer).Return(setpoint).Maybe()
	entity.On("FeatureOfTypeAndRole", model.FeatureTypeTypeHvac, model.RoleTypeServer).Return(nil).Maybe()

	device := mocks.NewDeviceRemoteInterface(t)
	device.On("Ski").Return(ski).Maybe()
	device.On("Entities").Return([]spineapi.EntityRemoteInterface{entity}).Maybe()
	return device
}

func TestHvacProbeInertWithoutSetup(t *testing.T) {
	logf, lines := collectLogf()
	p := NewHvacProbe(logf)

	device := mocks.NewDeviceRemoteInterface(t)
	device.On("Ski").Return("ABCD1234").Maybe()

	p.ProbeOnce("ABCD1234", device)

	if got := lines(); len(got) != 0 {
		t.Errorf("probe without Setup logged %v, want nothing", got)
	}
}

func TestHvacProbeSkipsDeviceWithoutHvacFeatures(t *testing.T) {
	logf, lines := collectLogf()
	p := NewHvacProbe(logf)

	local := mocks.NewEntityLocalInterface(t)
	local.On("GetOrAddFeature", mock.Anything, mock.Anything).Return(nil).Maybe()
	local.On("AddUseCaseSupport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	p.Setup(local)

	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("FeatureOfTypeAndRole", mock.Anything, model.RoleTypeServer).Return(nil).Maybe()
	device := mocks.NewDeviceRemoteInterface(t)
	device.On("Ski").Return("ABCD1234").Maybe()
	device.On("Entities").Return([]spineapi.EntityRemoteInterface{entity}).Maybe()

	p.ProbeOnce("ABCD1234", device)

	if got := lines(); len(got) != 0 {
		t.Errorf("probe on device without Setpoint/HVAC features logged %v, want nothing", got)
	}
	if p.probed["ABCD1234"] {
		t.Error("device without HVAC features must not be marked probed (entities may still be loading)")
	}
}

func TestHvacProbeRequestsAndDedups(t *testing.T) {
	logf, lines := collectLogf()
	p := NewHvacProbe(logf)
	p.pollInterval = 5 * time.Millisecond
	p.pollTimeout = 100 * time.Millisecond

	setpointData := &model.SetpointListDataType{
		SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1))},
		},
	}

	remoteFeature := mocks.NewFeatureRemoteInterface(t)
	// testify renders %v on argument mismatch checks, which calls String().
	remoteFeature.On("String").Return("setpoint-server-feature").Maybe()
	remoteFeature.On("Type").Return(model.FeatureTypeTypeSetpoint).Maybe()
	remoteFeature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{}).Maybe()
	remoteFeature.On("DataCopy", model.FunctionTypeSetpointListData).Return(setpointData).Maybe()
	remoteFeature.On("DataCopy", mock.Anything).Return(nil).Maybe()

	counter := model.MsgCounterType(1)
	localFeature := mocks.NewFeatureLocalInterface(t)
	localFeature.On("RequestRemoteData", mock.Anything, nil, nil, remoteFeature).Return(&counter, nil)

	local := mocks.NewEntityLocalInterface(t)
	local.On("GetOrAddFeature", mock.Anything, mock.Anything).Return(localFeature).Maybe()
	local.On("AddUseCaseSupport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	local.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeClient).Return(localFeature).Maybe()
	p.Setup(local)

	device := buildProbeDeviceMock(t, "ABCD1234", remoteFeature)
	p.ProbeOnce("ABCD1234", device)
	p.ProbeOnce("abcd1234", device) // same SKI, different case -> deduped

	deadline := time.Now().Add(2 * time.Second)
	for {
		out := strings.Join(lines(), "\n")
		if strings.Contains(out, "setpointListData") && strings.Contains(out, `"setpointId":1`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe never logged setpoint data:\n%s", out)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Exactly one operations header despite two ProbeOnce calls -> deduped.
	headers := 0
	for _, l := range lines() {
		if strings.Contains(l, "operations=[") {
			headers++
		}
	}
	if headers != 1 {
		t.Errorf("got %d operations headers, want 1 (dedup by normalized SKI)", headers)
	}
}

func TestHvacProbeBindRequestsAndConfirms(t *testing.T) {
	logf, lines := collectLogf()
	p := NewHvacProbe(logf)
	p.pollInterval = 5 * time.Millisecond
	p.pollTimeout = 200 * time.Millisecond

	remoteAddr := &model.FeatureAddressType{
		Device:  ptr(model.AddressDeviceType("d0")),
		Entity:  []model.AddressEntityType{4},
		Feature: ptr(model.AddressFeatureType(1)),
	}
	remoteFeature := mocks.NewFeatureRemoteInterface(t)
	remoteFeature.On("String").Return("setpoint-server-feature").Maybe()
	remoteFeature.On("Type").Return(model.FeatureTypeTypeSetpoint).Maybe()
	remoteFeature.On("Address").Return(remoteAddr).Maybe()
	remoteFeature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{}).Maybe()
	remoteFeature.On("DataCopy", mock.Anything).Return(nil).Maybe()

	counter := model.MsgCounterType(7)
	var (
		nmCallbackMu sync.Mutex
		nmCallback   func(spineapi.ResponseMessage)
	)
	localFeature := mocks.NewFeatureLocalInterface(t)
	localFeature.On("RequestRemoteData", mock.Anything, nil, nil, remoteFeature).Return(&counter, nil)
	localFeature.On("AddResultCallback", mock.Anything).Return()
	localFeature.On("HasBindingToRemote", remoteAddr).Return(false).Maybe()
	localFeature.On("BindToRemote", remoteAddr).Return(&counter, nil)

	// Bind accept/deny results arrive at the local NodeManagement feature, not
	// the client feature — capture its callback to inject the device's accept.
	nm := mocks.NewNodeManagementInterface(t)
	nm.On("AddResultCallback", mock.Anything).Run(func(args mock.Arguments) {
		nmCallbackMu.Lock()
		nmCallback = args.Get(0).(func(spineapi.ResponseMessage))
		nmCallbackMu.Unlock()
	}).Return()
	deviceLocal := mocks.NewDeviceLocalInterface(t)
	deviceLocal.On("NodeManagement").Return(nm).Maybe()

	local := mocks.NewEntityLocalInterface(t)
	local.On("GetOrAddFeature", mock.Anything, mock.Anything).Return(localFeature).Maybe()
	local.On("AddUseCaseSupport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	local.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeClient).Return(localFeature).Maybe()
	local.On("Device").Return(deviceLocal).Maybe()
	p.Setup(local)
	p.EnableBind()

	device := buildProbeDeviceMock(t, "ABCD1234", remoteFeature)
	p.ProbeOnce("ABCD1234", device)

	// Simulate the VR940 accepting the binding: result errorNumber=0
	// referencing the bind request's msgCounter.
	nmCallbackMu.Lock()
	cb := nmCallback
	nmCallbackMu.Unlock()
	if cb == nil {
		t.Fatal("probe never registered a NodeManagement result callback")
	}
	cb(spineapi.ResponseMessage{
		MsgCounterReference: counter,
		Data:                &model.ResultDataType{ErrorNumber: ptr(model.ErrorNumberType(0))},
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		out := strings.Join(lines(), "\n")
		if strings.Contains(out, "bind Setpoint requested") && strings.Contains(out, "bind Setpoint ACCEPTED") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe never confirmed binding:\n%s", out)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHvacProbeSetupAdvertisesClientUseCases(t *testing.T) {
	p := NewHvacProbe(func(string, ...any) {})

	var (
		mu         sync.Mutex
		advertised []model.UseCaseNameType
	)
	local := mocks.NewEntityLocalInterface(t)
	local.On("GetOrAddFeature", mock.Anything, mock.Anything).Return(nil).Maybe()
	local.On("AddUseCaseSupport",
		model.UseCaseActorTypeConfigurationAppliance,
		mock.Anything, mock.Anything, mock.Anything, true, mock.Anything,
	).Run(func(args mock.Arguments) {
		mu.Lock()
		advertised = append(advertised, args.Get(1).(model.UseCaseNameType))
		mu.Unlock()
	}).Return()

	p.Setup(local)

	want := []model.UseCaseNameType{
		model.UseCaseNameTypeConfigurationOfDhwSystemFunction,
		model.UseCaseNameTypeConfigurationOfDhwTemperature,
		model.UseCaseNameTypeConfigurationOfRoomHeatingSystemFunction,
		model.UseCaseNameTypeConfigurationOfRoomHeatingTemperature,
	}
	mu.Lock()
	defer mu.Unlock()
	if len(advertised) != len(want) {
		t.Fatalf("advertised %d use cases %v, want %d", len(advertised), advertised, len(want))
	}
	for i, name := range want {
		if advertised[i] != name {
			t.Errorf("use case %d = %s, want %s", i, advertised[i], name)
		}
	}
}

func ptr[T any](v T) *T { return &v }
