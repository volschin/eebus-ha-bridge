package usecases

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/features/server"
	usecase "github.com/enbility/eebus-go/usecases/usecase"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

// vapdUseCaseSupportUpdate is emitted by the embedded UseCaseBase when a remote
// VisualizationAppliance (such as the Vaillant VR940) discovers and binds to our
// local PV system.
const vapdUseCaseSupportUpdate eebusapi.EventType = "bridge-vapd-provider-support-update"

// VAPD scenario numbers (UseCaseScenarioSupportType). Scenario indices are
// use-case-scoped per the EEBUS UC spec, so name them rather than passing magic
// numbers into the scenario declarations.
const (
	vapdScenarioPeakPower      model.UseCaseScenarioSupportType = 1 // nominal peak power of the PV system (DeviceConfiguration)
	vapdScenarioMomentaryPower model.UseCaseScenarioSupportType = 2 // momentary total AC power produced by the PV system
	vapdScenarioYieldEnergy    model.UseCaseScenarioSupportType = 3 // total AC yield energy produced over the system's lifetime
)

var errVAPDNotInitialized = errors.New("vapd provider not initialized")

// VAPDProvider is a SPIKE provider implementation of the EEBUS "Visualization of
// Aggregated Photovoltaic Data" (VAPD) use case. eebus-go ships VAPD only as the
// reader (CEM/VisualizationAppliance) side; a device like the Vaillant VR940
// advertises that VisualizationAppliance role and expects some other device to act
// as the PVSystem data provider. This wrapper makes the bridge that provider: it
// advertises the VAPD use case with actor PVSystem on a local PVSystem entity and
// serves aggregated PV production via server-side Measurement + DeviceConfiguration
// features, so the heat pump / app can display the home's PV data (§1.3.3).
//
// Advertises all three mandatory VAPD scenarios — 1 (nominal peak power via
// DeviceConfiguration), 2 (momentary AC total power) and 3 (total AC yield energy).
// Display-only; complements the high-value MGCP grid feed. Experimental; gated
// behind config.Experimental.VAPDProvider. See docs/eebus-vaillant-improvements.md.
type VAPDProvider struct {
	*usecase.UseCaseBase
	bus       *eebus.EventBus
	pvEntity  spineapi.EntityLocalInterface
	meas      measurementServer
	publisher serializedMeasurementPublisher
	devConf   deviceConfigurationServer
	powerID   *model.MeasurementIdType            // scenario 2: AC total power (W)
	yieldID   *model.MeasurementIdType            // scenario 3: total AC yield energy (Wh)
	peakID    *model.DeviceConfigurationKeyIdType // scenario 1: nominal peak power (W)
	debug     bool

	snapshotMu      sync.Mutex
	snapshot        *PVSnapshot
	snapshotVersion uint64
	expiryTimer     *time.Timer
}

// NewVAPDProvider builds the provider on the given local PV-system entity
// (BridgeService.PVEntity()). Call Service.AddUseCase with UseCase() to register
// the features and advertise support.
func NewVAPDProvider(pvEntity spineapi.EntityLocalInterface, bus *eebus.EventBus, debug bool) *VAPDProvider {
	p := &VAPDProvider{bus: bus, pvEntity: pvEntity, debug: debug}

	// Remote consumers are VisualizationAppliances modelled as a CEM entity (the
	// VR940 advertises actor=VisualizationAppliance for VAPD). Scenario 1 needs
	// DeviceConfiguration; scenarios 2 and 3 need Measurement + ElectricalConnection.
	validActorTypes := []model.UseCaseActorType{model.UseCaseActorTypeVisualizationAppliance}
	validEntityTypes := []model.EntityTypeType{model.EntityTypeTypeCEM}
	measFeatures := []model.FeatureTypeType{
		model.FeatureTypeTypeMeasurement,
		model.FeatureTypeTypeElectricalConnection,
	}
	scenarios := []eebusapi.UseCaseScenario{
		{Scenario: vapdScenarioPeakPower, Mandatory: true, ServerFeatures: []model.FeatureTypeType{model.FeatureTypeTypeDeviceConfiguration}},
		{Scenario: vapdScenarioMomentaryPower, Mandatory: true, ServerFeatures: measFeatures},
		{Scenario: vapdScenarioYieldEnergy, Mandatory: true, ServerFeatures: measFeatures},
	}

	p.UseCaseBase = usecase.NewUseCaseBase(
		pvEntity,
		model.UseCaseActorTypePVSystem,
		model.UseCaseNameTypeVisualizationOfAggregatedPhotovoltaicData,
		"1.0.1",
		"release",
		scenarios,
		p.handleEvent,
		vapdUseCaseSupportUpdate,
		validActorTypes,
		validEntityTypes,
		false,
	)
	return p
}

