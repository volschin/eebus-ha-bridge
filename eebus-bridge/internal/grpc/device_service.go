package grpc

import (
	"context"
	"runtime/debug"
	"sort"
	"time"

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
	recovery   RecoveryDiagnosticsSource
	providers  ProviderDiagnosticsSource
}

type DeviceServiceOption func(*DeviceService)

type RecoveryDiagnosticsSource interface {
	Snapshot(string, time.Time) eebus.RecoverySnapshot
}

type ProviderDiagnosticsSource interface {
	ProviderDiagnostics(time.Time) []*pb.ProviderSampleDiagnostics
}

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
		service.snapshot.recovery = service.recovery
	}
}

func WithOperationalDiagnostics(recovery RecoveryDiagnosticsSource, providers ProviderDiagnosticsSource) DeviceServiceOption {
	return func(service *DeviceService) {
		service.recovery = recovery
		service.providers = providers
		if service.snapshot != nil {
			service.snapshot.recovery = recovery
		}
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
	pb.FeatureId_FEATURE_OPERATIONAL_DIAGNOSTICS,
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
	if s.recovery != nil {
		recovery := s.recovery.Snapshot(ski, time.Now())
		result.Readiness = readinessState(recovery.State)
		result.Recovery = recoveryDiagnostics(recovery)
	}
	return result, nil
}

func (s *DeviceService) GetDeviceDiagnostics(_ context.Context, req *pb.DeviceRequest) (*pb.DeviceOperationalDiagnostics, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	ski, err := requireExplicitSKI(req.Ski)
	if err != nil {
		return nil, err
	}
	if s.registry == nil || !s.registry.KnownDevice(ski) {
		return nil, status.Error(codes.NotFound, "device not found for specified ski")
	}
	now := time.Now()
	recovery := eebus.RecoverySnapshot{State: eebus.RecoveryStateUnknown}
	if s.recovery != nil {
		recovery = s.recovery.Snapshot(ski, now)
	}
	result := &pb.DeviceOperationalDiagnostics{
		RedactedSki: eebus.ShortSKI(ski),
		Readiness:   readinessState(recovery.State),
		Recovery:    recoveryDiagnostics(recovery),
		Features:    append([]pb.FeatureId(nil), s.serverInfo.Features...),
	}
	if s.bus != nil {
		events := s.bus.Diagnostics(ski)
		result.Events = &pb.EventTransportDiagnostics{
			Revision: events.Revision, DroppedEvents: events.DroppedEvents,
			ResyncCount: events.ResyncCount, UnresolvedEvents: events.UnresolvedEvents,
		}
	}
	if age, ok := s.registry.MonitoringLastSuccessAge(ski); ok {
		seconds := uint64(max(age/time.Second, 0))
		result.MonitoringLastSuccessAgeSeconds = &seconds
	}
	if s.snapshot != nil {
		metrics := s.snapshot.Metrics(ski)
		result.SnapshotReads = &pb.SnapshotReadDiagnostics{
			DurationMilliseconds: uint64(max(metrics.Duration/time.Millisecond, 0)),
		}
		if !metrics.LastSuccess.IsZero() {
			result.SnapshotReads.LastSuccess = timestamppb.New(metrics.LastSuccess)
		}
	}
	if s.providers != nil {
		result.Providers = s.providers.ProviderDiagnostics(now)
	}
	return result, nil
}

func readinessState(state eebus.RecoveryState) pb.DeviceReadinessState {
	switch state {
	case eebus.RecoveryStateUntrusted:
		return pb.DeviceReadinessState_DEVICE_READINESS_UNTRUSTED
	case eebus.RecoveryStateDisconnected:
		return pb.DeviceReadinessState_DEVICE_READINESS_DISCONNECTED
	case eebus.RecoveryStateGracePeriod:
		return pb.DeviceReadinessState_DEVICE_READINESS_GRACE_PERIOD
	case eebus.RecoveryStateRecovering:
		return pb.DeviceReadinessState_DEVICE_READINESS_RECOVERING
	case eebus.RecoveryStateHealthy:
		return pb.DeviceReadinessState_DEVICE_READINESS_READY
	case eebus.RecoveryStateExhausted:
		return pb.DeviceReadinessState_DEVICE_READINESS_EXHAUSTED
	default:
		return pb.DeviceReadinessState_DEVICE_READINESS_UNKNOWN
	}
}

func recoveryDiagnostics(snapshot eebus.RecoverySnapshot) *pb.RecoveryDiagnostics {
	result := &pb.RecoveryDiagnostics{
		State: readinessState(snapshot.State), Attempts: uint32(snapshot.Attempts), // #nosec G115 -- bounded retry count
	}
	if !snapshot.FirstStaleAt.IsZero() {
		result.FirstStaleAt = timestamppb.New(snapshot.FirstStaleAt)
	}
	if !snapshot.LastAttemptAt.IsZero() {
		result.LastAttemptAt = timestamppb.New(snapshot.LastAttemptAt)
	}
	if !snapshot.NextAttemptAt.IsZero() {
		result.NextAttemptAt = timestamppb.New(snapshot.NextAttemptAt)
	}
	if !snapshot.LastTransitionAt.IsZero() {
		result.LastTransitionAt = timestamppb.New(snapshot.LastTransitionAt)
	}
	return result
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
