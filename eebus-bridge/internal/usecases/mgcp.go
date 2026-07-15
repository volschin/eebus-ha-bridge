package usecases

import (
	"errors"
	"fmt"
	"log"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/features/server"
	usecase "github.com/enbility/eebus-go/usecases/usecase"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

// mgcpUseCaseSupportUpdate is emitted by the embedded UseCaseBase when the set of
// remote entities that consume this use case changes (i.e. when a MonitoringAppliance
// such as the Vaillant VR940 discovers and binds to our grid-connection-point).
const mgcpUseCaseSupportUpdate eebusapi.EventType = "bridge-mgcp-provider-support-update"

// MGCP scenario numbers (UseCaseScenarioSupportType). Scenario indices are
// use-case-scoped per the EEBUS UC spec — the same integer means different things
// in another use case — so name them here rather than passing magic numbers.
const (
	mgcpScenarioMomentaryPower model.UseCaseScenarioSupportType = 2 // momentary AC total power at the grid connection point
	mgcpScenarioFeedInEnergy   model.UseCaseScenarioSupportType = 3 // total energy fed into the grid (export)
	mgcpScenarioConsumedEnergy model.UseCaseScenarioSupportType = 4 // total energy consumed from the grid (import)
)

var errMGCPNotInitialized = errors.New("mgcp provider not initialized")

// MGCPProvider is a SPIKE provider implementation of the EEBUS "Monitoring of Grid
// Connection Point" (MGCP) use case. eebus-go ships MGCP only as the consumer
// (MonitoringAppliance) side; a heat pump like the Vaillant VR940 advertises that
// consumer role and expects some other device to act as the GridConnectionPoint
// data provider. This wrapper makes the bridge that provider: it advertises the
// MGCP use case with actor GridConnectionPoint on a local
// GridConnectionPointOfPremises entity and serves grid power via a server-side
// Measurement feature, so the heat pump can read the grid / PV-surplus situation.
//
// Advertises the three mandatory MGCP scenarios — 2 (momentary AC total power),
// 3 (total grid feed-in energy) and 4 (total grid consumed energy) — because a
// consumer such as the VR940 ignores a grid source that does not expose the full
// mandatory set. See docs/eebus-vaillant-improvements.md. Experimental; gated
// behind config.Experimental.MGCPProvider.
type MGCPProvider struct {
	*usecase.UseCaseBase
	bus        *eebus.EventBus
	gridEntity spineapi.EntityLocalInterface
	meas       *server.Measurement
	powerID    *model.MeasurementIdType // scenario 2: AC total power (W)
	feedInID   *model.MeasurementIdType // scenario 3: total grid feed-in energy (Wh)
	consumedID *model.MeasurementIdType // scenario 4: total grid consumed energy (Wh)
	debug      bool
}

// NewMGCPProvider builds the provider on the given local grid-connection-point
// entity (BridgeService.GridEntity()). Call Service.AddUseCase with UseCase() to
// register the features and advertise support.
func NewMGCPProvider(gridEntity spineapi.EntityLocalInterface, bus *eebus.EventBus, debug bool) *MGCPProvider {
	p := &MGCPProvider{bus: bus, gridEntity: gridEntity, debug: debug}

	// Remote consumers are MonitoringAppliances; on EEBUS the appliance entity is
	// typically modelled as a CEM. Scenarios 2 (momentary power), 3 (feed-in energy)
	// and 4 (consumed energy) are the mandatory set, each backed by the Measurement
	// + ElectricalConnection server features.
	validActorTypes := []model.UseCaseActorType{model.UseCaseActorTypeMonitoringAppliance}
	validEntityTypes := []model.EntityTypeType{model.EntityTypeTypeCEM}
	mandatoryFeatures := []model.FeatureTypeType{
		model.FeatureTypeTypeMeasurement,
		model.FeatureTypeTypeElectricalConnection,
	}
	scenarios := []eebusapi.UseCaseScenario{
		{Scenario: mgcpScenarioMomentaryPower, Mandatory: true, ServerFeatures: mandatoryFeatures},
		{Scenario: mgcpScenarioFeedInEnergy, Mandatory: true, ServerFeatures: mandatoryFeatures},
		{Scenario: mgcpScenarioConsumedEnergy, Mandatory: true, ServerFeatures: mandatoryFeatures},
	}

	p.UseCaseBase = usecase.NewUseCaseBase(
		gridEntity,
		model.UseCaseActorTypeGridConnectionPoint,
		model.UseCaseNameTypeMonitoringOfGridConnectionPoint,
		"1.0.0",
		"release",
		scenarios,
		p.handleEvent,
		mgcpUseCaseSupportUpdate,
		validActorTypes,
		validEntityTypes,
		false,
	)
	return p
}

// UseCase returns the provider for registration via Service.AddUseCase, which calls
// AddFeatures() then AddUseCase().
func (p *MGCPProvider) UseCase() eebusapi.UseCaseInterface { return p }

// AddFeatures attaches the server-side features to the grid entity and declares the
// AC-total-power measurement. Called by Service.AddUseCase before AddUseCase().
func (p *MGCPProvider) AddFeatures() error {
	// server.NewMeasurement/NewElectricalConnection only look up an existing
	// server feature on the entity; they do not create it. Add them first.
	p.gridEntity.GetOrAddFeature(model.FeatureTypeTypeMeasurement, model.RoleTypeServer)
	p.gridEntity.GetOrAddFeature(model.FeatureTypeTypeElectricalConnection, model.RoleTypeServer)

	meas, err := server.NewMeasurement(p.gridEntity)
	if err != nil {
		return fmt.Errorf("[MGCP] creating Measurement server feature failed: %w", err)
	}
	p.meas = meas

	// Scenario 2: momentary total active power at the grid connection point.
	// Negative = export / PV surplus per MGCP convention.
	p.powerID = meas.AddDescription(model.MeasurementDescriptionDataType{
		MeasurementType: util.Ptr(model.MeasurementTypeTypePower),
		CommodityType:   util.Ptr(model.CommodityTypeTypeElectricity),
		ScopeType:       util.Ptr(model.ScopeTypeTypeACPowerTotal),
		Unit:            util.Ptr(model.UnitOfMeasurementTypeW),
	})
	if p.powerID == nil {
		return errors.New("[MGCP] adding power measurement description failed")
	}

	// Scenario 3: total energy fed into the grid (export). ScopeType GridFeedIn,
	// unit Wh — matches what the eebus-go MGCP consumer reads.
	p.feedInID = meas.AddDescription(model.MeasurementDescriptionDataType{
		MeasurementType: util.Ptr(model.MeasurementTypeTypeEnergy),
		CommodityType:   util.Ptr(model.CommodityTypeTypeElectricity),
		ScopeType:       util.Ptr(model.ScopeTypeTypeGridFeedIn),
		Unit:            util.Ptr(model.UnitOfMeasurementTypeWh),
	})
	if p.feedInID == nil {
		log.Printf("[MGCP] adding feed-in energy measurement description failed")
	}

	// Scenario 4: total energy consumed from the grid (import). ScopeType
	// GridConsumption, unit Wh.
	p.consumedID = meas.AddDescription(model.MeasurementDescriptionDataType{
		MeasurementType: util.Ptr(model.MeasurementTypeTypeEnergy),
		CommodityType:   util.Ptr(model.CommodityTypeTypeElectricity),
		ScopeType:       util.Ptr(model.ScopeTypeTypeGridConsumption),
		Unit:            util.Ptr(model.UnitOfMeasurementTypeWh),
	})
	if p.consumedID == nil {
		log.Printf("[MGCP] adding consumed energy measurement description failed")
	}

	// ElectricalConnection is mandatory for scenario 2; provide a single connection
	// and a parameter that links the power measurement to it so the consumer can
	// resolve what the measurement refers to.
	ec, err := server.NewElectricalConnection(p.gridEntity)
	if err != nil {
		return fmt.Errorf("[MGCP] creating ElectricalConnection server feature failed: %w", err)
	}
	connID := util.Ptr(model.ElectricalConnectionIdType(0))
	if err := ec.AddDescription(model.ElectricalConnectionDescriptionDataType{
		ElectricalConnectionId: connID,
		PowerSupplyType:        util.Ptr(model.ElectricalConnectionVoltageTypeTypeAc),
		AcConnectedPhases:      util.Ptr(uint(3)),
	}); err != nil {
		log.Printf("[MGCP] adding electrical connection description failed: %v", err)
	}
	if id := ec.AddParameterDescription(model.ElectricalConnectionParameterDescriptionDataType{
		ElectricalConnectionId: connID,
		MeasurementId:          p.powerID,
		AcMeasuredPhases:       util.Ptr(model.ElectricalConnectionPhaseNameTypeAbc),
	}); id == nil {
		log.Printf("[MGCP] adding electrical connection parameter description failed")
	}

	log.Printf("[MGCP] grid-connection-point provider features added (power=%d feedIn=%v consumed=%v)",
		*p.powerID, idVal(p.feedInID), idVal(p.consumedID))
	return nil
}

func idVal(id *model.MeasurementIdType) int {
	if id == nil {
		return -1
	}
	return int(*id)
}

// publishMeasurement is the shared path for pushing one measurement value.
func (p *MGCPProvider) publishMeasurement(id *model.MeasurementIdType, value float64) error {
	if p.meas == nil || id == nil {
		return errMGCPNotInitialized
	}
	return p.meas.UpdateDataForIds([]eebusapi.MeasurementDataForID{{
		Data: model.MeasurementDataType{
			ValueType: util.Ptr(model.MeasurementValueTypeTypeValue),
			Value:     model.NewScaledNumberType(value),
		},
		Id: *id,
	}})
}

// PublishPower pushes the momentary total grid power (W; negative = export/surplus,
// scenario 2). Returns an error if the provider was not set up.
func (p *MGCPProvider) PublishPower(watts float64) error {
	if err := p.publishMeasurement(p.powerID, watts); err != nil {
		return err
	}
	if p.debug {
		log.Printf("[MGCP] published grid power: %.1f W", watts)
	}
	return nil
}

// PublishEnergyFeedIn pushes the cumulative grid feed-in (export) energy in Wh
// (scenario 3).
func (p *MGCPProvider) PublishEnergyFeedIn(wh float64) error {
	if err := p.publishMeasurement(p.feedInID, wh); err != nil {
		return err
	}
	if p.debug {
		log.Printf("[MGCP] published grid feed-in energy: %.1f Wh", wh)
	}
	return nil
}

// PublishEnergyConsumed pushes the cumulative grid consumed (import) energy in Wh
// (scenario 4).
func (p *MGCPProvider) PublishEnergyConsumed(wh float64) error {
	if err := p.publishMeasurement(p.consumedID, wh); err != nil {
		return err
	}
	if p.debug {
		log.Printf("[MGCP] published grid consumed energy: %.1f Wh", wh)
	}
	return nil
}

// handleEvent receives UseCaseBase notifications. For this spike it only logs when a
// remote MonitoringAppliance binds to the provider, which is the signal that the
// VR940 has discovered our grid-connection-point.
func (p *MGCPProvider) handleEvent(ski string, _ spineapi.DeviceRemoteInterface, _ spineapi.EntityRemoteInterface, event eebusapi.EventType) {
	if event == mgcpUseCaseSupportUpdate {
		log.Printf("[MGCP] consumer support update from ski=%s", ski)
		if p.bus != nil {
			p.bus.Publish(eebus.Event{SKI: ski, Type: eebus.EventTypeMGCPConsumerUpdated})
		}
	}
}
