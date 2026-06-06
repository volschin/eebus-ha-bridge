package usecases

import (
	"errors"
	"log"
	"sync"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	featureclient "github.com/enbility/eebus-go/features/client"
	featureserver "github.com/enbility/eebus-go/features/server"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	eglpc "github.com/enbility/eebus-go/usecases/eg/lpc"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

const (
	heartbeatInterval   = 4 * time.Second
	heartbeatStaleAfter = 2 * time.Minute
)

// LPCWrapper wraps the eebus-go LPC (Limitation of Power Consumption) use case
// and routes events to the internal EventBus.
type LPCWrapper struct {
	uc          *eglpc.LPC
	localEntity spineapi.EntityLocalInterface
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
	debug       bool

	heartbeatMu      sync.RWMutex
	heartbeatRunning bool
	heartbeatSKI     string
	lastHeartbeat    time.Time
	stopHeartbeat    chan struct{}
}

var errLPCNotInitialized = errors.New("lpc use case not initialized")

// NewLPCWrapper creates a new LPCWrapper. Call Setup() before using the use case.
func NewLPCWrapper(bus *eebus.EventBus, registry *eebus.DeviceRegistry, debugEvents bool) *LPCWrapper {
	return &LPCWrapper{bus: bus, registry: registry, debug: debugEvents}
}

// Setup initialises the underlying eebus-go LPC use case for the given local entity.
func (w *LPCWrapper) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil {
		return
	}
	w.localEntity = localEntity
	w.uc = eglpc.NewLPC(localEntity, w.HandleEvent)
	if _, err := featureserver.NewFeature(model.FeatureTypeTypeDeviceDiagnosis, localEntity); err != nil {
		log.Printf("creating local DeviceDiagnosis feature failed: %v", err)
	}
	if _, err := featureserver.NewDeviceDiagnosis(localEntity); err != nil {
		log.Printf("creating local DeviceDiagnosis heartbeat server failed: %v", err)
	}
}

// UseCase returns the underlying eebus-go LPC use case (may be nil before Setup).
func (w *LPCWrapper) UseCase() *eglpc.LPC {
	return w.uc
}

// HandleEvent is the api.EntityEventCallback passed to eebus-go. It translates
// eebus-go event types to internal EventBus events.
func (w *LPCWrapper) HandleEvent(ski string, device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event eebusapi.EventType) {
	if w.debug {
		log.Printf(
			"[DEBUG] EEBUS LPC event received: ski=%s event=%s has_device=%t has_entity=%t",
			ski,
			event,
			device != nil,
			entity != nil,
		)
	}

	if w.registry != nil {
		w.registry.UpsertObservation(ski, device, entity, "lpc")
	}

	var eventType string
	switch event {
	case eglpc.DataUpdateLimit:
		eventType = "lpc.limit_updated"
	case eglpc.DataUpdateFailsafeConsumptionActivePowerLimit:
		eventType = "lpc.failsafe_power_updated"
	case eglpc.DataUpdateFailsafeDurationMinimum:
		eventType = "lpc.failsafe_duration_updated"
	case eglpc.UseCaseSupportUpdate:
		eventType = "lpc.use_case_support_updated"
	default:
		return
	}
	if w.bus != nil {
		w.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
	}
}

// ConsumptionLimit returns the current load control limit for the given remote entity.
func (w *LPCWrapper) ConsumptionLimit(entity spineapi.EntityRemoteInterface) (ucapi.LoadLimit, error) {
	if w.uc == nil {
		return ucapi.LoadLimit{}, errLPCNotInitialized
	}
	return w.uc.ConsumptionLimit(entity)
}

// WriteConsumptionLimit sends a new consumption limit to the given remote entity.
func (w *LPCWrapper) WriteConsumptionLimit(entity spineapi.EntityRemoteInterface, limit ucapi.LoadLimit) error {
	if w.uc == nil {
		return errLPCNotInitialized
	}
	_, err := w.uc.WriteConsumptionLimit(entity, limit, nil)
	return err
}

// FailsafeConsumptionActivePowerLimit returns the failsafe active power limit.
func (w *LPCWrapper) FailsafeConsumptionActivePowerLimit(entity spineapi.EntityRemoteInterface) (float64, error) {
	if w.uc == nil {
		return 0, errLPCNotInitialized
	}
	return w.uc.FailsafeConsumptionActivePowerLimit(entity)
}

// WriteFailsafeConsumptionActivePowerLimit sends a new failsafe active power limit.
func (w *LPCWrapper) WriteFailsafeConsumptionActivePowerLimit(entity spineapi.EntityRemoteInterface, value float64) error {
	if w.uc == nil {
		return errLPCNotInitialized
	}
	_, err := w.uc.WriteFailsafeConsumptionActivePowerLimit(entity, value)
	return err
}