// UseCase returns the provider for registration via Service.AddUseCase, which calls
// AddFeatures() then AddUseCase().
func (p *VAPDProvider) UseCase() eebusapi.UseCaseInterface { return p }

// AddFeatures attaches the server-side features to the PV entity and declares the
// power/yield measurements and the peak-power configuration key. Called by
// Service.AddUseCase before AddUseCase().
func (p *VAPDProvider) AddFeatures() error {
	// server.New* only look up an existing server feature on the entity; they do
	// not create it. Add them first.
	p.pvEntity.GetOrAddFeature(model.FeatureTypeTypeElectricalConnection, model.RoleTypeServer)
	p.pvEntity.GetOrAddFeature(model.FeatureTypeTypeDeviceConfiguration, model.RoleTypeServer)

	meas, err := setupProviderMeasurementServer(p.pvEntity, "VAPD")
	if err != nil {
		return err
	}
	p.meas = meas

	// Scenario 2: momentary total AC power produced by the PV system.
	p.powerID = meas.AddDescription(model.MeasurementDescriptionDataType{
		MeasurementType: util.Ptr(model.MeasurementTypeTypePower),
		CommodityType:   util.Ptr(model.CommodityTypeTypeElectricity),
		ScopeType:       util.Ptr(model.ScopeTypeTypeACPowerTotal),
		Unit:            util.Ptr(model.UnitOfMeasurementTypeW),
	})
	if p.powerID == nil {
		return errors.New("[VAPD] adding power measurement description failed")
	}

	// Scenario 3: total AC yield energy produced over the system's lifetime.
	p.yieldID = meas.AddDescription(model.MeasurementDescriptionDataType{
		MeasurementType: util.Ptr(model.MeasurementTypeTypeEnergy),
		CommodityType:   util.Ptr(model.CommodityTypeTypeElectricity),
		ScopeType:       util.Ptr(model.ScopeTypeTypeACYieldTotal),
		Unit:            util.Ptr(model.UnitOfMeasurementTypeWh),
	})
	if p.yieldID == nil {
		log.Printf("[VAPD] adding yield energy measurement description failed")
	}

	// ElectricalConnection is mandatory for scenarios 2/3; provide a single
	// connection and link the power measurement to it.
	ec, err := server.NewElectricalConnection(p.pvEntity)
	if err != nil {
		return fmt.Errorf("[VAPD] creating ElectricalConnection server feature failed: %w", err)
	}
	connID := util.Ptr(model.ElectricalConnectionIdType(0))
	if err := ec.AddDescription(model.ElectricalConnectionDescriptionDataType{
		ElectricalConnectionId: connID,
		PowerSupplyType:        util.Ptr(model.ElectricalConnectionVoltageTypeTypeAc),
		AcConnectedPhases:      util.Ptr(uint(3)),
	}); err != nil {
		log.Printf("[VAPD] adding electrical connection description failed: %v", err)
	}
	if id := ec.AddParameterDescription(model.ElectricalConnectionParameterDescriptionDataType{
		ElectricalConnectionId: connID,
		MeasurementId:          p.powerID,
		AcMeasuredPhases:       util.Ptr(model.ElectricalConnectionPhaseNameTypeAbc),
	}); id == nil {
		log.Printf("[VAPD] adding electrical connection parameter description failed")
	}

	// Scenario 1: nominal peak power of the PV system, exposed as a
	// DeviceConfiguration scaled-number key the reader looks up by name.
	devConf, err := server.NewDeviceConfiguration(p.pvEntity)
	if err != nil {
		log.Printf("[VAPD] creating DeviceConfiguration server feature failed: %v", err)
	} else {
		p.devConf = devConf
		p.peakID = devConf.AddKeyValueDescription(model.DeviceConfigurationKeyValueDescriptionDataType{
			KeyName:   util.Ptr(model.DeviceConfigurationKeyNameTypePeakPowerOfPVSystem),
			ValueType: util.Ptr(model.DeviceConfigurationKeyValueTypeTypeScaledNumber),
			Unit:      util.Ptr(model.UnitOfMeasurementTypeW),
		})
		if p.peakID == nil {
			log.Printf("[VAPD] adding peak power configuration description failed")
		}
	}

	log.Printf("[VAPD] PV-system provider features added (power=%d yield=%v peak=%v)",
		*p.powerID, idVal(p.yieldID), keyIDVal(p.peakID))
	return nil
}

func keyIDVal(id *model.DeviceConfigurationKeyIdType) int {
	if id == nil {
		return -1
	}
	return int(*id)
}

