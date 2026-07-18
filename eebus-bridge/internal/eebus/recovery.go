package eebus

import (
	"log"
	"sort"
	"sync"
	"time"
)

type RecoveryState string

const (
	RecoveryStateUnknown      RecoveryState = "unknown"
	RecoveryStateUntrusted    RecoveryState = "untrusted"
	RecoveryStateDisconnected RecoveryState = "disconnected"
	RecoveryStateGracePeriod  RecoveryState = "grace_period"
	RecoveryStateRecovering   RecoveryState = "recovering"
	RecoveryStateHealthy      RecoveryState = "healthy"
	RecoveryStateExhausted    RecoveryState = "exhausted"
)

type RecoveryConfig struct {
	StaleThreshold time.Duration
	GracePeriod    time.Duration
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
	MaxAttempts    int
}

type RecoveryRegistry interface {
	StaleDevices(time.Duration, time.Duration) []string
	MonitoringLastSuccessAge(string) (time.Duration, bool)
	MonitoringSuccessSince(string, time.Time) bool
	ClearEntities(string)
	DeviceHealth(string) (DeviceHealthSnapshot, bool)
	ListDeviceHealth() []DeviceHealthSnapshot
}

type RecoveryController interface {
	RegisterRemoteSKI(string)
	UnregisterRemoteSKI(string)
}

type RecoverySnapshot struct {
	State            RecoveryState
	Attempts         int
	FirstStaleAt     time.Time
	LastAttemptAt    time.Time
	NextAttemptAt    time.Time
	LastTransitionAt time.Time
}

type RecoveryTickResult struct {
	RestartRequired bool
	ExhaustedSKIs   []string
}

type recoveryRecord struct {
	RecoverySnapshot
}

// RecoverySupervisor owns one independent recovery state machine per SKI.
// Its mutex protects only in-memory transitions: entity invalidation,
// reconnect commands and logging always happen after the mutex is released.
type RecoverySupervisor struct {
	registry   RecoveryRegistry
	controller RecoveryController
	config     RecoveryConfig

	mu               sync.RWMutex
	records          map[string]*recoveryRecord
	restartSignalled bool
}

func NewRecoverySupervisor(registry RecoveryRegistry, controller RecoveryController, config RecoveryConfig) *RecoverySupervisor {
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 3
	}
	if config.BaseBackoff <= 0 {
		config.BaseBackoff = config.GracePeriod
	}
	if config.MaxBackoff < config.BaseBackoff {
		config.MaxBackoff = config.BaseBackoff
	}
	return &RecoverySupervisor{
		registry: registry, controller: controller, config: config,
		records: make(map[string]*recoveryRecord),
	}
}

func (s *RecoverySupervisor) Tick(now time.Time) RecoveryTickResult {
	if s == nil || s.registry == nil {
		return RecoveryTickResult{}
	}
	health := s.registry.ListDeviceHealth()
	stale := s.registry.StaleDevices(s.config.StaleThreshold, s.config.GracePeriod)
	staleSet := make(map[string]struct{}, len(stale))
	for _, ski := range stale {
		ski = NormalizeSKI(ski)
		staleSet[ski] = struct{}{}
		s.recover(now, ski)
	}

	for _, device := range health {
		ski := NormalizeSKI(device.SKI)
		if _, isStale := staleSet[ski]; isStale {
			continue
		}
		s.reconcileHealthy(now, device)
	}

	return s.restartDecision(health)
}

