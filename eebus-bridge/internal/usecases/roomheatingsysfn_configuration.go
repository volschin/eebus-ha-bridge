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
) *CRHSFConfigurationFacade {
	if localEntity == nil {
		return &CRHSFConfigurationFacade{}
	}
	client := cacrhsf.NewCRHSF(localEntity, nil)
	inspector := bridgeRoomHeatingSystemFunctionCapabilityInspector{}
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
// read-only. Until CRHSF exposes WriteCapabilities, it inspects only the
// operation and changeability fields needed by the stable bridge contract.
// Mode IDs, relations, state reads and writes remain wholly upstream-owned.
type bridgeRoomHeatingSystemFunctionCapabilityInspector struct{}

func (i bridgeRoomHeatingSystemFunctionCapabilityInspector) State(
	entity spineapi.EntityRemoteInterface,
) (RoomHeatingSystemFunctionState, error) {
	if entity == nil {
		return RoomHeatingSystemFunctionState{}, ErrRoomHeatingSysFnDataUnavailable
	}
	remote := entity.FeatureOfTypeAndRole(model.FeatureTypeTypeHvac, model.RoleTypeServer)
	if remote == nil {
		return RoomHeatingSystemFunctionState{}, ErrRoomHeatingSysFnDataUnavailable
	}
	descriptions, ok := remote.DataCopy(model.FunctionTypeHvacSystemFunctionDescriptionListData).(*model.HvacSystemFunctionDescriptionListDataType)
	if !ok || descriptions == nil {
		return RoomHeatingSystemFunctionState{}, ErrRoomHeatingSysFnDataUnavailable
	}
	var heatingIDs []model.HvacSystemFunctionIdType
	for _, description := range descriptions.HvacSystemFunctionDescriptionData {
		if description.SystemFunctionId != nil && description.SystemFunctionType != nil &&
			*description.SystemFunctionType == model.HvacSystemFunctionTypeTypeHeating {
			heatingIDs = append(heatingIDs, *description.SystemFunctionId)
		}
	}
	if len(heatingIDs) != 1 {
		return RoomHeatingSystemFunctionState{}, ErrRoomHeatingSysFnDataUnavailable
	}
	data, ok := remote.DataCopy(model.FunctionTypeHvacSystemFunctionListData).(*model.HvacSystemFunctionListDataType)
	if !ok || data == nil {
		return RoomHeatingSystemFunctionState{}, ErrRoomHeatingSysFnDataUnavailable
	}
	var system *model.HvacSystemFunctionDataType
	for index := range data.HvacSystemFunctionData {
		candidate := &data.HvacSystemFunctionData[index]
		if candidate.SystemFunctionId != nil && *candidate.SystemFunctionId == heatingIDs[0] {
			if system != nil {
				return RoomHeatingSystemFunctionState{}, ErrRoomHeatingSysFnDataUnavailable
			}
			system = candidate
		}
	}
	if system == nil {
		return RoomHeatingSystemFunctionState{}, ErrRoomHeatingSysFnDataUnavailable
	}
	operation := remote.Operations()[model.FunctionTypeHvacSystemFunctionListData]
	return RoomHeatingSystemFunctionState{
		ModeWritable: operation != nil && operation.Write() &&
			(system.IsOperationModeIdChangeable == nil || *system.IsOperationModeIdChangeable),
	}, nil
}
