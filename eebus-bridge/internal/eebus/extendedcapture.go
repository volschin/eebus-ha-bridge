package eebus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

const (
	extendedCaptureSchemaVersion = 1
	extendedCaptureSubscription  = "not_attempted_read_only"
)

// ExtendedCapture is an opt-in, read-only SPINE diagnostic. It records the
// complete discovered feature/function map and actively requests every readable
// function in the feature families relevant to VR940 diagnostics. It never
// binds, subscribes or writes.
type ExtendedCapture struct {
	mu           sync.Mutex
	localEntity  spineapi.EntityLocalInterface
	outputDir    string
	captured     map[string]bool
	logf         func(format string, args ...any)
	pollInterval time.Duration
	pollTimeout  time.Duration
	now          func() time.Time
}

// ExtendedCaptureDocument is the canonical JSON artifact written by the
// capture. Slice ordering is normalized before serialization so captures are
// suitable for review and diffing.
type ExtendedCaptureDocument struct {
	SchemaVersion int                     `json:"schemaVersion"`
	CapturedAt    time.Time               `json:"capturedAt"`
	Device        ExtendedCaptureDevice   `json:"device"`
	UseCases      json.RawMessage         `json:"useCases,omitempty"`
	Entities      []ExtendedCaptureEntity `json:"entities"`
}

type ExtendedCaptureDevice struct {
	Identifier string `json:"identifier"`
	Type       string `json:"type,omitempty"`
}

type ExtendedCaptureEntity struct {
	Address  string                   `json:"address"`
	Type     string                   `json:"type"`
	Features []ExtendedCaptureFeature `json:"features"`
}

type ExtendedCaptureFeature struct {
	Address           string                    `json:"address"`
	Type              string                    `json:"type"`
	Role              string                    `json:"role"`
	Description       string                    `json:"description,omitempty"`
	SubscriptionProbe string                    `json:"subscriptionProbe"`
	Functions         []ExtendedCaptureFunction `json:"functions"`
}

type ExtendedCaptureFunction struct {
	Name         string           `json:"name"`
	Operations   CaptureOperation `json:"operations"`
	Status       string           `json:"status"`
	Error        string           `json:"error,omitempty"`
	CachedBefore json.RawMessage  `json:"cachedBefore,omitempty"`
	Data         json.RawMessage  `json:"data,omitempty"`
}

type CaptureOperation struct {
	Read         bool `json:"read"`
	ReadPartial  bool `json:"readPartial"`
	Write        bool `json:"write"`
	WritePartial bool `json:"writePartial"`
}

// These are the feature families for which the capture registers a local
// client and issues reads. Other discovered feature types remain in the
// artifact with their advertised operations, but are not actively queried.
var extendedCaptureFeatureTypes = []model.FeatureTypeType{
	model.FeatureTypeTypeDeviceClassification,
	model.FeatureTypeTypeDeviceConfiguration,
	model.FeatureTypeTypeDeviceDiagnosis,
	model.FeatureTypeTypeElectricalConnection,
	model.FeatureTypeTypeHvac,
	model.FeatureTypeTypeLoadControl,
	model.FeatureTypeTypeMeasurement,
	model.FeatureTypeTypeSetpoint,
	model.FeatureTypeTypeSmartEnergyManagementPs,
}

var extendedCaptureFeatureSet = func() map[model.FeatureTypeType]bool {
	result := make(map[model.FeatureTypeType]bool, len(extendedCaptureFeatureTypes))
	for _, featureType := range extendedCaptureFeatureTypes {
		result[featureType] = true
	}
	return result
}()

func NewExtendedCapture(logf func(format string, args ...any)) *ExtendedCapture {
	if logf == nil {
		logf = log.Printf
	}
	return &ExtendedCapture{
		captured:     make(map[string]bool),
		logf:         logf,
		pollInterval: 250 * time.Millisecond,
		pollTimeout:  30 * time.Second,
		now:          time.Now,
	}
}

var defaultExtendedCapture = NewExtendedCapture(nil)

