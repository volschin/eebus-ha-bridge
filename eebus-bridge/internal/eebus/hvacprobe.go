package eebus

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

// HvacProbe is a read-only diagnostic for the VR940's HVAC domain (stage 1 of
// the HVAC/DHW control spike, see docs/vr940-usecase-dump.txt). The dump shows
// the DHWCircuit and HVACRoom entities expose Setpoint and HVAC server
// features backing the configurationOfDhw*/configurationOfRoomHeating* use
// cases, but eebus-go ships no use case for them — so this probe talks raw
// SPINE: it requests every Setpoint/HVAC read function from those features and
// logs the returned data plus the advertised read/write operations. The
// operations output is the payoff: it tells us which functions the device
// declares writable before we attempt any write.
//
// Gated behind experimental.hvac_probe; sends only SPINE read commands, never
// writes or subscribes. Stage 2 (experimental.hvac_probe_bind, EnableBind)
// additionally requests a binding to each remote Setpoint/HVAC server feature
// — the precondition for any SPINE write — and reports whether the device
// accepts it. Still no writes.
type HvacProbe struct {
	mu          sync.Mutex
	localEntity spineapi.EntityLocalInterface
	probed      map[string]bool
	logf        func(format string, args ...any)

	// bindEnabled turns on the stage-2 binding attempt. resultHooked tracks
	// which local client features already log incoming SPINE result messages
	// (errors replying to the read requests). Bind accept/deny results do NOT
	// arrive there: binding management runs NodeManagement-to-NodeManagement,
	// so the device addresses its result to the local NodeManagement feature.
	// nmHooked guards the one callback registered there; bindResults collects
	// its errorNumber per msgCounterReference for collectBindResults to match
	// against the counters returned by BindToRemote.
	bindEnabled  bool
	resultHooked map[model.FeatureTypeType]bool
	nmHooked     bool
	bindResults  map[uint64]int64

	// pollInterval/pollTimeout control how long collectData waits for read
	// responses to land in the remote feature caches. Overridden in tests.
	pollInterval time.Duration
	pollTimeout  time.Duration
}

// hvacProbeFunctions lists, per feature type, the SPINE read functions the
// probe requests. Order matches the SPINE spec grouping (descriptions before
// values) purely for log readability.
var hvacProbeFunctions = map[model.FeatureTypeType][]model.FunctionType{
	model.FeatureTypeTypeSetpoint: {
		model.FunctionTypeSetpointDescriptionListData,
		model.FunctionTypeSetpointConstraintsListData,
		model.FunctionTypeSetpointListData,
	},
	model.FeatureTypeTypeHvac: {
		model.FunctionTypeHvacSystemFunctionDescriptionListData,
		model.FunctionTypeHvacSystemFunctionListData,
		model.FunctionTypeHvacOperationModeDescriptionListData,
		model.FunctionTypeHvacSystemFunctionOperationModeRelationListData,
		model.FunctionTypeHvacSystemFunctionSetPointRelationListData,
		model.FunctionTypeHvacOverrunDescriptionListData,
		model.FunctionTypeHvacOverrunListData,
	},
}

// NewHvacProbe returns a probe writing through logf (nil uses log.Printf).
// The probe stays inert until Setup provides a local entity.
func NewHvacProbe(logf func(format string, args ...any)) *HvacProbe {
	if logf == nil {
		logf = log.Printf
	}
	return &HvacProbe{
		probed:       make(map[string]bool),
		resultHooked: make(map[model.FeatureTypeType]bool),
		bindResults:  make(map[uint64]int64),
		logf:         logf,
		pollInterval: 2 * time.Second,
		pollTimeout:  30 * time.Second,
	}
}

var defaultHvacProbe = NewHvacProbe(nil)

// DefaultHvacProbe returns the process-wide probe shared by the use-case
// wrappers, mirroring DefaultUseCaseDiscovery.
func DefaultHvacProbe() *HvacProbe { return defaultHvacProbe }

