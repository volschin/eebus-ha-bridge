package usecases

import (
	"errors"
	"log"

	eebusapi "github.com/enbility/eebus-go/api"
	mampc "github.com/enbility/eebus-go/usecases/ma/mpc"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

// MonitoringWrapper wraps the eebus-go MPC (Monitoring of Power Consumption) use case
// and routes events to the internal EventBus.
type MonitoringWrapper struct {
	uc       *mampc.MPC
	bus      *eebus.EventBus
	registry *eebus.DeviceRegistry
	debug    bool
}

var errMonitoringNotInitialized = errors.New("monitoring use case not initialized")

// NewMonitoringWrapper creates a new MonitoringWrapper. Call Setup() before using the use case.
func NewMonitoringWrapper(bus *eebus.EventBus, registry *eebus.DeviceRegistry, debugEvents bool) *MonitoringWrapper {
	return &MonitoringWrapper{bus: bus, registry: registry, debug: debugEvents}
}

// Setup initialises the underlying eebus-go MPC use case for the given local entity.
func (w *MonitoringWrapper) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil {
		return
	}
	w.uc = mampc.NewMPC(localEntity, w.HandleEvent)
}

// UseCase returns the underlying eebus-go MPC use case (may be nil before Setup).
func (w *MonitoringWrapper) UseCase() *mampc.MPC {
	return w.uc
}

// HandleEvent is the api.EntityEventCallback passed to eebus-go. It translates
// eebus-go event types to internal EventBus events.
func (w *MonitoringWrapper) HandleEvent(ski string, device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event eebusapi.EventType) {
	if w.debug {
		log.Printf(
			"[DEBUG] EEBUS monitoring event received: ski=%s event=%s has_device=%t has_entity=%t",
			ski,
			event,
			device != nil,
			entity != nil,
		)
	}

	if w.registry != nil {
		w.registry.UpsertObservation(ski, device, entity, "monitoring")
	}

	var eventType string
	switch event {
	case mampc.DataUpdatePower:
		eventType = "monitoring.power_updated"
	case mampc.DataUpdatePowerPerPhase:
		eventType = "monitoring.power_per_phase_updated"
	case mampc.DataUpdateEnergyConsumed:
		eventType = "monitoring.energy_consumed_updated"
	case mampc.DataUpdateEnergyProduced:
		eventType = "monitoring.energy_produced_updated"
	case mampc.DataUpdateCurrentsPerPhase:
		eventType = "monitoring.currents_per_phase_updated"
	case mampc.DataUpdateVoltagePerPhase:
		eventType = "monitoring.voltage_per_phase_updated"
	case mampc.DataUpdateFrequency:
		eventType = "monitoring.frequency_updated"
	case mampc.UseCaseSupportUpdate:
		eventType = "monitoring.use_case_support_updated"
	default:
		return
	}
	if w.bus != nil {
		w.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
	}
}

// Power returns the total momentary active power for the given remote entity.
func (w *MonitoringWrapper) Power(entity spineapi.EntityRemoteInterface) (float64, error) {
	if w.uc == nil {
		return 0, errMonitoringNotInitialized
	}
	return w.uc.Power(entity)
}

// PowerPerPhase returns the phase-specific momentary active power.
func (w *MonitoringWrapper) PowerPerPhase(entity spineapi.EntityRemoteInterface) ([]float64, error) {
	if w.uc == nil {
		return nil, errMonitoringNotInitialized
	}
	return w.uc.PowerPerPhase(entity)
}

// EnergyConsumed returns the total consumed energy.
func (w *MonitoringWrapper) EnergyConsumed(entity spineapi.EntityRemoteInterface) (float64, error) {
	if w.uc == nil {
		return 0, errMonitoringNotInitialized
	}
	return w.uc.EnergyConsumed(entity)
}

// EnergyProduced returns the total produced/fed-in energy.
func (w *MonitoringWrapper) EnergyProduced(entity spineapi.EntityRemoteInterface) (float64, error) {
	if w.uc == nil {
		return 0, errMonitoringNotInitialized
	}
	return w.uc.EnergyProduced(entity)
}

// CurrentPerPhase returns the phase-specific momentary current.
func (w *MonitoringWrapper) CurrentPerPhase(entity spineapi.EntityRemoteInterface) ([]float64, error) {
	if w.uc == nil {
		return nil, errMonitoringNotInitialized
	}
	return w.uc.CurrentPerPhase(entity)
}

// VoltagePerPhase returns the phase-specific voltage.
func (w *MonitoringWrapper) VoltagePerPhase(entity spineapi.EntityRemoteInterface) ([]float64, error) {
	if w.uc == nil {
		return nil, errMonitoringNotInitialized
	}
	return w.uc.VoltagePerPhase(entity)
}

// Frequency returns the power network frequency.
func (w *MonitoringWrapper) Frequency(entity spineapi.EntityRemoteInterface) (float64, error) {
	if w.uc == nil {
		return 0, errMonitoringNotInitialized
	}
	return w.uc.Frequency(entity)
}
