package usecases

import (
	"context"
	"fmt"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	cacdsf "github.com/enbility/eebus-go/usecases/ca/cdsf"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

type caCDSFClient interface {
	eebusapi.UseCaseInterface
	RemoteEntitiesScenarios() []eebusapi.RemoteEntityScenarios
	IsScenarioAvailableAtEntity(spineapi.EntityRemoteInterface, uint) bool
	WriteOperationMode(
		spineapi.EntityRemoteInterface,
		ucapi.HvacOperationModeType,
		func(model.ResultDataType, model.MsgCounterType),
	) (*model.MsgCounterType, error)
	StartOneTimeDhw(
		spineapi.EntityRemoteInterface,
		func(model.ResultDataType, model.MsgCounterType),
	) (*model.MsgCounterType, error)
	StopOneTimeDhw(
		spineapi.EntityRemoteInterface,
		func(model.ResultDataType, model.MsgCounterType),
	) (*model.MsgCounterType, error)
}

type dhwSystemFunctionEntityResolver interface {
	CompatibleEntity(string) eebus.EntityResolution
}

type dhwSystemFunctionCapabilityInspector interface {
	State(spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error)
}

type dhwBoostWriter interface {
	WriteBoost(context.Context, spineapi.EntityRemoteInterface, bool) error
}

type dhwOperationModeWriter interface {
	WriteOperationMode(context.Context, spineapi.EntityRemoteInterface, string) error
}

// CDSFConfigurationFacade keeps entity resolution, cached capability inspection,
// and the two write transports independently replaceable during the CDSF
// upstream migration.
type CDSFConfigurationFacade struct {
	useCase             eebusapi.UseCaseInterface
	resolver            dhwSystemFunctionEntityResolver
	capabilityInspector dhwSystemFunctionCapabilityInspector
	boostWriter         dhwBoostWriter
	operationModeWriter dhwOperationModeWriter
}

func newCDSFConfigurationFacade(
	resolver dhwSystemFunctionEntityResolver,
	capabilityInspector dhwSystemFunctionCapabilityInspector,
	boostWriter dhwBoostWriter,
	operationModeWriter dhwOperationModeWriter,
) *CDSFConfigurationFacade {
	return &CDSFConfigurationFacade{
		resolver:            resolver,
		capabilityInspector: capabilityInspector,
		boostWriter:         boostWriter,
		operationModeWriter: operationModeWriter,
	}
}

// NewUpstreamDHWSystemFunctionConfiguration selects eebus-go CDSF for use-case
// negotiation, feature setup and DHW writes. A nil event callback deliberately
// keeps MDSF as the sole owner of DHW state and support events.
func NewUpstreamDHWSystemFunctionConfiguration(
	localEntity spineapi.EntityLocalInterface,
	debug bool,
) *CDSFConfigurationFacade {
	if localEntity == nil {
		return &CDSFConfigurationFacade{}
	}
	client := cacdsf.NewCDSF(localEntity, nil)
	localHvacFeature := func() spineapi.FeatureLocalInterface {
		return localEntity.FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeClient)
	}
	request := func(entity spineapi.EntityRemoteInterface, function model.FunctionType) {
		requestRemoteFeatureData(entity, hvacServer, localHvacFeature, function, debug, "DHWSYSFN")
	}
	return newUpstreamDHWSystemFunctionConfiguration(client, localHvacFeature, request)
}

func newUpstreamDHWSystemFunctionConfiguration(
	client caCDSFClient,
	localHvacFeature func() spineapi.FeatureLocalInterface,
	request func(spineapi.EntityRemoteInterface, model.FunctionType),
) *CDSFConfigurationFacade {
	if client == nil {
		return &CDSFConfigurationFacade{}
	}
	inspector := scenarioAwareDHWSystemFunctionCapabilityInspector{
		client: client,
		cached: cachedDHWSystemFunctionCapabilityInspector{},
	}
	boostTransport := &upstreamDHWBoostWriter{
		client:    client,
		inspector: inspector,
		request:   request,
	}
	modeTransport := &upstreamDHWOperationModeWriter{
		client:    client,
		inspector: inspector,
		request:   request,
	}
	facade := newCDSFConfigurationFacade(caCDSFEntityResolver{client: client}, inspector, boostTransport, modeTransport)
	facade.useCase = client
	return facade
}

// NewLegacyDHWSystemFunctionConfiguration selects the local CDSF use case for
// negotiation and both legacy write strategies. It remains the release-level
// rollback composition while the upstream boost and operation-mode strategies
// are validated independently.
func NewLegacyDHWSystemFunctionConfiguration(useCase *DHWSystemFunction) *CDSFConfigurationFacade {
	if useCase == nil {
		return &CDSFConfigurationFacade{}
	}
	inspector := cachedDHWSystemFunctionCapabilityInspector{}
	transport := &legacyDHWSystemFunctionWriter{
		localHvacFeature: useCase.localHvacFeature,
		request:          useCase.request,
		inspector:        inspector,
	}
	facade := newCDSFConfigurationFacade(useCase, inspector, transport, transport)
	facade.useCase = useCase
	return facade
}