// hvacProbeUseCases lists the client-side (ConfigurationAppliance actor)
// counterparts of the configurationOf* use cases the VR940 advertises on its
// DHWCircuit/HVACRoom entities (docs/vr940-usecase-dump.txt). Stage 2b:
// announcing these in the bridge's NodeManagement use-case data tests whether
// the VR940 gates bind acceptance on the partner declaring matching client
// use cases. Scenario lists mirror what the device advertises.
var hvacProbeUseCases = []struct {
	name      model.UseCaseNameType
	scenarios []model.UseCaseScenarioSupportType
}{
	{model.UseCaseNameTypeConfigurationOfDhwSystemFunction, []model.UseCaseScenarioSupportType{1, 2, 3}},
	{model.UseCaseNameTypeConfigurationOfDhwTemperature, []model.UseCaseScenarioSupportType{1}},
	{model.UseCaseNameTypeConfigurationOfRoomHeatingSystemFunction, []model.UseCaseScenarioSupportType{1}},
	{model.UseCaseNameTypeConfigurationOfRoomHeatingTemperature, []model.UseCaseScenarioSupportType{1}},
}

// Setup arms the probe: it registers Setpoint and HVAC client features on the
// local entity (required as SPINE sender addresses for the read requests) and
// advertises the matching ConfigurationAppliance client use cases. Both change
// the announced NodeManagement data, so Setup must run before the EEBUS
// service starts. Not calling Setup leaves ProbeOnce a no-op.
func (p *HvacProbe) Setup(localEntity spineapi.EntityLocalInterface) {
	if p == nil || localEntity == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.localEntity = localEntity
	localEntity.GetOrAddFeature(model.FeatureTypeTypeSetpoint, model.RoleTypeClient)
	localEntity.GetOrAddFeature(model.FeatureTypeTypeHvac, model.RoleTypeClient)
	for _, uc := range hvacProbeUseCases {
		localEntity.AddUseCaseSupport(
			model.UseCaseActorTypeConfigurationAppliance,
			uc.name,
			model.SpecificationVersionType("1.0.0"),
			"release",
			true,
			uc.scenarios,
		)
	}
}

// ProbeOnce probes a remote device the first time it appears with at least one
// Setpoint/HVAC server feature. Devices without such features are ignored but
// not marked probed, so a later event (once SPINE detailed discovery filled in
// the entities) retries. Safe for nil receiver/device and when Setup never ran.
func (p *HvacProbe) ProbeOnce(ski string, device spineapi.DeviceRemoteInterface) {
	if p == nil || device == nil {
		return
	}

	ski = NormalizeSKI(ski)
	if ski == "" {
		ski = NormalizeSKI(device.Ski())
	}

	p.mu.Lock()
	local := p.localEntity
	if local == nil || p.probed[ski] {
		p.mu.Unlock()
		return
	}
	targets := p.findTargets(device)
	if len(targets) == 0 {
		p.mu.Unlock()
		return
	}
	p.probed[ski] = true
	p.mu.Unlock()

	pending := p.requestAll(ski, local, targets)
	if len(pending) > 0 {
		go p.collectData(ski, pending)
	}

	p.mu.Lock()
	bind := p.bindEnabled
	p.mu.Unlock()
	if bind {
		if requested := p.bindAll(ski, local, targets); len(requested) > 0 {
			go p.collectBindResults(ski, requested)
		}
	}
}

// EnableBind arms stage 2: ProbeOnce additionally requests a binding to each
// remote Setpoint/HVAC server feature. Call alongside Setup, before the
// service starts.
func (p *HvacProbe) EnableBind() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.bindEnabled = true
	p.mu.Unlock()
}

// probeTarget is one remote Setpoint/HVAC server feature to interrogate.
type probeTarget struct {
	entityAddr string
	entityType model.EntityTypeType
	feature    spineapi.FeatureRemoteInterface
}

// pendingRead tracks one requested function awaiting response data.
type pendingRead struct {
	target   probeTarget
	function model.FunctionType
}

// findTargets collects every Setpoint/HVAC server feature across the device's
// entities. Caller holds p.mu.
func (p *HvacProbe) findTargets(device spineapi.DeviceRemoteInterface) []probeTarget {
	var targets []probeTarget
	for _, entity := range device.Entities() {
		if entity == nil {
			continue
		}
		for featureType := range hvacProbeFunctions {
			feature := entity.FeatureOfTypeAndRole(featureType, model.RoleTypeServer)
			if feature == nil {
				continue
			}
			targets = append(targets, probeTarget{
				entityAddr: formatEntityAddress(entity.Address()),
				entityType: entity.EntityType(),
				feature:    feature,
			})
		}
	}
	return targets
}

