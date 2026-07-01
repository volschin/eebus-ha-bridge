package grpc

import (
	"context"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type DeviceService struct {
	pb.UnimplementedDeviceServiceServer
	callbacks *eebus.Callbacks
	bus       *eebus.EventBus
	localSKI  string
	registry  *eebus.DeviceRegistry
}

func NewDeviceService(callbacks *eebus.Callbacks, bus *eebus.EventBus, localSKI string, registry *eebus.DeviceRegistry) *DeviceService {
	return &DeviceService{
		callbacks: callbacks,
		bus:       bus,
		localSKI:  localSKI,
		registry:  registry,
	}
}

func (s *DeviceService) GetStatus(_ context.Context, _ *pb.Empty) (*pb.ServiceStatus, error) {
	return &pb.ServiceStatus{
		Running:  true,
		LocalSki: s.localSKI,
	}, nil
}

func (s *DeviceService) ListDiscoveredDevices(_ context.Context, _ *pb.Empty) (*pb.ListDevicesResponse, error) {
	svcs := s.callbacks.DiscoveredServices()
	devices := make([]*pb.DiscoveredDevice, 0, len(svcs))
	for _, svc := range svcs {
		devices = append(devices, &pb.DiscoveredDevice{
			Ski: svc.Ski,
		})
	}
	return &pb.ListDevicesResponse{Devices: devices}, nil
}

func (s *DeviceService) RegisterRemoteSKI(_ context.Context, req *pb.RegisterSKIRequest) (*pb.Empty, error) {
	if !validSKI(req.Ski) {
		return nil, status.Errorf(codes.InvalidArgument, "ski must be 40 hex characters, got %q", req.Ski)
	}
	s.bus.Publish(eebus.Event{SKI: req.Ski, Type: "device.register_ski"})
	return &pb.Empty{}, nil
}

// UnregisterRemoteSKI revokes trust for a paired remote SKI without disturbing
// the bridge's own local identity. This lets a stale or wrongly-paired device
// be dropped without deleting internal/certs/ (which would rotate the local
// SKI and force every other paired device to re-pair too).
func (s *DeviceService) UnregisterRemoteSKI(_ context.Context, req *pb.RegisterSKIRequest) (*pb.Empty, error) {
	if !validSKI(req.Ski) {
		return nil, status.Errorf(codes.InvalidArgument, "ski must be 40 hex characters, got %q", req.Ski)
	}
	s.bus.Publish(eebus.Event{SKI: req.Ski, Type: "device.unregister_ski"})
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
	ch := s.bus.Subscribe()
	defer s.bus.Unsubscribe(ch)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return nil
			}
			var eventType pb.DeviceEventType
			switch evt.Type {
			case "device.connected":
				eventType = pb.DeviceEventType_DEVICE_EVENT_CONNECTED
			case "device.disconnected":
				eventType = pb.DeviceEventType_DEVICE_EVENT_DISCONNECTED
			case "device.trust_removed":
				eventType = pb.DeviceEventType_DEVICE_EVENT_TRUST_REMOVED
			default:
				continue
			}
			if err := stream.Send(&pb.DeviceEvent{
				Ski:       evt.SKI,
				EventType: eventType,
			}); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}