func DefaultExtendedCapture() *ExtendedCapture { return defaultExtendedCapture }

// Setup arms the capture and registers the local client features required as
// sender addresses for read requests. It must run before the EEBUS service is
// started so the feature map announced to the remote device is complete.
func (c *ExtendedCapture) Setup(localEntity spineapi.EntityLocalInterface, outputDir string) {
	if c == nil || localEntity == nil {
		return
	}
	c.mu.Lock()
	c.localEntity = localEntity
	c.outputDir = outputDir
	c.mu.Unlock()

	for _, featureType := range extendedCaptureFeatureTypes {
		localEntity.GetOrAddFeature(featureType, model.RoleTypeClient)
	}
}

type extendedCaptureTarget struct {
	feature      spineapi.FeatureRemoteInterface
	entityIndex  int
	featureIndex int
}

type extendedCapturePending struct {
	target        extendedCaptureTarget
	functionIndex int
	function      model.FunctionType
	response      chan any
	callbackError string
}

// CaptureOnce starts one asynchronous capture per normalized SKI. Calls made
// before detailed discovery provides readable target functions do not consume
// the once-only slot, allowing a later use-case event to retry.
func (c *ExtendedCapture) CaptureOnce(ski string, device spineapi.DeviceRemoteInterface) {
	if c == nil || device == nil {
		return
	}
	ski = NormalizeSKI(ski)
	if ski == "" {
		ski = NormalizeSKI(device.Ski())
	}
	if ski == "" {
		return
	}

	c.mu.Lock()
	local := c.localEntity
	if local == nil || c.captured[ski] {
		c.mu.Unlock()
		return
	}
	document, targets := buildExtendedCaptureDocument(c.now().UTC(), ski, device)
	if len(targets) == 0 {
		c.mu.Unlock()
		return
	}
	c.captured[ski] = true
	c.mu.Unlock()

	pending := c.requestReadableFunctions(local, &document, targets)
	go c.collectAndWrite(ski, document, pending)
}

func buildExtendedCaptureDocument(capturedAt time.Time, ski string, device spineapi.DeviceRemoteInterface) (ExtendedCaptureDocument, []extendedCaptureTarget) {
	document := ExtendedCaptureDocument{
		SchemaVersion: extendedCaptureSchemaVersion,
		CapturedAt:    capturedAt,
		Device: ExtendedCaptureDevice{
			Identifier: redactedDeviceIdentifier(ski),
		},
		Entities: []ExtendedCaptureEntity{},
	}
	if device.DeviceType() != nil {
		document.Device.Type = string(*device.DeviceType())
	}
	if raw, err := marshalRedacted(device.UseCases()); err == nil {
		document.UseCases = raw
	}

	entities := append([]spineapi.EntityRemoteInterface(nil), device.Entities()...)
	sort.SliceStable(entities, func(i, j int) bool {
		if entities[i] == nil {
			return false
		}
		if entities[j] == nil {
			return true
		}
		return formatEntityAddress(entities[i].Address()) < formatEntityAddress(entities[j].Address())
	})

	var targets []extendedCaptureTarget
	for _, entity := range entities {
		if entity == nil {
			continue
		}
		entityCapture := ExtendedCaptureEntity{
			Address:  formatEntityAddress(entity.Address()),
			Type:     string(entity.EntityType()),
			Features: []ExtendedCaptureFeature{},
		}
		features := append([]spineapi.FeatureRemoteInterface(nil), entity.Features()...)
		sort.SliceStable(features, func(i, j int) bool {
			return extendedFeatureSortKey(features[i]) < extendedFeatureSortKey(features[j])
		})

		entityIndex := len(document.Entities)
		for _, feature := range features {
			if feature == nil {
				continue
			}
			featureCapture := ExtendedCaptureFeature{
				Address:           formatCaptureFeatureAddress(feature.Address()),
				Type:              string(feature.Type()),
				Role:              string(feature.Role()),
				SubscriptionProbe: extendedCaptureSubscription,
				Functions:         []ExtendedCaptureFunction{},
			}
			if description := feature.Description(); description != nil {
				featureCapture.Description = string(*description)
			}

			functions := make([]model.FunctionType, 0, len(feature.Operations()))
			for function := range feature.Operations() {
				functions = append(functions, function)
			}
			sort.Slice(functions, func(i, j int) bool { return functions[i] < functions[j] })
			for _, function := range functions {
				op := feature.Operations()[function]
				if op == nil {
					continue
				}
				status := "not_readable"
				if op.Read() && (feature.Role() != model.RoleTypeServer || !extendedCaptureFeatureSet[feature.Type()]) {
					status = "not_queried_unsupported_feature"
				}
				functionCapture := ExtendedCaptureFunction{
					Name: string(function),
					Operations: CaptureOperation{
						Read:         op.Read(),
						ReadPartial:  op.ReadPartial(),
						Write:        op.Write(),
						WritePartial: op.WritePartial(),
					},
					Status: status,
				}
				if cached := feature.DataCopy(function); !isNilPointer(cached) {
					if raw, err := marshalRedacted(cached); err == nil {
						functionCapture.CachedBefore = raw
					} else {
						functionCapture.Error = fmt.Sprintf("encoding cached data: %v", err)
					}
				}
				featureCapture.Functions = append(featureCapture.Functions, functionCapture)
			}

			featureIndex := len(entityCapture.Features)
			entityCapture.Features = append(entityCapture.Features, featureCapture)
			if feature.Role() == model.RoleTypeServer && extendedCaptureFeatureSet[feature.Type()] && len(functions) > 0 {
				targets = append(targets, extendedCaptureTarget{
					feature:      feature,
					entityIndex:  entityIndex,
					featureIndex: featureIndex,
				})
			}
		}
		document.Entities = append(document.Entities, entityCapture)
	}
	return document, targets
}

