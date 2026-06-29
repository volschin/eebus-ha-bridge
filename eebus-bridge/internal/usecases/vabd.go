package usecases

import (
	"errors"
	"log"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/features/server"
	usecase "github.com/enbility/eebus-go/usecases/usecase"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

// vabdUseCaseSupportUpdate is emitted by the embedded UseCaseBase when a remote
// VisualizationAppliance (such as the Vaillant VR940) discovers and binds to our
// local battery system.
const vabdUseCaseSupportUpdate eebusapi.EventType = "bridge-vabd-provider-support-update"

var errVABDNotInitialized = errors.New("vabd provider not initialized")

// VABDProvider is a SPIKE provider implementation of the EEBUS "Visualization of
// Aggregated Battery Data" (VABD) use case. eebus-go ships VABD only as the reader
// (CEM/VisualizationAppliance) side; a device like the Vaillant VR940 advertises
// that VisualizationAppliance role and expects some other device to act as the
// BatterySystem data provider. This wrapper makes the bridge that provider: it
// advertises the VABD use case with actor BatterySystem on a local
// ElectricityStorageSystem entity and serves aggregated battery data via a
// server-side Measurement feature, so the heat pump / app can display the home's
// battery state (§1.3.3).
//
// Advertises the four VABD scenarios — 1 (momentary AC total power, mandatory),
// 2 (charged energy), 3 (discharged energy) and 4 (state of charge, mandatory).
// Display-only; complements the high-value MGCP grid feed. Experimental; gated
// behind config.Experimental.VABDProvider. See docs/eebus-vaillant-improvements.md.
type VABDProvider struct {
	*usecase.UseCaseBase
	bus           *eebus.EventBus
	batteryEntity spineapi.EntityLocalInterface
	meas          *server.Measurement
	powerID       *model.MeasurementIdType // scenario 1: AC total power (W); negative = charge
	chargedID     *model.MeasurementIdType // scenario 2: total charged energy (Wh)
	dischargedID  *model.MeasurementIdType // scenario 3: total discharged energy (Wh)
	socID         *model.MeasurementIdType // scenario 4: state of charge (%)
	debug         bool
}

// NewVABDProvider builds the provider on the given local battery-system entity
// (BridgeService.BatteryEntity()). Call Service.AddUseCase with UseCase() to
// register the features and advertise support.
func NewVABDProvider(batteryEntity spineapi.EntityLocalInterface, bus *eebus.EventBus, debug bool) *VABDProvider {
	p := &VABDProvider{bus: bus, batteryEntity: batteryEntity, debug: debug}

	// Remote consumers are VisualizationAppliances modelled as a CEM entity (the
	// VR940 advertises actor=VisualizationAppliance for VABD). Every scenario is
	// backed by the Measurement + ElectricalConnection server features.
	validActorTypes := []model.UseCaseActorType{model.UseCaseActorTypeVisualizationAppliance}
	validEntityTypes := []model.EntityTypeType{model.EntityTypeTypeCEM}
	measFeatures := []model.FeatureTypeType{
		model.FeatureTypeTypeMeasurement,
		model.FeatureTypeTypeElectricalConnection,
	}
	scenarios := []eebusapi.UseCaseScenario{
		{Scenario: model.UseCaseScenarioSupportType(1), Mandatory: true, ServerFeatures: measFeatures},
		{Scenario: model.UseCaseScenarioSupportType(2), ServerFeatures: measFeatures},
		{Scenario: model.UseCaseScenarioSupportType(3), ServerFeatures: measFeatures},
		{Scenario: model.UseCaseScenarioSupportType(4), Mandatory: true, ServerFeatures: measFeatures},
	}

	p.UseCaseBase = usecase.NewUseCaseBase(
		batteryEntity,
		model.UseCaseActorTypeBatterySystem,
		model.UseCaseNameTypeVisualizationOfAggregatedBatteryData,
		"1.0.1",
		"release",
		scenarios,
		p.handleEvent,
		vabdUseCaseSupportUpdate,
		validActorTypes,
		validEntityTypes,
	)
	return p
}

// UseCase returns the provider for registration via Service.AddUseCase, which calls
// AddFeatures() then AddUseCase().
func (p *VABDProvider) UseCase() eebusapi.UseCaseInterface { return p }

// AddFeatures attaches the server-side features to the battery entity and declares
// the power/energy/state-of-charge measurements. Called by Service.AddUseCase
// before AddUseCase().
func (p *VABDProvider) AddFeatures() {
	// server.New* only look up an existing server feature on the entity; they do
	// not create it. Add them first.
	p.batteryEntity.GetOrAddFeature(model.FeatureTypeTypeMeasurement, model.RoleTypeServer)
	p.batteryEntity.GetOrAddFeature(model.FeatureTypeTypeElectricalConnection, model.RoleTypeServer)

	meas, err := server.NewMeasurement(p.batteryEntity)
	if err != nil {
		log.Printf("[VABD] creating Measurement server feature failed: %v", err)
		return
	}
	p.meas = meas

	// Scenario 1: momentary total AC power at the battery.
	p.powerID = meas.AddDescription(model.MeasurementDescriptionDataType{
		MeasurementType: util.Ptr(model.MeasurementTypeTypePower),
		CommodityType:   util.Ptr(model.CommodityTypeTypeElectricity),
		ScopeType:       util.Ptr(model.ScopeTypeTypeACPowerTotal),
		Unit:            util.Ptr(model.UnitOfMeasurementTypeW),
	})
	if p.powerID == nil {
		log.Printf("[VABD] adding power measurement description failed")
		return
	}

	// Scenario 2: total energy charged into the battery.
	p.chargedID = meas.AddDescription(model.MeasurementDescriptionDataType{
		MeasurementType: util.Ptr(model.MeasurementTypeTypeEnergy),
		CommodityType:   util.Ptr(model.CommodityTypeTypeElectricity),
		ScopeType:       util.Ptr(model.ScopeTypeTypeCharge),
		Unit:            util.Ptr(model.UnitOfMeasurementTypeWh),
	})
	if p.chargedID == nil {
		log.Printf("[VABD] adding charged energy measurement description failed")
	}

	// Scenario 3: total energy discharged from the battery.
	p.dischargedID = meas.AddDescription(model.MeasurementDescriptionDataType{
		MeasurementType: util.Ptr(model.MeasurementTypeTypeEnergy),
		CommodityType:   util.Ptr(model.CommodityTypeTypeElectricity),
		ScopeType:       util.Ptr(model.ScopeTypeTypeDischarge),
		Unit:            util.Ptr(model.UnitOfMeasurementTypeWh),
	})
	if p.dischargedID == nil {
		log.Printf("[VABD] adding discharged energy measurement description failed")
	}

	// Scenario 4: state of charge as a percentage.
	p.socID = meas.AddDescription(model.MeasurementDescriptionDataType{
		MeasurementType: util.Ptr(model.MeasurementTypeTypePercentage),
		ScopeType:       util.Ptr(model.ScopeTypeTypeStateOfCharge),
		Unit:            util.Ptr(model.UnitOfMeasurementTypepct),
	})
	if p.socID == nil {
		log.Printf("[VABD] adding state-of-charge measurement description failed")
	}

	// ElectricalConnection is mandatory for scenario 1; provide a single
	// connection and link the power measurement to it.
	ec, err := server.NewElectricalConnection(p.batteryEntity)
	if err != nil {
		log.Printf("[VABD] creating ElectricalConnection server feature failed: %v", err)
		return
	}
	connID := util.Ptr(model.ElectricalConnectionIdType(0))
	if err := ec.AddDescription(model.ElectricalConnectionDescriptionDataType{
		ElectricalConnectionId: connID,
		PowerSupplyType:        util.Ptr(model.ElectricalConnectionVoltageTypeTypeAc),
		AcConnectedPhases:      util.Ptr(uint(3)),
	}); err != nil {
		log.Printf("[VABD] adding electrical connection description failed: %v", err)
	}
	if id := ec.AddParameterDescription(model.ElectricalConnectionParameterDescriptionDataType{
		ElectricalConnectionId: connID,
		MeasurementId:          p.powerID,
		AcMeasuredPhases:       util.Ptr(model.ElectricalConnectionPhaseNameTypeAbc),
	}); id == nil {
		log.Printf("[VABD] adding electrical connection parameter description failed")
	}

	log.Printf("[VABD] battery-system provider features added (power=%d charged=%v discharged=%v soc=%v)",
		*p.powerID, idVal(p.chargedID), idVal(p.dischargedID), idVal(p.socID))
}

// publishMeasurement is the shared path for pushing one measurement value.
func (p *VABDProvider) publishMeasurement(id *model.MeasurementIdType, value float64) error {
	if p.meas == nil || id == nil {
		return errVABDNotInitialized
	}
	return p.meas.UpdateDataForId(model.MeasurementDataType{
		ValueType: util.Ptr(model.MeasurementValueTypeTypeValue),
		Value:     model.NewScaledNumberType(value),
	}, nil, *id)
}

// PublishPower pushes the momentary total battery power (W; scenario 1).
func (p *VABDProvider) PublishPower(watts float64) error {
	if err := p.publishMeasurement(p.powerID, watts); err != nil {
		return err
	}
	if p.debug {
		log.Printf("[VABD] published battery power: %.1f W", watts)
	}
	return nil
}

// PublishEnergyCharged pushes the cumulative charged energy in Wh (scenario 2).
func (p *VABDProvider) PublishEnergyCharged(wh float64) error {
	if err := p.publishMeasurement(p.chargedID, wh); err != nil {
		return err
	}
	if p.debug {
		log.Printf("[VABD] published battery charged energy: %.1f Wh", wh)
	}
	return nil
}

// PublishEnergyDischarged pushes the cumulative discharged energy in Wh (scenario 3).
func (p *VABDProvider) PublishEnergyDischarged(wh float64) error {
	if err := p.publishMeasurement(p.dischargedID, wh); err != nil {
		return err
	}
	if p.debug {
		log.Printf("[VABD] published battery discharged energy: %.1f Wh", wh)
	}
	return nil
}

// PublishStateOfCharge pushes the battery state of charge in percent (scenario 4).
func (p *VABDProvider) PublishStateOfCharge(pct float64) error {
	if err := p.publishMeasurement(p.socID, pct); err != nil {
		return err
	}
	if p.debug {
		log.Printf("[VABD] published battery state of charge: %.1f %%", pct)
	}
	return nil
}

// handleEvent receives UseCaseBase notifications. For this spike it only logs when a
// remote VisualizationAppliance binds to the provider.
func (p *VABDProvider) handleEvent(ski string, _ spineapi.DeviceRemoteInterface, _ spineapi.EntityRemoteInterface, event eebusapi.EventType) {
	if event == vabdUseCaseSupportUpdate {
		log.Printf("[VABD] consumer support update from ski=%s", ski)
		if p.bus != nil {
			p.bus.Publish(eebus.Event{SKI: ski, Type: "vabd.consumer_updated"})
		}
	}
}
