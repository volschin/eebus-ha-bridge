package grpc

import (
	"context"
	"runtime/debug"
	"sort"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type TrustController interface {
	RegisterSKI(ski string) error
	UnregisterSKI(ski string) error
}

type DeviceService struct {
	pb.UnimplementedDeviceServiceServer
	callbacks  *eebus.Callbacks
	bus        *eebus.EventBus
	localSKI   string
	registry   *eebus.DeviceRegistry
	trust      TrustController
	payloads   DeviceStatePayloadSources
	serverInfo *pb.ServerInfo
	snapshot   *DeviceSnapshotAssembler
}

type DeviceServiceOption func(*DeviceService)

type DeviceStatePayloadSources struct {
	Monitoring MeasurementPayloadSource
	LPC        LPCPayloadSource
	DHW        DHWPayloadSource
	HVAC       HVACPayloadSource
	OHPCF      OHPCFPayloadSource
}

type MeasurementPayloadSource interface {
	AttachMeasurementPayload(*pb.MeasurementEvent, string, pb.MeasurementEventType)
}

type LPCPayloadSource interface {
	AttachLPCPayload(*pb.LPCEvent, string, pb.LPCEventType)
}

type DHWPayloadSource interface {
	AttachDHWPayload(*pb.DHWEvent, string)
	AttachDHWSystemFunctionPayload(*pb.DHWSystemFunctionEvent, string)
}

type HVACPayloadSource interface {
	AttachRoomHeatingPayload(*pb.RoomHeatingEvent, string) bool
}

type OHPCFPayloadSource interface {
	AttachOHPCFPayload(*pb.OHPCFEvent, string, eebus.EventType) bool
}

func WithDeviceStatePayloads(sources DeviceStatePayloadSources) DeviceServiceOption {
	return func(service *DeviceService) {
		service.payloads = sources
		service.snapshot = NewDeviceSnapshotAssembler(service.registry, sources)
	}
}

const (
	APIMajor uint32 = 1
	APIMinor uint32 = 0
)

// BuildVersion is overridden by release builds through -ldflags. Keeping the
// development default explicit makes local and test binaries identifiable
// without coupling the RPC implementation to the main package.
var BuildVersion = "dev"

var implementedFeatures = []pb.FeatureId{
	pb.FeatureId_FEATURE_EXPLICIT_CAPABILITIES,
	pb.FeatureId_FEATURE_CONSOLIDATED_DEVICE_STREAM,
	pb.FeatureId_FEATURE_PROVIDER_SAMPLE_INVALIDATION,
	pb.FeatureId_FEATURE_DEVICE_SNAPSHOT,
	pb.FeatureId_FEATURE_TYPED_MEASUREMENTS,
}

func WithServerInfo(info *pb.ServerInfo) DeviceServiceOption {
	return func(service *DeviceService) {
		service.serverInfo = cloneServerInfo(info)
	}
}

func defaultServerInfo() *pb.ServerInfo {
	version := BuildVersion
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	}
	return &pb.ServerInfo{
		ApiMajor:           APIMajor,
		ApiMinor:           APIMinor,
		BridgeBuildVersion: version,
		Features:           append([]pb.FeatureId(nil), implementedFeatures...),
	}
}

func cloneServerInfo(info *pb.ServerInfo) *pb.ServerInfo {
	if info == nil {
		return defaultServerInfo()
	}
	return &pb.ServerInfo{
		ApiMajor:           info.ApiMajor,
		ApiMinor:           info.ApiMinor,
		BridgeBuildVersion: info.BridgeBuildVersion,
		Features:           append([]pb.FeatureId(nil), info.Features...),
		LocalSki:           info.LocalSki,
	}
}

func NewDeviceService(callbacks *eebus.Callbacks, bus *eebus.EventBus, localSKI string, registry *eebus.DeviceRegistry, trust TrustController, opts ...DeviceServiceOption) *DeviceService {
	service := &DeviceService{
		callbacks:  callbacks,
		bus:        bus,
		localSKI:   localSKI,
		registry:   registry,
		trust:      trust,
		serverInfo: defaultServerInfo(),
	}
	for _, opt := range opts {
		opt(service)
	}
	service.serverInfo.LocalSki = localSKI
	return service
}

func (s *DeviceService) GetServerInfo(_ context.Context, _ *pb.Empty) (*pb.ServerInfo, error) {
	return cloneServerInfo(s.serverInfo), nil
}

func (s *DeviceService) GetStatus(_ context.Context, _ *pb.Empty) (*pb.ServiceStatus, error) {
	return &pb.ServiceStatus{
		Running:  true,
		LocalSki: s.localSKI,
	}, nil
}

