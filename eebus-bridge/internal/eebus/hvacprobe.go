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
// accepts it. Stage 3 (experimental.hvac_probe_write, EnableWrite) sends one
// echo write per accepted Setpoint binding: it writes the device's own current
// SetpointListData back unchanged, so the write path is exercised without
// altering any temperature, and reports the device's accept/deny result.
// Stage 3b (experimental.hvac_probe_write_delta_ski, EnableWriteDelta) proves
// the device applies writes: scoped to one SKI, it changes the dhwTemperature
// setpoint by one advertised step, confirms via re-read, and restores.
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
	ucAdvertised bool
	resultHooked map[model.FeatureTypeType]bool
	nmHooked     bool
	bindResults  map[uint64]int64

	// writeEnabled turns on the stage-3 echo write. featureResults collects,
	// per msgCounterReference, the errorNumber of result messages arriving at
	// the local client features: unlike binds, a write is a direct
	// client-to-server command, so the device addresses its result to the
	// sending client feature. bindResults is still polled as a fallback in
	// case a device routes the write result via NodeManagement instead.
	writeEnabled   bool
	featureResults map[uint64]int64

	// writeDeltaSKI arms the stage-3b value-changing test for exactly one
	// device: on that device's DHWCircuit entity the echo write is replaced by
	// write(current±step) → re-read to confirm the device applied it →
	// write(original) → re-read. The setpoint is off its original value only
	// for the seconds between the two writes. Empty means never.
	writeDeltaSKI string

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
		probed:         make(map[string]bool),
		resultHooked:   make(map[model.FeatureTypeType]bool),
		bindResults:    make(map[uint64]int64),
		featureResults: make(map[uint64]int64),
		logf:           logf,
		pollInterval:   2 * time.Second,
		pollTimeout:    30 * time.Second,
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
	{model.UseCaseNameTypeConfigurationOfRoomHeatingSystemFunction, []model.UseCaseScenarioSupportType{1}},
	{model.UseCaseNameTypeConfigurationOfRoomHeatingTemperature, []model.UseCaseScenarioSupportType{1}},
}

// Setup arms the probe: it registers Setpoint and HVAC client features on the
// local entity (required as SPINE sender addresses for the read requests).
// The ConfigurationAppliance client use cases are only advertised once the
// bind stage is armed (stage 2b) — the pure read probe must not claim use
// cases the bridge doesn't implement. Both change the announced
// NodeManagement data, so Setup and EnableBind must run before the EEBUS
// service starts. Not calling Setup leaves ProbeOnce a no-op.
func (p *HvacProbe) Setup(localEntity spineapi.EntityLocalInterface) {
	if p == nil || localEntity == nil {
		return
	}
	p.mu.Lock()
	p.localEntity = localEntity
	bind := p.bindEnabled
	p.mu.Unlock()
	localEntity.GetOrAddFeature(model.FeatureTypeTypeSetpoint, model.RoleTypeClient)
	localEntity.GetOrAddFeature(model.FeatureTypeTypeHvac, model.RoleTypeClient)
	if bind {
		p.advertiseClientUseCases()
	}
}