// UseCase returns the selected CDSF negotiation owner for service registration.
func (f *CDSFConfigurationFacade) UseCase() eebusapi.UseCaseInterface {
	if f == nil {
		return nil
	}
	return f.useCase
}

func (f *CDSFConfigurationFacade) CompatibleEntity(ski string) eebus.EntityResolution {
	if f == nil || f.resolver == nil {
		return eebus.EntityResolution{}
	}
	return f.resolver.CompatibleEntity(ski)
}

func (f *CDSFConfigurationFacade) State(entity spineapi.EntityRemoteInterface) (DHWSystemFunctionState, error) {
	if f == nil || f.capabilityInspector == nil {
		return DHWSystemFunctionState{}, ErrDHWSysFnDataUnavailable
	}
	return f.capabilityInspector.State(entity)
}

func (f *CDSFConfigurationFacade) WriteBoost(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	active bool,
) error {
	if f == nil || f.boostWriter == nil {
		return ErrDHWSysFnNotWritable
	}
	return f.boostWriter.WriteBoost(ctx, entity, active)
}

func (f *CDSFConfigurationFacade) WriteOperationMode(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	mode string,
) error {
	if f == nil || f.operationModeWriter == nil {
		return ErrDHWSysFnNotWritable
	}
	return f.operationModeWriter.WriteOperationMode(ctx, entity, mode)
}

type caCDSFEntityResolver struct {
	client caCDSFClient
}

func (r caCDSFEntityResolver) CompatibleEntity(ski string) eebus.EntityResolution {
	if r.client == nil {
		return eebus.EntityResolution{}
	}
	return compatibleEntity(r.client.RemoteEntitiesScenarios(), ski)
}

type scenarioAwareDHWSystemFunctionCapabilityInspector struct {
	client caCDSFClient
	cached dhwSystemFunctionCapabilityInspector
}

func (i scenarioAwareDHWSystemFunctionCapabilityInspector) State(
	entity spineapi.EntityRemoteInterface,
) (DHWSystemFunctionState, error) {
	if i.client == nil || i.cached == nil {
		return DHWSystemFunctionState{}, ErrDHWSysFnDataUnavailable
	}
	state, err := i.cached.State(entity)
	if err != nil {
		return DHWSystemFunctionState{}, err
	}
	modeScenario := i.client.IsScenarioAvailableAtEntity(entity, 1)
	boostStartScenario := i.client.IsScenarioAvailableAtEntity(entity, 2)
	boostStopScenario := i.client.IsScenarioAvailableAtEntity(entity, 3)
	state.ModeWritable = state.ModeWritable && modeScenario
	state.BoostWritable = state.BoostWritable && boostStartScenario && boostStopScenario
	return state, nil
}

// legacyDHWSystemFunctionWriter contains the bridge-local list merge and SPINE
// transport retained for rollback while upstream CDSF is introduced.
type legacyDHWSystemFunctionWriter struct {
	localHvacFeature func() spineapi.FeatureLocalInterface
	request          func(spineapi.EntityRemoteInterface, model.FunctionType)
	inspector        dhwSystemFunctionCapabilityInspector
}

func (w *legacyDHWSystemFunctionWriter) WriteBoost(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	active bool,
) error {
	state, err := w.inspector.State(entity)
	if err != nil {
		return err
	}
	if !state.BoostWritable {
		return ErrDHWSysFnNotWritable
	}
	remote := hvacServer(entity)
	local := w.localFeature()
	if remote == nil || local == nil {
		return ErrDHWSysFnDataUnavailable
	}
	resolved, err := resolveDHWSystemFunction(remote)
	if err != nil {
		return err
	}
	data, ok := remote.DataCopy(model.FunctionTypeHvacOverrunListData).(*model.HvacOverrunListDataType)
	if !ok || data == nil {
		return ErrDHWSysFnDataUnavailable
	}
	entries := make([]model.HvacOverrunDataType, len(data.HvacOverrunData))
	copy(entries, data.HvacOverrunData)
	status := model.HvacOverrunStatusTypeInactive
	if active {
		status = model.HvacOverrunStatusTypeActive
	}
	found := false
	for index := range entries {
		if entries[index].OverrunId != nil && *entries[index].OverrunId == resolved.overrunID {
			entries[index].OverrunStatus = &status
			found = true
			break
		}
	}
	if !found {
		return ErrDHWSysFnDataUnavailable
	}
	return w.write(ctx, entity, remote, local, model.CmdType{
		HvacOverrunListData: &model.HvacOverrunListDataType{HvacOverrunData: entries},
	}, model.FunctionTypeHvacOverrunListData, "DHW boost")
}