// requestAll logs each target's advertised operations (read/write flags per
// function — the ground truth for what the device claims is controllable) and
// fires a SPINE read request per candidate function. Returns the reads to
// poll for response data.
func (p *HvacProbe) requestAll(ski string, local spineapi.EntityLocalInterface, targets []probeTarget) []pendingRead {
	var pending []pendingRead
	for _, t := range targets {
		featureType := t.feature.Type()
		p.logf("[HVACPROBE] ski=%s entity=%s type=%s feature=%s operations=[%s]",
			ski, t.entityAddr, t.entityType, featureType, formatOperations(t.feature.Operations()))

		localFeature := local.FeatureOfTypeAndRole(featureType, model.RoleTypeClient)
		if localFeature == nil {
			p.logf("[HVACPROBE] ski=%s entity=%s feature=%s: no local client feature, skipping",
				ski, t.entityAddr, featureType)
			continue
		}

		for _, fn := range hvacProbeFunctions[featureType] {
			if _, err := localFeature.RequestRemoteData(fn, nil, nil, t.feature); err != nil {
				p.logf("[HVACPROBE] ski=%s entity=%s read %s failed: %s",
					ski, t.entityAddr, fn, errorTypeString(err))
				continue
			}
			pending = append(pending, pendingRead{target: t, function: fn})
		}
	}
	return pending
}

// pendingBind tracks one bind request awaiting confirmation.
type pendingBind struct {
	target     probeTarget
	msgCounter uint64
}

// bindAll requests a binding from the local client feature to each remote
// Setpoint/HVAC server feature. Acceptance is confirmed asynchronously via
// the result callback on the local NodeManagement feature (binding management
// is a NodeManagement-to-NodeManagement exchange, so the accept/deny result
// is addressed there — NOT to the client feature; spine-go's own
// bindResponseCallback and HasBindingToRemote miss it for the same reason).
// collectBindResults matches the results against the request msgCounters.
func (p *HvacProbe) bindAll(ski string, local spineapi.EntityLocalInterface, targets []probeTarget) []pendingBind {
	p.hookNodeManagementResults(local)
	var requested []pendingBind
	for _, t := range targets {
		featureType := t.feature.Type()
		localFeature := local.FeatureOfTypeAndRole(featureType, model.RoleTypeClient)
		if localFeature == nil {
			continue
		}
		p.hookResultLogging(ski, featureType, localFeature)

		remoteAddr := t.feature.Address()
		if localFeature.HasBindingToRemote(remoteAddr) {
			p.logf("[HVACPROBE] ski=%s entity=%s bind %s: already bound", ski, t.entityAddr, featureType)
			continue
		}
		msgCounter, err := localFeature.BindToRemote(remoteAddr)
		if err != nil {
			p.logf("[HVACPROBE] ski=%s entity=%s bind %s request failed: %s",
				ski, t.entityAddr, featureType, errorTypeString(err))
			continue
		}
		counter := uint64(0)
		if msgCounter != nil {
			counter = uint64(*msgCounter)
		}
		p.logf("[HVACPROBE] ski=%s entity=%s bind %s requested (msgCounter=%d)",
			ski, t.entityAddr, featureType, counter)
		requested = append(requested, pendingBind{target: t, msgCounter: counter})
	}
	return requested
}

// hookNodeManagementResults registers, once, a result callback on the local
// NodeManagement feature that records every result's errorNumber by
// msgCounterReference — the only place bind accept/deny replies surface.
func (p *HvacProbe) hookNodeManagementResults(local spineapi.EntityLocalInterface) {
	p.mu.Lock()
	hooked := p.nmHooked
	if !hooked {
		p.nmHooked = true
	}
	p.mu.Unlock()
	if hooked {
		return
	}
	local.Device().NodeManagement().AddResultCallback(func(msg spineapi.ResponseMessage) {
		result, ok := msg.Data.(*model.ResultDataType)
		if !ok || result.ErrorNumber == nil {
			return
		}
		p.mu.Lock()
		p.bindResults[uint64(msg.MsgCounterReference)] = int64(*result.ErrorNumber)
		p.mu.Unlock()
	})
}