// advertiseClientUseCases announces, once, the ConfigurationAppliance client
// counterparts of the configurationOf* use cases in NodeManagement. Part of
// stage 2b: claiming the client role is only justified when the probe
// actually binds (and possibly writes), not for raw diagnostic reads.
func (p *HvacProbe) advertiseClientUseCases() {
	p.mu.Lock()
	local := p.localEntity
	done := p.ucAdvertised
	if local != nil {
		p.ucAdvertised = true
	}
	p.mu.Unlock()
	if local == nil || done {
		return
	}
	for _, uc := range hvacProbeUseCases {
		local.AddUseCaseSupport(
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
// remote Setpoint/HVAC server feature, and the matching ConfigurationAppliance
// client use cases are advertised in NodeManagement. Call alongside Setup,
// before the service starts.
func (p *HvacProbe) EnableBind() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.bindEnabled = true
	p.mu.Unlock()
	p.advertiseClientUseCases()
}

// EnableWrite arms stage 3: after a Setpoint binding is ACCEPTED, the probe
// writes the device's current SetpointListData back unchanged and reports the
// result. Requires EnableBind; without an accepted binding no write is sent.
func (p *HvacProbe) EnableWrite() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.writeEnabled = true
	p.mu.Unlock()
}

// EnableWriteDelta arms stage 3b for exactly the device with the given SKI:
// on its DHWCircuit entity, instead of the echo write, change the
// dhwTemperature setpoint by one advertised step, re-read to confirm the
// device applied it, then write the original value back and confirm the
// restore. Fail-closed: without a SKI, a dhwTemperature-scoped setpoint
// description, complete constraints, and a writable setpointListData
// operation, no value-changing write is sent. Requires EnableWrite.
func (p *HvacProbe) EnableWriteDelta(ski string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.writeDeltaSKI = NormalizeSKI(ski)
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

// pendingBind tracks one bind request awaiting confirmation. localFeature is
// kept so the stage-3 write can reuse the bound client feature as sender.
type pendingBind struct {
	target       probeTarget
	localFeature spineapi.FeatureLocalInterface
	msgCounter   uint64
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
		requested = append(requested, pendingBind{target: t, localFeature: localFeature, msgCounter: counter})
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
		p.mu.Lock()
		p.featureResults[uint64(msg.MsgCounterReference)] = errno
		p.mu.Unlock()
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
				p.mu.Lock()
				write := p.writeEnabled
				p.mu.Unlock()
				if write && b.target.feature.Type() == model.FeatureTypeTypeSetpoint {
					go p.attemptSetpointEchoWrite(ski, b)
				}
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

// attemptSetpointEchoWrite is stage 3: once the device accepted the Setpoint
// binding, wait for its SetpointListData to arrive in the remote feature
// cache, then write that exact data back. The values are unchanged, so the
// device's temperature configuration is untouched — the point is whether the
// write command itself is ACCEPTED (result errorNumber 0) or denied, which is
// the last open question before building a real configurationOf* use case.
func (p *HvacProbe) attemptSetpointEchoWrite(ski string, b pendingBind) {
	data := p.waitForSetpointData(b.target)
	if data == nil {
		p.logf("[HVACPROBE] ski=%s entity=%s write skipped: no setpointListData within %s",
			ski, b.target.entityAddr, p.pollTimeout)
		return
	}
	if len(data.SetpointData) == 0 {
		p.logf("[HVACPROBE] ski=%s entity=%s write skipped: setpointListData has no entries", ski, b.target.entityAddr)
		return
	}

	p.mu.Lock()
	deltaSKI := p.writeDeltaSKI
	p.mu.Unlock()
	if deltaSKI != "" && deltaSKI == NormalizeSKI(ski) && b.target.entityType == model.EntityTypeTypeDHWCircuit {
		p.runSetpointDeltaTest(ski, b, data)
		return
	}

	payload, err := json.Marshal(data)
	if err != nil {
		payload = []byte(fmt.Sprintf("%+v", data))
	}
	p.logf("[HVACPROBE] ski=%s entity=%s echo-writing current setpointListData (values unchanged): %s",
		ski, b.target.entityAddr, payload)
	p.sendSetpointWriteAndAwait(ski, b, data, "write")
}

// sendSetpointWriteAndAwait sends one SetpointListData write and waits for the
// device's result. sent reports whether the command reached the sender at all;
// seen whether a result arrived; errno is only meaningful when seen. label
// distinguishes echo/delta/restore writes in the log.
func (p *HvacProbe) sendSetpointWriteAndAwait(ski string, b pendingBind, data *model.SetpointListDataType, label string) (errno int64, seen, sent bool) {
	cmd := model.CmdType{SetpointListData: data}
	msgCounter, werr := b.target.feature.Device().Sender().Write(b.localFeature.Address(), b.target.feature.Address(), cmd)
	if werr != nil {
		p.logf("[HVACPROBE] ski=%s entity=%s %s Setpoint send failed: %v", ski, b.target.entityAddr, label, werr)
		return -1, false, false
	}
	counter := uint64(0)
	if msgCounter != nil {
		counter = uint64(*msgCounter)
	}
	p.logf("[HVACPROBE] ski=%s entity=%s %s Setpoint sent (msgCounter=%d)", ski, b.target.entityAddr, label, counter)

	deadline := time.Now().Add(p.pollTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(p.pollInterval)
		p.mu.Lock()
		res, ok := p.featureResults[counter]
		if !ok {
			// Fallback: some routing puts results on NodeManagement instead.
			res, ok = p.bindResults[counter]
		}
		p.mu.Unlock()
		if !ok {
			continue
		}
		if res == 0 {
			p.logf("[HVACPROBE] ski=%s entity=%s %s Setpoint ACCEPTED (msgCounter=%d)",
				ski, b.target.entityAddr, label, counter)
		} else {
			p.logf("[HVACPROBE] ski=%s entity=%s %s Setpoint DENIED errorNumber=%d (msgCounter=%d)",
				ski, b.target.entityAddr, label, res, counter)
		}
		return res, true, true
	}
	p.logf("[HVACPROBE] ski=%s entity=%s %s Setpoint NOT answered within %s",
		ski, b.target.entityAddr, label, p.pollTimeout)
	return -1, false, true
}

// runSetpointDeltaTest is stage 3b: prove the device APPLIES writes, not just
// ACKs them. Fail-closed target selection: the setpoint must be identified as
// dhwTemperature via its description, declare complete constraints (positive
// step, min and max), and the remote feature must advertise setpointListData
// as writable — otherwise no value-changing write is sent. Once the delta
// write was handed to the sender, the original value is restored best-effort
// regardless of whether a result was seen: a lost or late result does not
// prove the device ignored the command. A failed restore is logged loudly —
// that is the one outcome needing manual attention.
func (p *HvacProbe) runSetpointDeltaTest(ski string, b pendingBind, data *model.SetpointListDataType) {
	op := b.target.feature.Operations()[model.FunctionTypeSetpointListData]
	if op == nil || !op.Write() {
		p.logf("[HVACPROBE] ski=%s entity=%s delta test skipped: setpointListData not advertised writable", ski, b.target.entityAddr)
		return
	}
	id, ok := p.dhwTemperatureSetpointId(b.target)
	if !ok {
		p.logf("[HVACPROBE] ski=%s entity=%s delta test skipped: no dhwTemperature-scoped setpoint description within %s",
			ski, b.target.entityAddr, p.pollTimeout)
		return
	}
	idx := -1
	for i, sp := range data.SetpointData {
		if sp.SetpointId != nil && *sp.SetpointId == id && sp.Value != nil {
			idx = i
			break
		}
	}
	if idx < 0 {
		p.logf("[HVACPROBE] ski=%s entity=%s delta test skipped: setpointId=%d has no value", ski, b.target.entityAddr, id)
		return
	}
	orig := data.SetpointData[idx].Value.GetValue()

	step, minV, maxV, ok := p.setpointConstraints(b.target, id)
	if !ok {
		p.logf("[HVACPROBE] ski=%s entity=%s delta test skipped: setpointId=%d has incomplete constraints (need step+min+max)",
			ski, b.target.entityAddr, id)
		return
	}
	target := orig + step
	if target > maxV {
		target = orig - step
	}
	if target < minV {
		p.logf("[HVACPROBE] ski=%s entity=%s delta test skipped: no room within constraints [%v..%v]",
			ski, b.target.entityAddr, minV, maxV)
		return
	}

	p.logf("[HVACPROBE] ski=%s entity=%s DELTA TEST setpointId=%d: %v -> %v (restore follows)",
		ski, b.target.entityAddr, id, orig, target)
	errno, seen, sent := p.sendSetpointWriteAndAwait(ski, b, setpointListWithValue(data, idx, target), "delta-write")
	if !sent {
		// Nothing reached the sender; the device cannot have changed.
		p.logf("[HVACPROBE] ski=%s entity=%s DELTA TEST aborted: delta write not sent; device unchanged at %v",
			ski, b.target.entityAddr, orig)
		return
	}
	// From here on the command is out — restore no matter what the result
	// said: even a missing or negative result does not prove the device did
	// not apply the write.
	if !seen {
		p.logf("[HVACPROBE] ski=%s entity=%s delta-write result not seen; confirming and restoring anyway", ski, b.target.entityAddr)
	}
	switch {
	case seen && errno != 0:
		p.logf("[HVACPROBE] ski=%s entity=%s DELTA TEST setpointId=%d denied by device; verifying it stayed at %v",
			ski, b.target.entityAddr, id, orig)
	case p.confirmSetpointValue(b, id, target):
		p.logf("[HVACPROBE] ski=%s entity=%s DELTA TEST setpointId=%d APPLIED: device now reports %v",
			ski, b.target.entityAddr, id, target)
	default:
		p.logf("[HVACPROBE] ski=%s entity=%s DELTA TEST setpointId=%d NOT CONFIRMED: device never reported %v (ACK without apply?)",
			ski, b.target.entityAddr, id, target)
	}

	_, _, sent = p.sendSetpointWriteAndAwait(ski, b, setpointListWithValue(data, idx, orig), "restore")
	if !sent {
		p.logf("[HVACPROBE] ski=%s entity=%s RESTORE FAILED: setpointId=%d may still be at %v instead of %v — fix manually (myVAILLANT)!",
			ski, b.target.entityAddr, id, target, orig)
		return
	}
	if p.confirmSetpointValue(b, id, orig) {
		p.logf("[HVACPROBE] ski=%s entity=%s DELTA TEST complete: setpointId=%d RESTORED to %v",
			ski, b.target.entityAddr, id, orig)
	} else {
		p.logf("[HVACPROBE] ski=%s entity=%s RESTORE NOT CONFIRMED: device never reported %v again — verify manually (myVAILLANT)!",
			ski, b.target.entityAddr, orig)
	}
}

// dhwTemperatureSetpointId polls the cached setpoint descriptions for the one
// scoped dhwTemperature. The description read is requested during stage 1, so
// the data normally arrives within the poll window.
func (p *HvacProbe) dhwTemperatureSetpointId(t probeTarget) (model.SetpointIdType, bool) {
	deadline := time.Now().Add(p.pollTimeout)
	for {
		raw := t.feature.DataCopy(model.FunctionTypeSetpointDescriptionListData)
		if desc, ok := raw.(*model.SetpointDescriptionListDataType); ok && desc != nil {
			for _, d := range desc.SetpointDescriptionData {
				if d.SetpointId != nil && d.ScopeType != nil && *d.ScopeType == model.ScopeTypeTypeDhwTemperature {
					return *d.SetpointId, true
				}
			}
		}
		if !time.Now().Before(deadline) {
			return 0, false
		}
		time.Sleep(p.pollInterval)
	}
}

// setpointListWithValue returns a copy of the list with entry idx's value
// replaced — the original stays untouched for the later restore write.
func setpointListWithValue(data *model.SetpointListDataType, idx int, value float64) *model.SetpointListDataType {
	entries := make([]model.SetpointDataType, len(data.SetpointData))
	copy(entries, data.SetpointData)
	entries[idx].Value = model.NewScaledNumberType(value)
	return &model.SetpointListDataType{SetpointData: entries}
}

// setpointConstraints returns the advertised step/min/max for a setpoint from
// the cached constraints data. ok is false unless all three are present and
// the step is positive — the delta test must not guess bounds for a write
// that changes a real appliance.
func (p *HvacProbe) setpointConstraints(t probeTarget, id model.SetpointIdType) (step, minV, maxV float64, ok bool) {
	raw := t.feature.DataCopy(model.FunctionTypeSetpointConstraintsListData)
	constraints, isList := raw.(*model.SetpointConstraintsListDataType)
	if !isList || constraints == nil {
		return 0, 0, 0, false
	}
	for _, c := range constraints.SetpointConstraintsData {
		if c.SetpointId == nil || *c.SetpointId != id {
			continue
		}
		if c.SetpointStepSize == nil || c.SetpointRangeMin == nil || c.SetpointRangeMax == nil {
			return 0, 0, 0, false
		}
		step = c.SetpointStepSize.GetValue()
		if step <= 0 {
			return 0, 0, 0, false
		}
		return step, c.SetpointRangeMin.GetValue(), c.SetpointRangeMax.GetValue(), true
	}
	return 0, 0, 0, false
}

// confirmSetpointValue re-reads SetpointListData until the device reports the
// expected value for the setpoint or the poll timeout passes. Each poll
// issues a fresh read request: the probe holds no subscription, so the remote
// cache only updates when the device answers a read.
func (p *HvacProbe) confirmSetpointValue(b pendingBind, id model.SetpointIdType, want float64) bool {
	deadline := time.Now().Add(p.pollTimeout)
	for time.Now().Before(deadline) {
		if _, err := b.localFeature.RequestRemoteData(model.FunctionTypeSetpointListData, nil, nil, b.target.feature); err != nil {
			return false
		}
		time.Sleep(p.pollInterval)
		raw := b.target.feature.DataCopy(model.FunctionTypeSetpointListData)
		data, ok := raw.(*model.SetpointListDataType)
		if !ok || data == nil {
			continue
		}
		for _, sp := range data.SetpointData {
			if sp.SetpointId != nil && *sp.SetpointId == id && sp.Value != nil && sp.Value.GetValue() == want {
				return true
			}
		}
	}
	return false
}

// waitForSetpointData polls the remote feature cache until SetpointListData is
// available or the poll timeout passes. Returns nil on timeout or type
// mismatch.
func (p *HvacProbe) waitForSetpointData(t probeTarget) *model.SetpointListDataType {
	deadline := time.Now().Add(p.pollTimeout)
	for time.Now().Before(deadline) {
		raw := t.feature.DataCopy(model.FunctionTypeSetpointListData)
		if raw != nil && !isNilPointer(raw) {
			data, ok := raw.(*model.SetpointListDataType)
			if !ok {
				return nil
			}
			return data
		}
		time.Sleep(p.pollInterval)
	}
	return nil
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
