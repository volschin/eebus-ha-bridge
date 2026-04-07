package grpc

import (
	"context"

	shipapi "github.com/enbility/ship-go/api"
	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

type DeviceService struct {
	pb.UnimplementedDeviceServiceServer
	callbacks *eebus.Callbacks
	bus       *eebus.EventBus
	localSKI  string
}

func NewDeviceService(callbacks *eebus.Callbacks, bus *eebus.EventBus, localSKI string) *DeviceService {
	return &DeviceService{
		callbacks: callbacks,
		bus:       bus,
		localSKI:  localSKI,
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
	s.bus.Publish(eebus.Event{SKI: req.Ski, Type: "device.register_ski"})
	return &pb.Empty{}, nil
}

func (s *DeviceService) UnregisterRemoteSKI(_ context.Context, req *pb.DeviceRequest) (*pb.Empty, error) {
	s.bus.Publish(eebus.Event{SKI: req.Ski, Type: "device.unregister_ski"})
	return &pb.Empty{}, nil
}

func (s *DeviceService) GetPairingStatus(_ context.Context, req *pb.DeviceRequest) (*pb.PairingStatus, error) {
	state := s.callbacks.PairingState(req.Ski)
	ps := &pb.PairingStatus{Ski: req.Ski, State: pb.PairingState_PAIRING_STATE_UNSPECIFIED}
	if state != nil {
		ps.State = mapConnectionState(state.State())
	}
	return ps, nil
}

func mapConnectionState(cs shipapi.ConnectionState) pb.PairingState {
	switch cs {
	case shipapi.ConnectionStateQueued,
		shipapi.ConnectionStateInitiated,
		shipapi.ConnectionStateReceivedPairingRequest,
		shipapi.ConnectionStateInProgress:
		return pb.PairingState_PAIRING_STATE_PENDING
	case shipapi.ConnectionStateTrusted,
		shipapi.ConnectionStateCompleted:
		return pb.PairingState_PAIRING_STATE_TRUSTED
	case shipapi.ConnectionStateRemoteDeniedTrust,
		shipapi.ConnectionStateError:
		return pb.PairingState_PAIRING_STATE_DENIED
	default:
		return pb.PairingState_PAIRING_STATE_UNSPECIFIED
	}
}

func (s *DeviceService) ListPairedDevices(_ context.Context, _ *pb.Empty) (*pb.ListPairedDevicesResponse, error) {
	return &pb.ListPairedDevicesResponse{}, nil
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