func (s *DeviceService) GetDeviceStatus(_ context.Context, req *pb.DeviceRequest) (*pb.DeviceStatus, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	ski, err := requireExplicitSKI(req.Ski)
	if err != nil {
		return nil, err
	}

	connected, lastTransition, known := s.registry.DeviceConnection(ski)
	result := &pb.DeviceStatus{Connected: connected}
	if known && !lastTransition.IsZero() {
		result.LastTransition = timestamppb.New(lastTransition)
	}
	return result, nil
}

func (s *DeviceService) GetDeviceCapabilities(_ context.Context, req *pb.DeviceRequest) (*pb.DeviceCapabilities, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	ski, err := requireExplicitSKI(req.Ski)
	if err != nil {
		return nil, err
	}
	if s.registry == nil {
		return nil, status.Error(codes.Unavailable, "device registry not initialized")
	}
	if !s.registry.KnownDevice(ski) {
		return nil, status.Error(codes.NotFound, "device not found for specified ski")
	}
	return s.deviceCapabilities(ski), nil
}

func (s *DeviceService) deviceCapabilities(ski string) *pb.DeviceCapabilities {
	if s.registry == nil {
		return &pb.DeviceCapabilities{Ski: ski}
	}
	return capabilitiesFromRegistry(s.registry, ski)
}

func capabilityID(value eebus.Capability) pb.CapabilityId {
	return pb.CapabilityId(value)
}

func capabilityState(value eebus.CapabilityState) pb.CapabilityState {
	return pb.CapabilityState(value)
}

func capabilityReason(value eebus.CapabilityReason) pb.CapabilityReason {
	return pb.CapabilityReason(value)
}

func (s *DeviceService) ListDiscoveredDevices(_ context.Context, _ *pb.Empty) (*pb.ListDevicesResponse, error) {
	svcs := s.callbacks.DiscoveredServices()
	sort.SliceStable(svcs, func(i, j int) bool {
		return eebus.NormalizeSKI(svcs[i].Ski) < eebus.NormalizeSKI(svcs[j].Ski)
	})
	devices := make([]*pb.DiscoveredDevice, 0, len(svcs))
	for _, svc := range svcs {
		devices = append(devices, &pb.DiscoveredDevice{
			Ski: svc.Ski,
		})
	}
	return &pb.ListDevicesResponse{Devices: devices}, nil
}

func (s *DeviceService) RegisterRemoteSKI(_ context.Context, req *pb.RegisterSKIRequest) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	ski, err := requireExplicitSKI(req.Ski)
	if err != nil {
		return nil, err
	}
	if err := s.trust.RegisterSKI(ski); err != nil {
		return nil, mapUsecaseError("registering remote SKI", err, usecaseErrorClasses{})
	}
	return &pb.Empty{}, nil
}

// UnregisterRemoteSKI revokes trust for a paired remote SKI without disturbing
// the bridge's own local identity. This lets a stale or wrongly-paired device
// be dropped without deleting internal/certs/ (which would rotate the local
// SKI and force every other paired device to re-pair too).
func (s *DeviceService) UnregisterRemoteSKI(_ context.Context, req *pb.RegisterSKIRequest) (*pb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	ski, err := requireExplicitSKI(req.Ski)
	if err != nil {
		return nil, err
	}
	if err := s.trust.UnregisterSKI(ski); err != nil {
		return nil, mapUsecaseError("unregistering remote SKI", err, usecaseErrorClasses{})
	}
	return &pb.Empty{}, nil
}

func (s *DeviceService) ListPairedDevices(_ context.Context, _ *pb.Empty) (*pb.ListPairedDevicesResponse, error) {
	devices := s.registry.ListDevices()
	result := make([]*pb.PairedDevice, 0, len(devices))
	for _, device := range devices {
		result = append(result, &pb.PairedDevice{
			Ski:               device.SKI,
			Brand:             device.Brand,
			Model:             device.Model,
			Serial:            device.Serial,
			DeviceType:        device.DeviceType,
			SupportedUseCases: device.UseCases,
		})
	}
	return &pb.ListPairedDevicesResponse{Devices: result}, nil
}

func (s *DeviceService) SubscribeDeviceEvents(_ *pb.Empty, stream pb.DeviceService_SubscribeDeviceEventsServer) error {
	return subscribeAllEvents(s.bus, stream.Context(), stream.Send, func(evt eebus.Event) (*pb.DeviceEvent, bool) {
		var eventType pb.DeviceEventType
		switch evt.Type {
		case eebus.EventTypeDeviceConnected:
			eventType = pb.DeviceEventType_DEVICE_EVENT_CONNECTED
		case eebus.EventTypeDeviceDisconnected:
			eventType = pb.DeviceEventType_DEVICE_EVENT_DISCONNECTED
		case eebus.EventTypeDeviceTrustRemoved:
			eventType = pb.DeviceEventType_DEVICE_EVENT_TRUST_REMOVED
		default:
			return nil, false
		}
		return &pb.DeviceEvent{
			Ski:       evt.SKI,
			EventType: eventType,
		}, true
	})
}