func (s *RecoverySupervisor) recover(now time.Time, ski string) {
	s.mu.Lock()
	record := s.recordLocked(ski, now)
	if record.State == RecoveryStateRecovering || record.State == RecoveryStateExhausted {
		s.mu.Unlock()
		return
	}
	if record.State == RecoveryStateGracePeriod && now.Before(record.NextAttemptAt) {
		s.mu.Unlock()
		return
	}
	if record.Attempts >= s.config.MaxAttempts {
		s.transitionLocked(record, RecoveryStateExhausted, now)
		snapshot := record.RecoverySnapshot
		s.mu.Unlock()
		s.logTransition(ski, snapshot)
		return
	}
	if record.Attempts == 0 && record.FirstStaleAt.IsZero() {
		record.FirstStaleAt = now
	}
	record.Attempts++
	record.LastAttemptAt = now
	s.transitionLocked(record, RecoveryStateRecovering, now)
	attempt := record.Attempts
	firstStaleAt := record.FirstStaleAt
	s.mu.Unlock()

	s.logStage(ski, "stale", attempt, now.Sub(firstStaleAt))
	s.registry.ClearEntities(ski)
	s.logStage(ski, "invalidate", attempt, 0)
	if s.controller != nil {
		s.controller.UnregisterRemoteSKI(ski)
		s.controller.RegisterRemoteSKI(ski)
	}
	s.logStage(ski, "reconnect", attempt, 0)

	s.mu.Lock()
	record = s.recordLocked(ski, now)
	if record.State == RecoveryStateRecovering && record.Attempts == attempt {
		record.NextAttemptAt = now.Add(s.backoff(attempt))
		s.transitionLocked(record, RecoveryStateGracePeriod, now)
	}
	snapshot := record.RecoverySnapshot
	s.mu.Unlock()
	s.logTransition(ski, snapshot)
}

func (s *RecoverySupervisor) reconcileHealthy(now time.Time, device DeviceHealthSnapshot) {
	ski := NormalizeSKI(device.SKI)
	s.mu.Lock()
	record := s.records[ski]
	if device.TrustKnown && !device.Trusted {
		if record == nil {
			record = s.recordLocked(ski, now)
		}
		s.transitionLocked(record, RecoveryStateUntrusted, now)
		s.mu.Unlock()
		return
	}
	if !device.Connected {
		if record == nil {
			record = s.recordLocked(ski, now)
		}
		s.transitionLocked(record, RecoveryStateDisconnected, now)
		s.mu.Unlock()
		return
	}
	if record == nil {
		state := RecoveryStateGracePeriod
		if device.MonitoringSuccessOnConnect {
			state = RecoveryStateHealthy
		}
		record = s.recordLocked(ski, now)
		s.transitionLocked(record, state, now)
		if state == RecoveryStateGracePeriod {
			record.NextAttemptAt = now.Add(s.config.GracePeriod)
		}
		s.mu.Unlock()
		return
	}
	lastAttempt := record.LastAttemptAt
	state := record.State
	retryAfterGrace := state == RecoveryStateGracePeriod && !now.Before(record.NextAttemptAt)
	s.mu.Unlock()

	monitoringRecovered := !lastAttempt.IsZero() && s.registry.MonitoringSuccessSince(ski, lastAttempt)
	if monitoringRecovered || (lastAttempt.IsZero() && device.MonitoringSuccessOnConnect) {
		s.mu.Lock()
		record = s.recordLocked(ski, now)
		record.Attempts = 0
		record.FirstStaleAt = time.Time{}
		record.LastAttemptAt = time.Time{}
		record.NextAttemptAt = time.Time{}
		s.transitionLocked(record, RecoveryStateHealthy, now)
		snapshot := record.RecoverySnapshot
		s.mu.Unlock()
		s.logTransition(ski, snapshot)
		return
	}
	if state == RecoveryStateExhausted {
		return
	}
	if retryAfterGrace {
		s.recover(now, ski)
		return
	}

	// A reconnect removes the device from the registry's stale set during its
	// connection grace period. Keep the per-device state explicit until either
	// a monitoring read succeeds or the supervisor-owned retry becomes due.
	s.mu.Lock()
	record = s.recordLocked(ski, now)
	s.transitionLocked(record, RecoveryStateGracePeriod, now)
	if record.NextAttemptAt.IsZero() {
		record.NextAttemptAt = now.Add(s.config.GracePeriod)
	}
	s.mu.Unlock()
}