// hookResultLogging logs every SPINE result message arriving at a local
// client feature once per feature type. Bind accept/deny replies surface here
// with their error number (0 = accepted).
func (p *HvacProbe) hookResultLogging(ski string, featureType model.FeatureTypeType, localFeature spineapi.FeatureLocalInterface) {
	p.mu.Lock()
	hooked := p.resultHooked[featureType]
	if !hooked {
		p.resultHooked[featureType] = true
	}
	p.mu.Unlock()
	if hooked {
		return
	}
	localFeature.AddResultCallback(func(msg spineapi.ResponseMessage) {
		result, ok := msg.Data.(*model.ResultDataType)
		if !ok {
			return
		}
		errno := int64(-1)
		if result.ErrorNumber != nil {
			errno = int64(*result.ErrorNumber)
		}
		desc := ""
		if result.Description != nil {
			desc = " " + string(*result.Description)
		}
		p.logf("[HVACPROBE] ski=%s feature=%s result msgCounterRef=%d errorNumber=%d%s",
			ski, featureType, uint64(msg.MsgCounterReference), errno, desc)
	})
}

// collectBindResults polls the NodeManagement result log until each requested
// bind's msgCounter got an accept (errorNumber 0) or deny result, or the
// timeout passes.
func (p *HvacProbe) collectBindResults(ski string, pending []pendingBind) {
	deadline := time.Now().Add(p.pollTimeout)
	for len(pending) > 0 && time.Now().Before(deadline) {
		time.Sleep(p.pollInterval)
		remaining := pending[:0]
		for _, b := range pending {
			p.mu.Lock()
			errno, ok := p.bindResults[b.msgCounter]
			p.mu.Unlock()
			if !ok {
				remaining = append(remaining, b)
				continue
			}
			if errno == 0 {
				p.logf("[HVACPROBE] ski=%s entity=%s bind %s ACCEPTED (msgCounter=%d)",
					ski, b.target.entityAddr, b.target.feature.Type(), b.msgCounter)
			} else {
				p.logf("[HVACPROBE] ski=%s entity=%s bind %s DENIED errorNumber=%d (msgCounter=%d)",
					ski, b.target.entityAddr, b.target.feature.Type(), errno, b.msgCounter)
			}
		}
		pending = remaining
	}
	for _, b := range pending {
		p.logf("[HVACPROBE] ski=%s entity=%s bind %s NOT answered within %s",
			ski, b.target.entityAddr, b.target.feature.Type(), p.pollTimeout)
	}
}

// collectData polls the remote feature caches until every requested function
// produced data or the timeout passes, logging each payload as JSON once.
func (p *HvacProbe) collectData(ski string, pending []pendingRead) {
	deadline := time.Now().Add(p.pollTimeout)
	for len(pending) > 0 && time.Now().Before(deadline) {
		time.Sleep(p.pollInterval)
		remaining := pending[:0]
		for _, r := range pending {
			data := r.target.feature.DataCopy(r.function)
			if data == nil || isNilPointer(data) {
				remaining = append(remaining, r)
				continue
			}
			payload, err := json.Marshal(data)
			if err != nil {
				payload = []byte(fmt.Sprintf("%+v", data))
			}
			p.logf("[HVACPROBE] ski=%s entity=%s type=%s data %s = %s",
				ski, r.target.entityAddr, r.target.entityType, r.function, payload)
		}
		pending = remaining
	}
	for _, r := range pending {
		p.logf("[HVACPROBE] ski=%s entity=%s data %s = <no response within %s>",
			ski, r.target.entityAddr, r.function, p.pollTimeout)
	}
}

// isNilPointer reports whether DataCopy returned a typed nil pointer wrapped
// in a non-nil interface (spine-go returns e.g. (*SetpointListDataType)(nil)
// for functions that never received data).
func isNilPointer(v any) bool {
	return v == nil || fmt.Sprintf("%v", v) == "<nil>"
}

// formatOperations renders a sorted "function:r/w" summary of a feature's
// advertised operations. Empty when the device announced none (yet).
func formatOperations(ops map[model.FunctionType]spineapi.OperationsInterface) string {
	out := make([]string, 0, len(ops))
	for fn, op := range ops {
		if op == nil {
			continue
		}
		flags := ""
		if op.Read() {
			flags += "r"
		}
		if op.Write() {
			flags += "w"
		}
		if flags == "" {
			flags = "-"
		}
		out = append(out, fmt.Sprintf("%s:%s", fn, flags))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// errorTypeString renders a spine ErrorType for logging.
func errorTypeString(err *model.ErrorType) string {
	if err == nil {
		return "unknown error"
	}
	if err.Description != nil && *err.Description != "" {
		return fmt.Sprintf("%d (%s)", err.ErrorNumber, string(*err.Description))
	}
	return fmt.Sprintf("error number %d", err.ErrorNumber)
}