func (c *ExtendedCapture) requestReadableFunctions(local spineapi.EntityLocalInterface, document *ExtendedCaptureDocument, targets []extendedCaptureTarget) []extendedCapturePending {
	var pending []extendedCapturePending
	for _, target := range targets {
		featureCapture := &document.Entities[target.entityIndex].Features[target.featureIndex]
		localFeature := local.FeatureOfTypeAndRole(target.feature.Type(), model.RoleTypeClient)
		if localFeature == nil {
			for i := range featureCapture.Functions {
				if featureCapture.Functions[i].Operations.Read {
					featureCapture.Functions[i].Status = "no_local_client_feature"
				}
			}
			continue
		}
		for i := range featureCapture.Functions {
			functionCapture := &featureCapture.Functions[i]
			if !functionCapture.Operations.Read {
				continue
			}
			function := model.FunctionType(functionCapture.Name)
			functionCapture.Status = "read_requested"
			msgCounter, requestErr := localFeature.RequestRemoteData(function, nil, nil, target.feature)
			if requestErr != nil {
				functionCapture.Status = "read_error"
				functionCapture.Error = errorTypeString(requestErr)
				continue
			}
			response := make(chan any, 1)
			item := extendedCapturePending{
				target:        target,
				functionIndex: i,
				function:      function,
				response:      response,
			}
			if msgCounter == nil {
				item.callbackError = "read request returned no message counter"
			} else if err := localFeature.AddResponseCallback(*msgCounter, func(message spineapi.ResponseMessage) {
				select {
				case response <- message.Data:
				default:
				}
			}); err != nil {
				item.callbackError = fmt.Sprintf("registering response callback: %v", err)
			}
			pending = append(pending, item)
		}
	}
	return pending
}

