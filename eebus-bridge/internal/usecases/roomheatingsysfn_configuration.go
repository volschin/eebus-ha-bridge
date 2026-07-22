package usecases

import (
	"context"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	cacrhsf "github.com/enbility/eebus-go/usecases/ca/crhsf"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

type caCRHSFClient interface {
	eebusapi.UseCaseInterface
	RemoteEntitiesScenarios() []eebusapi.RemoteEntityScenarios
	OperationModes(spineapi.EntityRemoteInterface) ([]ucapi.HvacOperationModeType, error)
	WriteOperationMode(
		spineapi.EntityRemoteInterface,
		ucapi.HvacOperationModeType,
		func(model.ResultDataType, model.MsgCounterType),
	) (*model.MsgCounterType, error)
}

type roomHeatingSystemFunctionEntityResolver interface {
	CompatibleEntity(string) eebus.EntityResolution
}

type roomHeatingSystemFunctionCapabilityInspector interface {
	State(spineapi.EntityRemoteInterface) (RoomHeatingSystemFunctionState, error)
}

type roomHeatingOperationModeWriter interface {
	WriteOperationMode(context.Context, spineapi.EntityRemoteInterface, string) error
}

// CRHSFConfigurationFacade keeps upstream CRHSF negotiation and entity
// resolution separate from the release-wide capability and writer strategies.
// Phase 3 retains the read-only bridge capability inspector while selecting
// CRHSF as the sole writer. There is no per-request fallback to the legacy
// writer.
type CRHSFConfigurationFacade struct {
	useCase             eebusapi.UseCaseInterface
	resolver            roomHeatingSystemFunctionEntityResolver
	capabilityInspector roomHeatingSystemFunctionCapabilityInspector
	operationModeWriter roomHeatingOperationModeWriter
}

func newCRHSFConfigurationFacade(
	useCase eebusapi.UseCaseInterface,
	resolver roomHeatingSystemFunctionEntityResolver,
	capabilityInspector roomHeatingSystemFunctionCapabilityInspector,
	operationModeWriter roomHeatingOperationModeWriter,
) *CRHSFConfigurationFacade {
	return &CRHSFConfigurationFacade{
		useCase:             useCase,
		resolver:            resolver,
		capabilityInspector: capabilityInspector,
		operationModeWriter: operationModeWriter,
	}
}

// NewUpstreamRoomHeatingSystemFunctionConfiguration selects eebus-go CRHSF
// for use-case negotiation, feature setup and cache population. Until CRHSF
// exposes a fail-closed public WriteCapabilities API, a read-only bridge
// inspector remains the capability owner. CRHSF is the only selected write
// strategy. A nil upstream callback keeps MRHSF as the sole owner of
// user-visible room-heating state events.
func NewUpstreamRoomHeatingSystemFunctionConfiguration(
	localEntity spineapi.EntityLocalInterface,
	debug bool,
) *CRHSFConfigurationFacade {
	if localEntity == nil {
		return &CRHSFConfigurationFacade{}
	}
	client := cacrhsf.NewCRHSF(localEntity, nil)
	legacy := newLegacyRoomHeatingSystemFunctionStrategy(localEntity, debug)
	inspector := bridgeRoomHeatingSystemFunctionCapabilityInspector{state: legacy}
	return newCRHSFConfigurationFacade(
		client,
		crhsfEntityResolver{useCase: client},
		inspector,
		&upstreamRoomHeatingOperationModeWriter{client: client, inspector: inspector},
	)
}

// UseCase returns eebus-go's CRHSF use case for service registration.
func (f *CRHSFConfigurationFacade) UseCase() eebusapi.UseCaseInterface {
	if f == nil {
		return nil
	}
	return f.useCase
}

func (f *CRHSFConfigurationFacade) CompatibleEntity(ski string) eebus.EntityResolution {
	if f == nil || f.resolver == nil {
		return eebus.EntityResolution{}
	}
	return f.resolver.CompatibleEntity(ski)
}

func (f *CRHSFConfigurationFacade) State(
	entity spineapi.EntityRemoteInterface,
) (RoomHeatingSystemFunctionState, error) {
	if f == nil || f.capabilityInspector == nil {
		return RoomHeatingSystemFunctionState{}, ErrRoomHeatingSysFnDataUnavailable
	}
	return f.capabilityInspector.State(entity)
}

func (f *CRHSFConfigurationFacade) WriteOperationMode(
	ctx context.Context,
	entity spineapi.EntityRemoteInterface,
	mode string,
) error {
	if f == nil || f.operationModeWriter == nil {
		return ErrRoomHeatingSysFnNotWritable
	}
	return f.operationModeWriter.WriteOperationMode(ctx, entity, mode)
}

type crhsfEntityResolver struct {
	useCase eebusapi.UseCaseInterface
}

func (r crhsfEntityResolver) CompatibleEntity(ski string) eebus.EntityResolution {
	if r.useCase == nil {
		return eebus.EntityResolution{}
	}
	return compatibleEntity(r.useCase.RemoteEntitiesScenarios(), ski)
}

// bridgeRoomHeatingSystemFunctionCapabilityInspector is intentionally
// read-only. It preserves the legacy distinction between incomplete caches
// (data unavailable) and a negotiated read-only function (ModeWritable=false)
// without taking negotiation or write ownership away from upstream CRHSF.
type bridgeRoomHeatingSystemFunctionCapabilityInspector struct {
	state roomHeatingSystemFunctionCapabilityInspector
}

func (i bridgeRoomHeatingSystemFunctionCapabilityInspector) State(
	entity spineapi.EntityRemoteInterface,
) (RoomHeatingSystemFunctionState, error) {
	if i.state == nil || entity == nil {
		return RoomHeatingSystemFunctionState{}, ErrRoomHeatingSysFnDataUnavailable
	}
	state, err := i.state.State(entity)
	if err != nil {
		return RoomHeatingSystemFunctionState{}, err
	}
	return RoomHeatingSystemFunctionState{ModeWritable: state.ModeWritable}, nil
}