func (s *RecoverySupervisor) restartDecision(health []DeviceHealthSnapshot) RecoveryTickResult {
	relevant := make([]string, 0, len(health))
	for _, device := range health {
		if device.Connected && (!device.TrustKnown || device.Trusted) {
			relevant = append(relevant, NormalizeSKI(device.SKI))
		}
	}
	sort.Strings(relevant)

	s.mu.Lock()
	defer s.mu.Unlock()
	exhausted := make([]string, 0, len(relevant))
	for _, ski := range relevant {
		if record := s.records[ski]; record != nil && record.State == RecoveryStateExhausted {
			exhausted = append(exhausted, ski)
		}
	}
	allExhausted := len(relevant) > 0 && len(exhausted) == len(relevant)
	restart := allExhausted && !s.restartSignalled
	if allExhausted {
		s.restartSignalled = true
	} else {
		s.restartSignalled = false
	}
	return RecoveryTickResult{RestartRequired: restart, ExhaustedSKIs: exhausted}
}

func (s *RecoverySupervisor) Snapshot(ski string, now time.Time) RecoverySnapshot {
	if s == nil {
		return RecoverySnapshot{State: RecoveryStateUnknown}
	}
	ski = NormalizeSKI(ski)
	s.mu.RLock()
	record := s.records[ski]
	if record != nil {
		result := record.RecoverySnapshot
		s.mu.RUnlock()
		return result
	}
	s.mu.RUnlock()
	if s.registry == nil {
		return RecoverySnapshot{State: RecoveryStateUnknown}
	}
	health, ok := s.registry.DeviceHealth(ski)
	if !ok {
		return RecoverySnapshot{State: RecoveryStateUnknown}
	}
	if !health.Connected {
		if health.TrustKnown && !health.Trusted {
			return RecoverySnapshot{State: RecoveryStateUntrusted, LastTransitionAt: health.LastTransitionAt}
		}
		return RecoverySnapshot{State: RecoveryStateDisconnected, LastTransitionAt: health.LastTransitionAt}
	}
	state := RecoveryStateGracePeriod
	if health.MonitoringSuccessOnConnect {
		state = RecoveryStateHealthy
	}
	return RecoverySnapshot{State: state, LastTransitionAt: health.LastTransitionAt}
}

func (s *RecoverySupervisor) recordLocked(ski string, now time.Time) *recoveryRecord {
	record := s.records[ski]
	if record == nil {
		record = &recoveryRecord{RecoverySnapshot: RecoverySnapshot{
			State: RecoveryStateHealthy, LastTransitionAt: now,
		}}
		s.records[ski] = record
	}
	return record
}

func (s *RecoverySupervisor) transitionLocked(record *recoveryRecord, state RecoveryState, now time.Time) {
	if record.State == state {
		return
	}
	record.State = state
	record.LastTransitionAt = now
}

func (s *RecoverySupervisor) backoff(attempt int) time.Duration {
	backoff := s.config.BaseBackoff
	for count := 1; count < attempt && backoff < s.config.MaxBackoff; count++ {
		backoff *= 2
		if backoff > s.config.MaxBackoff {
			backoff = s.config.MaxBackoff
		}
	}
	return backoff
}

func (s *RecoverySupervisor) logTransition(ski string, snapshot RecoverySnapshot) {
	retryDelay := time.Duration(0)
	if !snapshot.NextAttemptAt.IsZero() {
		retryDelay = snapshot.NextAttemptAt.Sub(snapshot.LastTransitionAt)
	}
	s.logStage(ski, string(snapshot.State), snapshot.Attempts, retryDelay)
}

func (s *RecoverySupervisor) logStage(ski, stage string, attempt int, duration time.Duration) {
	if duration < 0 {
		duration = 0
	}
	log.Printf(
		"monitoring recovery: ski=%s stage=%s attempt=%d duration=%s",
		ShortSKI(ski), stage, attempt, duration.Round(time.Second),
	)
}
