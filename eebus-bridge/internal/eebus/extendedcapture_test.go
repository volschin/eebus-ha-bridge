package eebus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/mocks"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/spine"
	"github.com/stretchr/testify/mock"
)

func TestExtendedCaptureReadsAdvertisedFunctionsAndRedactsArtifacts(t *testing.T) {
	outputDir := t.TempDir()
	capture := NewExtendedCapture(func(string, ...any) {})
	capture.pollInterval = 5 * time.Millisecond
	capture.pollTimeout = 500 * time.Millisecond
	capture.now = func() time.Time {
		return time.Date(2026, 7, 13, 12, 0, 0, 123, time.UTC)
	}

	localFeature := mocks.NewFeatureLocalInterface(t)
	local := mocks.NewEntityLocalInterface(t)
	local.On("GetOrAddFeature", mock.Anything, model.RoleTypeClient).Return(localFeature).Maybe()
	local.On("FeatureOfTypeAndRole", model.FeatureTypeTypeDeviceClassification, model.RoleTypeClient).Return(localFeature)
	capture.Setup(local, outputDir)

	readOperation := spine.NewOperations(true, true, false, false)
	writeOperation := spine.NewOperations(false, false, true, true)
	manufacturerFunction := model.FunctionTypeDeviceClassificationManufacturerData
	userFunction := model.FunctionTypeDeviceClassificationUserData
	featureAddress := &model.FeatureAddressType{
		Device:  ptr(model.AddressDeviceType("remote-device-address")),
		Entity:  []model.AddressEntityType{0},
		Feature: ptr(model.AddressFeatureType(1)),
	}
	remoteFeature := mocks.NewFeatureRemoteInterface(t)
	remoteFeature.On("String").Return("device-classification-server").Maybe()
	remoteFeature.On("Address").Return(featureAddress).Maybe()
	remoteFeature.On("Type").Return(model.FeatureTypeTypeDeviceClassification).Maybe()
	remoteFeature.On("Role").Return(model.RoleTypeServer).Maybe()
	remoteFeature.On("Description").Return((*model.DescriptionType)(nil)).Maybe()
	remoteFeature.On("Operations").Return(map[model.FunctionType]spineapi.OperationsInterface{
		manufacturerFunction: readOperation,
		userFunction:         writeOperation,
	}).Maybe()
	remoteFeature.On("DataCopy", mock.Anything).Return(nil).Maybe()

	entity := mocks.NewEntityRemoteInterface(t)
	entity.On("Address").Return(&model.EntityAddressType{
		Device: ptr(model.AddressDeviceType("remote-device-address")),
		Entity: []model.AddressEntityType{0},
	}).Maybe()
	entity.On("EntityType").Return(model.EntityTypeTypeDeviceInformation).Maybe()
	entity.On("Features").Return([]spineapi.FeatureRemoteInterface{remoteFeature}).Maybe()

	deviceType := model.DeviceTypeTypeGeneric
	device := mocks.NewDeviceRemoteInterface(t)
	device.On("Ski").Return("AA BB CC DD").Maybe()
	device.On("DeviceType").Return(&deviceType).Maybe()
	device.On("UseCases").Return([]model.UseCaseInformationDataType{}).Maybe()
	device.On("Entities").Return([]spineapi.EntityRemoteInterface{entity}).Maybe()

	counter := model.MsgCounterType(42)
	var (
		callbackMu sync.Mutex
		callback   func(spineapi.ResponseMessage)
	)
	localFeature.On("RequestRemoteData", manufacturerFunction, mock.Anything, mock.Anything, mock.Anything).Return(&counter, nil).Once()
	localFeature.On("AddResponseCallback", counter, mock.Anything).Run(func(args mock.Arguments) {
		callbackMu.Lock()
		callback = args.Get(1).(func(spineapi.ResponseMessage))
		callbackMu.Unlock()
	}).Return(nil).Once()

	// An early event before detailed discovery must not consume the once-only
	// slot. The next event with the actual feature map performs the capture.
	emptyDevice := mocks.NewDeviceRemoteInterface(t)
	emptyDevice.On("Ski").Return("AA BB CC DD").Maybe()
	emptyDevice.On("DeviceType").Return(&deviceType).Maybe()
	emptyDevice.On("UseCases").Return([]model.UseCaseInformationDataType{}).Maybe()
	emptyDevice.On("Entities").Return([]spineapi.EntityRemoteInterface{}).Maybe()
	capture.CaptureOnce("AA BB CC DD", emptyDevice)
	capture.CaptureOnce("AA BB CC DD", device)
	capture.CaptureOnce("aabbccdd", device) // normalized duplicate

	callbackMu.Lock()
	responseCallback := callback
	callbackMu.Unlock()
	if responseCallback == nil {
		t.Fatal("capture did not register a response callback")
	}
	responseCallback(spineapi.ResponseMessage{Data: &model.DeviceClassificationManufacturerDataType{
		DeviceName:                     ptr(model.DeviceClassificationStringType("VR940")),
		SerialNumber:                   ptr(model.DeviceClassificationStringType("SECRET-SERIAL")),
		SoftwareRevision:               ptr(model.DeviceClassificationStringType("09.17")),
		ManufacturerNodeIdentification: ptr(model.DeviceClassificationStringType("SECRET-NODE-ID")),
	}})

	jsonPath := waitForCaptureFile(t, outputDir, "*.json")
	textPath := waitForCaptureFile(t, outputDir, "*.txt")
	jsonData, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"AABBCCDD", "AA BB CC DD", "SECRET-SERIAL", "SECRET-NODE-ID", "remote-device-address"} {
		if strings.Contains(string(jsonData), secret) || strings.Contains(filepath.Base(jsonPath), secret) {
			t.Errorf("capture leaked %q: %s", secret, jsonData)
		}
	}
	if !strings.Contains(string(jsonData), `"softwareRevision": "09.17"`) || !strings.Contains(string(jsonData), `"serialNumber": "<redacted>"`) {
		t.Fatalf("capture did not retain useful data while redacting identifiers:\n%s", jsonData)
	}

	var document ExtendedCaptureDocument
	if err := json.Unmarshal(jsonData, &document); err != nil {
		t.Fatalf("unmarshal capture: %v", err)
	}
	functions := document.Entities[0].Features[0].Functions
	if len(functions) != 2 {
		t.Fatalf("functions = %+v, want two advertised functions", functions)
	}
	if functions[0].Name != string(manufacturerFunction) || functions[0].Status != "received" || !functions[0].Operations.Read || !functions[0].Operations.ReadPartial {
		t.Errorf("read function = %+v", functions[0])
	}
	if functions[1].Name != string(userFunction) || functions[1].Status != "not_readable" || !functions[1].Operations.Write || !functions[1].Operations.WritePartial {
		t.Errorf("write-only function = %+v", functions[1])
	}
	if document.Entities[0].Features[0].SubscriptionProbe != extendedCaptureSubscription {
		t.Errorf("subscription probe = %q", document.Entities[0].Features[0].SubscriptionProbe)
	}

	textData, err := os.ReadFile(textPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"subscription=not_attempted_read_only",
		"operations=read:true,readPartial:true,write:false,writePartial:false status=received",
		`"serialNumber":"<redacted>"`,
	} {
		if !strings.Contains(string(textData), want) {
			t.Errorf("text capture missing %q:\n%s", want, textData)
		}
	}
}

func TestMarshalRedactedRemovesNestedIdentifiers(t *testing.T) {
	raw, err := marshalRedacted(map[string]any{
		"ski": "secret-ski",
		"nested": []any{map[string]any{
			"device":                 "secret-device-address",
			"userNodeIdentification": "secret-user-node",
			"label":                  "Heating zone",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-ski", "secret-device-address", "secret-user-node"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("redacted JSON leaked %q: %s", secret, raw)
		}
	}
	if !strings.Contains(string(raw), "Heating zone") {
		t.Errorf("redaction removed non-sensitive diagnostic label: %s", raw)
	}
}

func waitForCaptureFile(t *testing.T, dir, pattern string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == 1 {
			return matches[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %s capture artifact appeared in %s", pattern, dir)
	return ""
}