func (c *ExtendedCapture) collectAndWrite(ski string, document ExtendedCaptureDocument, pending []extendedCapturePending) {
	deadline := time.NewTimer(c.pollTimeout)
	ticker := time.NewTicker(c.pollInterval)
	defer deadline.Stop()
	defer ticker.Stop()

	for len(pending) > 0 {
		remaining := pending[:0]
		for _, item := range pending {
			select {
			case data := <-item.response:
				c.recordCaptureResponse(&document, item, data, "received")
				continue
			default:
			}
			// A reply can update the remote cache before the callback is attached.
			// Cache fallback is conclusive only when no value existed before.
			functionCapture := c.captureFunction(&document, item)
			if len(functionCapture.CachedBefore) == 0 {
				if data := item.target.feature.DataCopy(item.function); !isNilPointer(data) {
					c.recordCaptureResponse(&document, item, data, "received_via_cache")
					continue
				}
			}
			remaining = append(remaining, item)
		}
		pending = remaining
		if len(pending) == 0 {
			break
		}

		select {
		case <-ticker.C:
		case <-deadline.C:
			for _, item := range pending {
				functionCapture := c.captureFunction(&document, item)
				functionCapture.Status = "timeout"
				if item.callbackError != "" {
					functionCapture.Error = item.callbackError
				}
				if data := item.target.feature.DataCopy(item.function); !isNilPointer(data) {
					if raw, err := marshalRedacted(data); err == nil {
						functionCapture.Data = raw
						if len(functionCapture.CachedBefore) > 0 {
							functionCapture.Status = "timeout_with_cached_data"
						}
					} else if functionCapture.Error == "" {
						functionCapture.Error = fmt.Sprintf("encoding post-read data: %v", err)
					}
				}
			}
			pending = nil
		}
	}

	c.writeArtifacts(ski, document)
}

func (c *ExtendedCapture) captureFunction(document *ExtendedCaptureDocument, item extendedCapturePending) *ExtendedCaptureFunction {
	return &document.Entities[item.target.entityIndex].Features[item.target.featureIndex].Functions[item.functionIndex]
}

func (c *ExtendedCapture) recordCaptureResponse(document *ExtendedCaptureDocument, item extendedCapturePending, data any, status string) {
	functionCapture := c.captureFunction(document, item)
	if result, ok := data.(*model.ResultDataType); ok && result != nil && result.ErrorNumber != nil && *result.ErrorNumber != model.ErrorNumberTypeNoError {
		functionCapture.Status = "read_error"
		functionCapture.Error = errorTypeString(&model.ErrorType{ErrorNumber: *result.ErrorNumber, Description: result.Description})
		if raw, err := marshalRedacted(result); err == nil {
			functionCapture.Data = raw
		}
		return
	}
	if isNilPointer(data) {
		data = item.target.feature.DataCopy(item.function)
	}
	if isNilPointer(data) {
		functionCapture.Status = "received_without_data"
		return
	}
	raw, err := marshalRedacted(data)
	if err != nil {
		functionCapture.Status = "encoding_error"
		functionCapture.Error = err.Error()
		return
	}
	functionCapture.Status = status
	functionCapture.Data = raw
}

func (c *ExtendedCapture) writeArtifacts(ski string, document ExtendedCaptureDocument) {
	jsonData, err := marshalCaptureJSON(document, true)
	if err != nil {
		c.logf("[EXTCAPTURE] encoding capture failed: %v", err)
		return
	}
	jsonData = append(jsonData, '\n')
	textData := []byte(formatExtendedCaptureText(document))

	c.mu.Lock()
	outputDir := c.outputDir
	c.mu.Unlock()
	if outputDir == "" {
		c.logf("[EXTCAPTURE] output directory empty; JSON follows: %s", strings.TrimSpace(string(jsonData)))
		for _, line := range strings.Split(strings.TrimRight(string(textData), "\n"), "\n") {
			c.logf("%s", line)
		}
		return
	}
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		c.logf("[EXTCAPTURE] creating output directory %s failed: %v", outputDir, err)
		return
	}
	base := fmt.Sprintf("%s-eebus-extended-%s", document.CapturedAt.Format("20060102T150405.000000000Z"), deviceToken(ski))
	jsonPath := filepath.Join(outputDir, base+".json")
	textPath := filepath.Join(outputDir, base+".txt")
	if err := os.WriteFile(jsonPath, jsonData, 0o600); err != nil {
		c.logf("[EXTCAPTURE] writing JSON %s failed: %v", jsonPath, err)
		return
	}
	if err := os.WriteFile(textPath, textData, 0o600); err != nil {
		c.logf("[EXTCAPTURE] writing text %s failed: %v", textPath, err)
		return
	}
	c.logf("[EXTCAPTURE] completed device=%s json=%s text=%s", redactedDeviceIdentifier(ski), jsonPath, textPath)
}

