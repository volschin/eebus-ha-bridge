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

// mgcpUseCaseSupportUpdate is emitted by the embedded UseCaseBase when the set of
// remote entities that consume this use case changes (i.e. when a MonitoringAppliance
// such as the Vaillant VR940 discovers and binds to our grid-connection-point).
const mgcpUseCaseSupportUpdate eebusapi.EventType = "bridge-mgcp-provider-support-update"

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
// Only AC total power (MGCP scenario 2) is implemented for now — enough to validate
// that a real VR940 discovers, binds and reads the value. See
// docs/eebus-vaillant-improvements.md. Experimental; gated behind
// config.Experimental.MGCPProvider.
type MGCPProvider struct {
	*usecase.UseCaseBase
	bus        *eebus.EventBus
	gridEntity spineapi.EntityLocalInterface
	meas       *server.Measurement
	powerID    *model.MeasurementIdType
	debug      bool
}

// NewMGCPProvider builds the provider on the given local grid-connection-point
// entity (BridgeService.GridEntity()). Call Service.AddUseCase with UseCase() to
// register the features and advertise support.
func NewMGCPProvider(gridEntity spineapi.EntityLocalInterface, bus *eebus.EventBus, debug bool) *MGCPProvider {
	p := &MGCPProvider{bus: bus, gridEntity: gridEntity, debug: debug}

	// Remote consumers are MonitoringAppliances; on EEBUS the appliance entity is
	// typically modelled as a CEM. Scenario 2 (momentary grid power) is the
	// mandatory minimum and requires the Measurement + ElectricalConnection server
	// features.
	validActorTypes := []model.UseCaseActorType{model.UseCaseActorTypeMonitoringAppliance}
	validEntityTypes := []model.EntityTypeType{model.EntityTypeTypeCEM}
	scenarios := []eebusapi.UseCaseScenario{
		{
			Scenario:  model.UseCaseScenarioSupportType(2),
			Mandatory: true,
			ServerFeatures: []model.FeatureTypeType{
				model.FeatureTypeTypeMeasurement,
				model.FeatureTypeTypeElectricalConnection,
			},
		},
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
	)
	return p
}

// UseCase returns the provider for registration via Service.AddUseCase, which calls
// AddFeatures() then AddUseCase().
func (p *MGCPProvider) UseCase() eebusapi.UseCaseInterface { return p }

// AddFeatures attaches the server-side features to the grid entity and declares the
// AC-total-power measurement. Called by Service.AddUseCase before AddUseCase().
func (p *MGCPProvider) AddFeatures() {
	meas, err := server.NewMeasurement(p.gridEntity)
	if err != nil {
		log.Printf("[MGCP] creating Measurement server feature failed: %v", err)
		return
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
		log.Printf("[MGCP] adding power measurement description failed")
		return
	}

	// ElectricalConnection is mandatory for scenario 2; provide a single connection
	// and a parameter that links the power measurement to it so the consumer can
	// resolve what the measurement refers to.
	ec, err := server.NewElectricalConnection(p.gridEntity)
	if err != nil {
		log.Printf("[MGCP] creating ElectricalConnection server feature failed: %v", err)
		return
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

	log.Printf("[MGCP] grid-connection-point provider features added (power measurementId=%d)", *p.powerID)
}

// PublishPower pushes the momentary total grid power (W; negative = export/surplus)
// to subscribed consumers. Returns an error if the provider was not set up.
func (p *MGCPProvider) PublishPower(watts float64) error {
	if p.meas == nil || p.powerID == nil {
		return errMGCPNotInitialized
	}
	err := p.meas.UpdateDataForId(model.MeasurementDataType{
		ValueType: util.Ptr(model.MeasurementValueTypeTypeValue),
		Value:     model.NewScaledNumberType(watts),
	}, nil, *p.powerID)
	if err != nil {
		return err
	}
	if p.debug {
		log.Printf("[MGCP] published grid power: %.1f W", watts)
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
			p.bus.Publish(eebus.Event{SKI: ski, Type: "mgcp.consumer_updated"})
		}
	}
}
