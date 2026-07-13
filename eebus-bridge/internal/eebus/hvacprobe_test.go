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

func TestHvacProbeEchoWriteAfterBindAccept(t *testing.T) {
	logf, lines := collectLogf()
	p := NewHvacProbe(logf)
	p.pollInterval = 5 * time.Millisecond
	p.pollTimeout = 500 * time.Millisecond
	p.EnableBind()
	p.EnableWrite()

	remoteAddr := &model.FeatureAddressType{
		Device:  ptr(model.AddressDeviceType("d0")),
		Entity:  []model.AddressEntityType{4},
		Feature: ptr(model.AddressFeatureType(1)),
	}
	localAddr := &model.FeatureAddressType{
		Device:  ptr(model.AddressDeviceType("bridge")),
		Entity:  []model.AddressEntityType{1},
		Feature: ptr(model.AddressFeatureType(2)),
	}
	setpointData := &model.SetpointListDataType{
		SetpointData: []model.SetpointDataType{
			{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(50)},
		},
	}

	writeCounter := model.MsgCounterType(9)
	var (
		writeMu  sync.Mutex
		writeCmd *model.CmdType
	)
	sender := mocks.NewSenderInterface(t)
	sender.On("Write", localAddr, remoteAddr, mock.Anything).Run(func(args mock.Arguments) {
		cmd := args.Get(2).(model.CmdType)
		writeMu.Lock()
		writeCmd = &cmd
		writeMu.Unlock()
	}).Return(&writeCounter, nil)
	deviceRemote := mocks.NewDeviceRemoteInterface(t)
	deviceRemote.On("Sender").Return(sender).Maybe()

	remoteFeature := mocks.NewFeatureRemoteInterface(t)
	remoteFeature.On("String").Return("setpoint-server-feature").Maybe()
	remoteFeature.On("Type").Return(model.FeatureTypeTypeSetpoint).Maybe()
	remoteFeature.On("Address").Return(remoteAddr).Maybe()
	remoteFeature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{}).Maybe()
	remoteFeature.On("DataCopy", model.FunctionTypeSetpointListData).Return(setpointData).Maybe()
	remoteFeature.On("DataCopy", mock.Anything).Return(nil).Maybe()
	remoteFeature.On("Device").Return(deviceRemote).Maybe()

	bindCounter := model.MsgCounterType(7)
	var (
		cbMu           sync.Mutex
		nmCallback     func(spineapi.ResponseMessage)
		clientCallback func(spineapi.ResponseMessage)
	)
	localFeature := mocks.NewFeatureLocalInterface(t)
	localFeature.On("RequestRemoteData", mock.Anything, nil, nil, remoteFeature).Return(&bindCounter, nil)
	localFeature.On("AddResultCallback", mock.Anything).Run(func(args mock.Arguments) {
		cbMu.Lock()
		clientCallback = args.Get(0).(func(spineapi.ResponseMessage))
		cbMu.Unlock()
	}).Return()
	localFeature.On("HasBindingToRemote", remoteAddr).Return(false).Maybe()
	localFeature.On("BindToRemote", remoteAddr).Return(&bindCounter, nil)
	localFeature.On("Address").Return(localAddr).Maybe()

	nm := mocks.NewNodeManagementInterface(t)
	nm.On("AddResultCallback", mock.Anything).Run(func(args mock.Arguments) {
		cbMu.Lock()
		nmCallback = args.Get(0).(func(spineapi.ResponseMessage))
		cbMu.Unlock()
	}).Return()
	deviceLocal := mocks.NewDeviceLocalInterface(t)
	deviceLocal.On("NodeManagement").Return(nm).Maybe()

	local := mocks.NewEntityLocalInterface(t)
	local.On("GetOrAddFeature", mock.Anything, mock.Anything).Return(localFeature).Maybe()
	local.On("AddUseCaseSupport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	local.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeClient).Return(localFeature).Maybe()
	local.On("Device").Return(deviceLocal).Maybe()
	p.Setup(local)

	device := buildProbeDeviceMock(t, "ABCD1234", remoteFeature)
	p.ProbeOnce("ABCD1234", device)

	// Device accepts the binding via a NodeManagement result.
	cbMu.Lock()
	cb := nmCallback
	cbMu.Unlock()
	if cb == nil {
		t.Fatal("probe never registered a NodeManagement result callback")
	}
	cb(spineapi.ResponseMessage{
		MsgCounterReference: bindCounter,
		Data:                &model.ResultDataType{ErrorNumber: ptr(model.ErrorNumberType(0))},
	})

	// Wait for the echo write to be sent, then answer it on the client feature.
	deadline := time.Now().Add(2 * time.Second)
	for {
		out := strings.Join(lines(), "\n")
		if strings.Contains(out, "write Setpoint sent") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe never sent the echo write:\n%s", out)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cbMu.Lock()
	ccb := clientCallback
	cbMu.Unlock()
	if ccb == nil {
		t.Fatal("probe never registered a client feature result callback")
	}
	ccb(spineapi.ResponseMessage{
		MsgCounterReference: writeCounter,
		Data:                &model.ResultDataType{ErrorNumber: ptr(model.ErrorNumberType(0))},
	})

	for {
		out := strings.Join(lines(), "\n")
		if strings.Contains(out, "write Setpoint ACCEPTED") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe never confirmed the write:\n%s", out)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The write must echo the device's own data unchanged.
	writeMu.Lock()
	defer writeMu.Unlock()
	if writeCmd == nil || writeCmd.SetpointListData == nil {
		t.Fatal("write command carried no setpointListData")
	}
	got := writeCmd.SetpointListData.SetpointData
	if len(got) != 1 || got[0].SetpointId == nil || *got[0].SetpointId != 1 {
		t.Errorf("echo write data = %+v, want the device's setpointId 1 unchanged", got)
	}
	if got[0].Value == nil || got[0].Value.GetValue() != 50 {
		t.Errorf("echo write value = %+v, want unchanged 50", got[0].Value)
	}
}

// deltaHarness wires the full stage-3b mock scenario: a DHWCircuit whose
// "live" DHW setpoint (id 1, plus a decoy valued setpoint id 0 that is NOT
// dhwTemperature-scoped) is updated by writes and returned by reads.
type deltaHarness struct {
	p          *HvacProbe
	lines      func() []string
	value      func() float64
	senderUsed func() bool
	acceptBind func()
}

// newDeltaHarness builds the scenario. constraints is the DataCopy return for
// SetpointConstraintsListData (nil = device advertises none); ackWrites
// controls whether the device answers writes with a result errorNumber=0.
func newDeltaHarness(t *testing.T, constraints any, ackWrites bool) *deltaHarness {
	t.Helper()
	logf, lines := collectLogf()
	p := NewHvacProbe(logf)
	p.pollInterval = 5 * time.Millisecond
	p.pollTimeout = 300 * time.Millisecond
	p.EnableBind()
	p.EnableWrite()
	p.EnableWriteDelta("ABCD1234")

	remoteAddr := &model.FeatureAddressType{
		Device:  ptr(model.AddressDeviceType("d0")),
		Entity:  []model.AddressEntityType{4},
		Feature: ptr(model.AddressFeatureType(1)),
	}
	localAddr := &model.FeatureAddressType{
		Device:  ptr(model.AddressDeviceType("bridge")),
		Entity:  []model.AddressEntityType{1},
		Feature: ptr(model.AddressFeatureType(2)),
	}
	descriptions := &model.SetpointDescriptionListDataType{
		SetpointDescriptionData: []model.SetpointDescriptionDataType{
			{SetpointId: ptr(model.SetpointIdType(0)), ScopeType: ptr(model.ScopeTypeTypeRoomAirTemperature)},
			{SetpointId: ptr(model.SetpointIdType(1)), ScopeType: ptr(model.ScopeTypeTypeDhwTemperature)},
		},
	}

	var (
		valueMu     sync.Mutex
		deviceValue = float64(46)
		senderCalls int
	)
	writeCounter := model.MsgCounterType(9)
	var (
		cbMu           sync.Mutex
		nmCallback     func(spineapi.ResponseMessage)
		clientCallback func(spineapi.ResponseMessage)
	)
	sender := mocks.NewSenderInterface(t)
	sender.On("Write", localAddr, remoteAddr, mock.Anything).Run(func(args mock.Arguments) {
		cmd := args.Get(2).(model.CmdType)
		valueMu.Lock()
		senderCalls++
		for _, sp := range cmd.SetpointListData.SetpointData {
			if sp.SetpointId != nil && *sp.SetpointId == 1 && sp.Value != nil {
				deviceValue = sp.Value.GetValue()
			}
		}
		valueMu.Unlock()
		if !ackWrites {
			return
		}
		cbMu.Lock()
		ccb := clientCallback
		cbMu.Unlock()
		if ccb != nil {
			go ccb(spineapi.ResponseMessage{
				MsgCounterReference: writeCounter,
				Data:                &model.ResultDataType{ErrorNumber: ptr(model.ErrorNumberType(0))},
			})
		}
	}).Return(&writeCounter, nil).Maybe()
	deviceRemote := mocks.NewDeviceRemoteInterface(t)
	deviceRemote.On("Sender").Return(sender).Maybe()

	writableOp := mocks.NewOperationsInterface(t)
	writableOp.On("Read").Return(true).Maybe()
	writableOp.On("Write").Return(true).Maybe()
	writableOp.On("String").Return("rw").Maybe()

	remoteFeature := mocks.NewFeatureRemoteInterface(t)
	remoteFeature.On("String").Return("setpoint-server-feature").Maybe()
	remoteFeature.On("Type").Return(model.FeatureTypeTypeSetpoint).Maybe()
	remoteFeature.On("Address").Return(remoteAddr).Maybe()
	remoteFeature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		model.FunctionTypeSetpointListData: writableOp,
	}).Maybe()
	remoteFeature.On("DataCopy", model.FunctionTypeSetpointListData).Return(func(model.FunctionType) any {
		valueMu.Lock()
		defer valueMu.Unlock()
		return &model.SetpointListDataType{
			SetpointData: []model.SetpointDataType{
				// Decoy: valued, but its description is not dhwTemperature.
				{SetpointId: ptr(model.SetpointIdType(0)), Value: model.NewScaledNumberType(99)},
				{SetpointId: ptr(model.SetpointIdType(1)), Value: model.NewScaledNumberType(deviceValue)},
			},
		}
	}).Maybe()
	remoteFeature.On("DataCopy", model.FunctionTypeSetpointDescriptionListData).Return(descriptions).Maybe()
	remoteFeature.On("DataCopy", model.FunctionTypeSetpointConstraintsListData).Return(constraints).Maybe()
	remoteFeature.On("DataCopy", mock.Anything).Return(nil).Maybe()
	remoteFeature.On("Device").Return(deviceRemote).Maybe()

	bindCounter := model.MsgCounterType(7)
	localFeature := mocks.NewFeatureLocalInterface(t)
	localFeature.On("RequestRemoteData", mock.Anything, nil, nil, remoteFeature).Return(&bindCounter, nil)
	localFeature.On("AddResultCallback", mock.Anything).Run(func(args mock.Arguments) {
		cbMu.Lock()
		clientCallback = args.Get(0).(func(spineapi.ResponseMessage))
		cbMu.Unlock()
	}).Return()
	localFeature.On("HasBindingToRemote", remoteAddr).Return(false).Maybe()
	localFeature.On("BindToRemote", remoteAddr).Return(&bindCounter, nil)
	localFeature.On("Address").Return(localAddr).Maybe()

	nm := mocks.NewNodeManagementInterface(t)
	nm.On("AddResultCallback", mock.Anything).Run(func(args mock.Arguments) {
		cbMu.Lock()
		nmCallback = args.Get(0).(func(spineapi.ResponseMessage))
		cbMu.Unlock()
	}).Return()
	deviceLocal := mocks.NewDeviceLocalInterface(t)
	deviceLocal.On("NodeManagement").Return(nm).Maybe()

	local := mocks.NewEntityLocalInterface(t)
	local.On("GetOrAddFeature", mock.Anything, mock.Anything).Return(localFeature).Maybe()
	local.On("AddUseCaseSupport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	local.On("FeatureOfTypeAndRole", model.FeatureTypeTypeSetpoint, model.RoleTypeClient).Return(localFeature).Maybe()
	local.On("Device").Return(deviceLocal).Maybe()
	p.Setup(local)

	device := buildProbeDeviceMock(t, "ABCD1234", remoteFeature)
	p.ProbeOnce("ABCD1234", device)

	return &deltaHarness{
		p:     p,
		lines: lines,
		value: func() float64 {
			valueMu.Lock()
			defer valueMu.Unlock()
			return deviceValue
		},
		senderUsed: func() bool {
			valueMu.Lock()
			defer valueMu.Unlock()
			return senderCalls > 0
		},
		acceptBind: func() {
			cbMu.Lock()
			cb := nmCallback
			cbMu.Unlock()
			if cb == nil {
				t.Fatal("probe never registered a NodeManagement result callback")
			}
			cb(spineapi.ResponseMessage{
				MsgCounterReference: bindCounter,
				Data:                &model.ResultDataType{ErrorNumber: ptr(model.ErrorNumberType(0))},
			})
		},
	}
}

func waitForLog(t *testing.T, lines func() []string, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		out := strings.Join(lines(), "\n")
		if strings.Contains(out, want) {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("log never contained %q:\n%s", want, out)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func fullConstraints() *model.SetpointConstraintsListDataType {
	return &model.SetpointConstraintsListDataType{
		SetpointConstraintsData: []model.SetpointConstraintsDataType{{
			SetpointId:       ptr(model.SetpointIdType(1)),
			SetpointRangeMin: model.NewScaledNumberType(35),
			SetpointRangeMax: model.NewScaledNumberType(70),
			SetpointStepSize: model.NewScaledNumberType(1),
		}},
	}
}

func TestHvacProbeDeltaWriteAppliesAndRestores(t *testing.T) {
	h := newDeltaHarness(t, fullConstraints(), true)
	h.acceptBind()

	out := waitForLog(t, h.lines, "RESTORED to 46", 3*time.Second)
	for _, want := range []string{
		"DELTA TEST setpointId=1: 46 -> 47",
		"delta-write Setpoint ACCEPTED",
		"APPLIED: device now reports 47",
		"restore Setpoint ACCEPTED",
		"DELTA TEST complete: setpointId=1 RESTORED to 46",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing log %q in:\n%s", want, out)
		}
	}
	if got := h.value(); got != 46 {
		t.Errorf("device value after test = %v, want restored 46", got)
	}
}

func TestHvacProbeDeltaRestoresWhenResultLost(t *testing.T) {
	// Device applies writes but its results never arrive: the probe must
	// still confirm via re-read and restore the original value.
	h := newDeltaHarness(t, fullConstraints(), false)
	h.acceptBind()

	out := waitForLog(t, h.lines, "RESTORED to 46", 5*time.Second)
	for _, want := range []string{
		"delta-write Setpoint NOT answered",
		"delta-write result not seen; confirming and restoring anyway",
		"APPLIED: device now reports 47",
		"DELTA TEST complete: setpointId=1 RESTORED to 46",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing log %q in:\n%s", want, out)
		}
	}
	if got := h.value(); got != 46 {
		t.Errorf("device value after test = %v, want restored 46", got)
	}
}

func TestHvacProbeDeltaFailsClosedWithoutConstraints(t *testing.T) {
	h := newDeltaHarness(t, nil, true)
	h.acceptBind()

	waitForLog(t, h.lines, "delta test skipped: setpointId=1 has incomplete constraints", 3*time.Second)
	if h.senderUsed() {
		t.Error("probe sent a write despite missing constraints — must fail closed")
	}
	if got := h.value(); got != 46 {
		t.Errorf("device value = %v, want untouched 46", got)
	}
}

func TestHvacProbeAdvertisesClientUseCasesOnlyWithBind(t *testing.T) {
	var (
		mu         sync.Mutex
		advertised []model.UseCaseNameType
	)
	newLocal := func() *mocks.EntityLocalInterface {
		local := mocks.NewEntityLocalInterface(t)
		local.On("GetOrAddFeature", mock.Anything, mock.Anything).Return(nil).Maybe()
		local.On("AddUseCaseSupport",
			model.UseCaseActorTypeConfigurationAppliance,
			mock.Anything, mock.Anything, mock.Anything, true, mock.Anything,
		).Run(func(args mock.Arguments) {
			mu.Lock()
			advertised = append(advertised, args.Get(1).(model.UseCaseNameType))
			mu.Unlock()
		}).Return().Maybe()
		return local
	}

	// Read-only stage 1: Setup without EnableBind must not claim any use case.
	p := NewHvacProbe(func(string, ...any) {})
	p.Setup(newLocal())
	mu.Lock()
	if len(advertised) != 0 {
		t.Fatalf("read-only probe advertised %v, want none", advertised)
	}
	mu.Unlock()

	// Stage 2: EnableBind after Setup advertises all four, exactly once.
	p.EnableBind()
	p.EnableBind() // idempotent

	want := []model.UseCaseNameType{
		model.UseCaseNameTypeConfigurationOfDhwSystemFunction,
		model.UseCaseNameTypeConfigurationOfDhwTemperature,
		model.UseCaseNameTypeConfigurationOfRoomHeatingSystemFunction,
		model.UseCaseNameTypeConfigurationOfRoomHeatingTemperature,
	}
	mu.Lock()
	if len(advertised) != len(want) {
		t.Fatalf("advertised %d use cases %v, want %d", len(advertised), advertised, len(want))
	}
	for i, name := range want {
		if advertised[i] != name {
			t.Errorf("use case %d = %s, want %s", i, advertised[i], name)
		}
	}
	advertised = nil
	mu.Unlock()

	// Reverse order (EnableBind before Setup) also advertises exactly once.
	p2 := NewHvacProbe(func(string, ...any) {})
	p2.EnableBind()
	p2.Setup(newLocal())
	mu.Lock()
	defer mu.Unlock()
	if len(advertised) != len(want) {
		t.Errorf("bind-before-Setup advertised %d use cases %v, want %d", len(advertised), advertised, len(want))
	}
}

func ptr[T any](v T) *T { return &v }