// FailsafeDurationMinimum returns the minimum failsafe duration.
func (w *LPCWrapper) FailsafeDurationMinimum(entity spineapi.EntityRemoteInterface) (time.Duration, error) {
	if w.uc == nil {
		return 0, errLPCNotInitialized
	}
	return w.uc.FailsafeDurationMinimum(entity)
}

// WriteFailsafeDurationMinimum sends a new minimum failsafe duration (must be 2h–24h).
func (w *LPCWrapper) WriteFailsafeDurationMinimum(entity spineapi.EntityRemoteInterface, duration time.Duration) error {
	if w.uc == nil {
		return errLPCNotInitialized
	}
	_, err := w.uc.WriteFailsafeDurationMinimum(entity, duration)
	return err
}

// ConsumptionNominalMax returns the nominal maximum active power the controllable system
// can consume.
func (w *LPCWrapper) ConsumptionNominalMax(entity spineapi.EntityRemoteInterface) (float64, error) {
	if w.uc == nil {
		return 0, errLPCNotInitialized
	}
	return w.uc.ConsumptionNominalMax(entity)
}

// StartHeartbeat starts periodic DeviceDiagnosis heartbeat requests to the
// remote controllable system. If ski is empty, the first known remote entity is used.
func (w *LPCWrapper) StartHeartbeat(ski string) error {
	if w.localEntity == nil {
		return errLPCNotInitialized
	}
	if w.registry == nil {
		return errors.New("device registry not initialized")
	}
	if err := w.localEntity.HeartbeatManager().StartHeartbeat(); err != nil {
		return err
	}

	w.heartbeatMu.Lock()
	defer w.heartbeatMu.Unlock()
	w.heartbeatSKI = ski
	if w.heartbeatRunning {
		return nil
	}
	w.stopHeartbeat = make(chan struct{})
	w.heartbeatRunning = true
	go w.heartbeatLoop(w.stopHeartbeat)
	return nil
}

// StopHeartbeat stops the periodic heartbeat requests.
func (w *LPCWrapper) StopHeartbeat() error {
	w.heartbeatMu.Lock()
	defer w.heartbeatMu.Unlock()
	if !w.heartbeatRunning {
		if w.localEntity != nil {
			w.localEntity.HeartbeatManager().StopHeartbeat()
		}
		return nil
	}
	close(w.stopHeartbeat)
	w.stopHeartbeat = nil
	w.heartbeatRunning = false
	if w.localEntity != nil {
		w.localEntity.HeartbeatManager().StopHeartbeat()
	}
	return nil
}

// IsHeartbeatWithinDuration reports whether the last heartbeat request succeeded recently.
func (w *LPCWrapper) IsHeartbeatWithinDuration(_ spineapi.EntityRemoteInterface) bool {
	w.heartbeatMu.RLock()
	defer w.heartbeatMu.RUnlock()
	return w.heartbeatRunning && !w.lastHeartbeat.IsZero() && time.Since(w.lastHeartbeat) <= heartbeatStaleAfter
}

func (w *LPCWrapper) IsHeartbeatRunning() bool {
	w.heartbeatMu.RLock()
	defer w.heartbeatMu.RUnlock()
	localRunning := w.localEntity != nil && w.localEntity.HeartbeatManager().IsHeartbeatRunning()
	return w.heartbeatRunning || localRunning
}

func (w *LPCWrapper) heartbeatLoop(stop <-chan struct{}) {
	w.requestHeartbeatOnce()
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.requestHeartbeatOnce()
		case <-stop:
			return
		}
	}
}

func (w *LPCWrapper) requestHeartbeatOnce() {
	w.heartbeatMu.RLock()
	ski := w.heartbeatSKI
	w.heartbeatMu.RUnlock()

	entity := w.registry.FirstEntity(ski)
	if entity == nil {
		entity = w.registry.FirstAvailableEntity()
	}
	if entity == nil {
		if w.debug {
			log.Printf("[DEBUG] LPC heartbeat waiting for remote entity: ski=%s", ski)
		}
		return
	}

	diagnosis, err := featureclient.NewDeviceDiagnosis(w.localEntity, entity)
	if err != nil {
		log.Printf("creating DeviceDiagnosis heartbeat client failed: %v", err)
		return
	}
	if _, err := diagnosis.RequestHeartbeat(); err != nil {
		log.Printf("requesting EEBUS heartbeat failed: %v", err)
		return
	}

	w.heartbeatMu.Lock()
	w.lastHeartbeat = time.Now()
	w.heartbeatMu.Unlock()
	if w.debug {
		log.Printf("[DEBUG] Requested EEBUS heartbeat: ski=%s", ski)
	}
}