func (w *legacyDHWSystemFunctionWriter) WriteOperationMode(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	modeType string,
) error {
	state, err := w.inspector.State(entity)
	if err != nil {
		return err
	}
	if !state.ModeWritable {
		return ErrDHWSysFnNotWritable
	}
	remote := hvacServer(entity)
	local := w.localFeature()
	if remote == nil || local == nil {
		return ErrDHWSysFnDataUnavailable
	}
	resolved, err := resolveDHWSystemFunction(remote)
	if err != nil {
		return err
	}
	id, ok := resolved.modeIDForType[modeType]
	if !ok {
		return fmt.Errorf("%w: %s", ErrDHWSysFnInvalidMode, modeType)
	}
	data, ok := remote.DataCopy(model.FunctionTypeHvacSystemFunctionListData).(*model.HvacSystemFunctionListDataType)
	if !ok || data == nil {
		return ErrDHWSysFnDataUnavailable
	}
	entries := make([]model.HvacSystemFunctionDataType, len(data.HvacSystemFunctionData))
	copy(entries, data.HvacSystemFunctionData)
	found := false
	for index := range entries {
		if entries[index].SystemFunctionId != nil && *entries[index].SystemFunctionId == resolved.systemID {
			entries[index].CurrentOperationModeId = &id
			found = true
			break
		}
	}
	if !found {
		return ErrDHWSysFnDataUnavailable
	}
	return w.write(ctx, entity, remote, local, model.CmdType{
		HvacSystemFunctionListData: &model.HvacSystemFunctionListDataType{HvacSystemFunctionData: entries},
	}, model.FunctionTypeHvacSystemFunctionListData, "DHW operation mode")
}

func (w *legacyDHWSystemFunctionWriter) write(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	remote spineapi.FeatureRemoteInterface,
	local spineapi.FeatureLocalInterface,
	cmd model.CmdType,
	refresh model.FunctionType,
	label string,
) error {
	err := awaitDHWWrite(ctx, label, func(callback dhwResultCallback) (*model.MsgCounterType, error) {
		device := entity.Device()
		if device == nil {
			return nil, ErrDHWSysFnDataUnavailable
		}
		sender := device.Sender()
		if sender == nil {
			return nil, ErrDHWSysFnDataUnavailable
		}
		counter, err := sender.Write(local.Address(), remote.Address(), cmd)
		if err != nil {
			return counter, fmt.Errorf("sending %s: %w", label, err)
		}
		if counter == nil {
			return nil, fmt.Errorf("sending %s returned no message counter", label)
		}
		if err := local.AddResponseCallback(*counter, func(message spineapi.ResponseMessage) {
			if data, ok := message.Data.(*model.ResultDataType); ok && data != nil {
				callback(*data, *counter)
			}
		}); err != nil {
			return counter, fmt.Errorf("waiting for %s result: %w", label, err)
		}
		return counter, nil
	})
	if err != nil {
		return err
	}
	if w.request != nil {
		w.request(entity, refresh)
	}
	return nil
}

func (w *legacyDHWSystemFunctionWriter) localFeature() spineapi.FeatureLocalInterface {
	if w == nil || w.localHvacFeature == nil {
		return nil
	}
	return w.localHvacFeature()
}

type dhwResultCallback func(model.ResultDataType, model.MsgCounterType)
type dhwWriteCall func(dhwResultCallback) (*model.MsgCounterType, error)

var dhwSystemFunctionWriteTimeout = dhwWriteTimeout

// awaitDHWWrite adapts eebus-go's asynchronous CDSF result callback to the
// bridge's context-aware synchronous write contract. The buffered channel is
// required because a test double or transport may invoke the callback before
// the write method returns.
func awaitDHWWrite(ctx context.Context, label string, write dhwWriteCall) error {
	type writeResult struct {
		data    model.ResultDataType
		counter model.MsgCounterType
	}
	result := make(chan writeResult, 1)
	counter, err := write(func(data model.ResultDataType, counter model.MsgCounterType) {
		result <- writeResult{data: data, counter: counter}
	})
	if err != nil {
		return err
	}
	if counter == nil {
		return fmt.Errorf("sending %s returned no message counter", label)
	}

	timer := time.NewTimer(dhwSystemFunctionWriteTimeout)
	defer timer.Stop()
	select {
	case response := <-result:
		if response.counter != *counter {
			return fmt.Errorf("waiting for %s result returned unexpected message counter", label)
		}
		if response.data.ErrorNumber != nil && *response.data.ErrorNumber != 0 {
			err := fmt.Errorf("%w: %s error=%d", ErrDHWSysFnRejected, label, *response.data.ErrorNumber)
			if response.data.Description != nil && *response.data.Description != "" {
				err = fmt.Errorf("%w (%s)", err, *response.data.Description)
			}
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("timed out waiting for %s result", label)
	}
}