// publishMeasurement is the shared path for pushing one measurement value.
func (p *VAPDProvider) publishMeasurement(id *model.MeasurementIdType, value float64) error {
	return p.publisher.publishValue(p.meas, errVAPDNotInitialized, id, value)
}

func (p *VAPDProvider) publishPVMeasurements(snapshot PVSnapshot) error {
	return p.publisher.publishValues(
		p.meas,
		errVAPDNotInitialized,
		providerMeasurementValue{id: p.powerID, value: &snapshot.PowerW},
		providerMeasurementValue{id: p.yieldID, value: snapshot.YieldWh},
	)
}

func (p *VAPDProvider) invalidatePVMeasurements() error {
	return p.publisher.invalidate(p.meas, errVAPDNotInitialized, p.powerID, p.yieldID)
}

func (p *VAPDProvider) PublishPVSnapshot(snapshot PVSnapshot) error {
	p.snapshotMu.Lock()
	defer p.snapshotMu.Unlock()

	if snapshot.Validity.Invalid {
		if err := p.invalidatePVMeasurements(); err != nil {
			return err
		}
		p.snapshotVersion++
		stopProviderExpiryTimer(&p.expiryTimer)
		p.snapshot = nil
		return nil
	}
	if err := p.publishPVMeasurements(snapshot); err != nil {
		return err
	}
	next := snapshot.clone()
	p.snapshotVersion++
	p.snapshot = &next
	version := p.snapshotVersion
	scheduleProviderExpiryTimer(&p.expiryTimer, snapshot.Validity.ValidUntil, func() {
		p.expirePVSnapshot(version, time.Now())
	})
	return nil
}

func (p *VAPDProvider) CurrentPVSnapshot(now time.Time) (PVSnapshot, bool) {
	p.snapshotMu.Lock()
	defer p.snapshotMu.Unlock()

	if p.snapshot == nil || !p.snapshot.Validity.Current(now) {
		return PVSnapshot{}, false
	}
	return p.snapshot.clone(), true
}

func (p *VAPDProvider) expirePVSnapshot(version uint64, now time.Time) {
	p.snapshotMu.Lock()
	defer p.snapshotMu.Unlock()

	if p.snapshotVersion != version || p.snapshot == nil || p.snapshot.Validity.Current(now) {
		return
	}
	if err := p.invalidatePVMeasurements(); err != nil {
		log.Printf("[VAPD] expiring PV sample failed: %v", err)
		return
	}
	p.snapshotVersion++
	p.snapshot = nil
	stopProviderExpiryTimer(&p.expiryTimer)
}

// PublishPower pushes the momentary total PV power (W; scenario 2).
func (p *VAPDProvider) PublishPower(watts float64) error {
	if err := p.publishMeasurement(p.powerID, watts); err != nil {
		return err
	}
	if p.debug {
		log.Printf("[VAPD] published PV power: %.1f W", watts)
	}
	return nil
}

// PublishYield pushes the cumulative AC yield energy in Wh (scenario 3).
func (p *VAPDProvider) PublishYield(wh float64) error {
	if err := p.publishMeasurement(p.yieldID, wh); err != nil {
		return err
	}
	if p.debug {
		log.Printf("[VAPD] published PV yield energy: %.1f Wh", wh)
	}
	return nil
}

// PublishPeakPower pushes the nominal peak power of the PV system in W (scenario 1).
func (p *VAPDProvider) PublishPeakPower(watts float64) error {
	if p.devConf == nil || p.peakID == nil {
		return errVAPDNotInitialized
	}
	if err := p.devConf.UpdateKeyValueDataForKeyId(model.DeviceConfigurationKeyValueDataType{
		Value: &model.DeviceConfigurationKeyValueValueType{
			ScaledNumber: model.NewScaledNumberType(watts),
		},
	}, nil, *p.peakID); err != nil {
		return err
	}
	if p.debug {
		log.Printf("[VAPD] published PV peak power: %.1f W", watts)
	}
	return nil
}

// handleEvent receives UseCaseBase notifications. For this spike it only logs when a
// remote VisualizationAppliance binds to the provider.
func (p *VAPDProvider) handleEvent(ski string, _ spineapi.DeviceRemoteInterface, _ spineapi.EntityRemoteInterface, event eebusapi.EventType) {
	if event == vapdUseCaseSupportUpdate {
		log.Printf("[VAPD] consumer support update from ski=%s", ski)
		if p.bus != nil {
			p.bus.Publish(eebus.Event{SKI: ski, Type: eebus.EventTypeVAPDConsumerUpdated})
		}
	}
}