func formatExtendedCaptureText(document ExtendedCaptureDocument) string {
	var out strings.Builder
	fmt.Fprintf(&out, "[EXTCAPTURE] schema=%d capturedAt=%s device=%s type=%s\n",
		document.SchemaVersion, document.CapturedAt.Format(time.RFC3339Nano), document.Device.Identifier, document.Device.Type)
	for _, entity := range document.Entities {
		fmt.Fprintf(&out, "[EXTCAPTURE] entity=%s type=%s\n", entity.Address, entity.Type)
		for _, feature := range entity.Features {
			fmt.Fprintf(&out, "[EXTCAPTURE]   feature=%s type=%s role=%s subscription=%s\n",
				feature.Address, feature.Type, feature.Role, feature.SubscriptionProbe)
			for _, function := range feature.Functions {
				fmt.Fprintf(&out, "[EXTCAPTURE]     function=%s operations=read:%t,readPartial:%t,write:%t,writePartial:%t status=%s",
					function.Name, function.Operations.Read, function.Operations.ReadPartial,
					function.Operations.Write, function.Operations.WritePartial, function.Status)
				if function.Error != "" {
					fmt.Fprintf(&out, " error=%q", function.Error)
				}
				if len(function.CachedBefore) > 0 {
					fmt.Fprintf(&out, " cachedBefore=%s", compactJSON(function.CachedBefore))
				}
				if len(function.Data) > 0 {
					fmt.Fprintf(&out, " data=%s", compactJSON(function.Data))
				}
				out.WriteByte('\n')
			}
		}
	}
	return out.String()
}

func compactJSON(data json.RawMessage) string {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return string(data)
	}
	result, err := marshalCaptureJSON(value, false)
	if err != nil {
		return string(data)
	}
	return string(result)
}

func extendedFeatureSortKey(feature spineapi.FeatureRemoteInterface) string {
	if feature == nil {
		return "~"
	}
	return fmt.Sprintf("%s/%s/%s", feature.Type(), feature.Role(), formatCaptureFeatureAddress(feature.Address()))
}

func formatCaptureFeatureAddress(address *model.FeatureAddressType) string {
	if address == nil {
		return "?"
	}
	parts := make([]string, 0, len(address.Entity)+1)
	for _, entity := range address.Entity {
		parts = append(parts, fmt.Sprintf("%d", uint(entity)))
	}
	if address.Feature != nil {
		parts = append(parts, fmt.Sprintf("f%d", uint(*address.Feature)))
	}
	if len(parts) == 0 {
		return "?"
	}
	return strings.Join(parts, ":")
}

func marshalRedacted(value any) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return nil, err
	}
	redactCaptureValue(generic)
	return marshalCaptureJSON(generic, false)
}

func marshalCaptureJSON(value any, indent bool) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if indent {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(output.Bytes()), nil
}

var captureRedactedKeys = map[string]bool{
	"device":                         true,
	"manufacturerNodeIdentification": true,
	"serialNumber":                   true,
	"ski":                            true,
	"userNodeIdentification":         true,
}

func redactCaptureValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if captureRedactedKeys[key] {
				typed[key] = "<redacted>"
				continue
			}
			redactCaptureValue(child)
		}
	case []any:
		for _, child := range typed {
			redactCaptureValue(child)
		}
	}
}

func redactedDeviceIdentifier(ski string) string {
	return fmt.Sprintf("<redacted:sha256:%s>", deviceToken(ski))
}

func deviceToken(ski string) string {
	hash := sha256.Sum256([]byte(NormalizeSKI(ski)))
	return hex.EncodeToString(hash[:6])
}
