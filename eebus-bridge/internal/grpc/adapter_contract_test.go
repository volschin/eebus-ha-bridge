package grpc

import (
	"context"
	"strings"
	"testing"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/eebus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestServiceSKIContractRejectsMalformedExplicitSKI(t *testing.T) {
	ctx := context.Background()
	malformed := "not-a-ski-secret-token"
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "device read",
			call: func() error {
				_, err := NewDeviceService(nil, bus, "", registry, nil).
					GetDeviceStatus(ctx, &pb.DeviceRequest{Ski: malformed})
				return err
			},
		},
		{
			name: "device write",
			call: func() error {
				_, err := NewDeviceService(nil, bus, "", registry, nil).
					RegisterRemoteSKI(ctx, &pb.RegisterSKIRequest{Ski: malformed})
				return err
			},
		},
		{
			name: "lpc read",
			call: func() error {
				_, err := NewLPCService(nil, bus, registry).
					GetConsumptionLimit(ctx, &pb.DeviceRequest{Ski: malformed})
				return err
			},
		},
		{
			name: "lpc write",
			call: func() error {
				_, err := NewLPCService(nil, bus, registry).
					WriteConsumptionLimit(ctx, &pb.WriteLoadLimitRequest{Ski: malformed})
				return err
			},
		},
		{
			name: "lpc heartbeat mutation",
			call: func() error {
				_, err := NewLPCService(nil, bus, registry).
					StartHeartbeat(ctx, &pb.DeviceRequest{Ski: malformed})
				return err
			},
		},
		{
			name: "ohpcf read",
			call: func() error {
				_, err := NewOHPCFService(nil, bus, registry).
					GetCompressorFlexibility(ctx, &pb.DeviceRequest{Ski: malformed})
				return err
			},
		},
		{
			name: "ohpcf write",
			call: func() error {
				_, err := NewOHPCFService(nil, bus, registry).
					ControlCompressorFlexibility(ctx, &pb.ControlCompressorRequest{Ski: malformed})
				return err
			},
		},
		{
			name: "dhw read",
			call: func() error {
				_, err := NewDHWService(nil, nil, bus, registry).
					GetDHWSetpoint(ctx, &pb.DeviceRequest{Ski: malformed})
				return err
			},
		},
		{
			name: "dhw write",
			call: func() error {
				_, err := NewDHWService(nil, nil, bus, registry).
					SetDHWSetpoint(ctx, &pb.SetDHWSetpointRequest{Ski: malformed})
				return err
			},
		},
		{
			name: "hvac read",
			call: func() error {
				_, err := NewHVACService(nil, nil, nil, bus, registry).
					GetRoomHeating(ctx, &pb.DeviceRequest{Ski: malformed})
				return err
			},
		},
		{
			name: "hvac write",
			call: func() error {
				_, err := NewHVACService(nil, nil, nil, bus, registry).
					SetRoomHeatingTemperature(ctx, &pb.SetRoomHeatingTemperatureRequest{Ski: malformed})
				return err
			},
		},
		{
			name: "monitoring read",
			call: func() error {
				_, err := NewMonitoringService(nil, MonitoringReaders{}, bus, registry).
					GetPowerConsumption(ctx, &pb.DeviceRequest{Ski: malformed})
				return err
			},
		},
		{
			name: "monitoring diagnostics read",
			call: func() error {
				_, err := NewMonitoringService(nil, MonitoringReaders{}, bus, registry).
					GetDeviceDiagnostics(ctx, &pb.DeviceRequest{Ski: malformed})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if code := status.Code(err); code != codes.InvalidArgument {
				t.Fatalf("status code = %v, want %v (err: %v)", code, codes.InvalidArgument, err)
			}
			if strings.Contains(status.Convert(err).Message(), malformed) {
				t.Fatalf("error message exposes malformed SKI input: %q", status.Convert(err).Message())
			}
		})
	}
}

func TestServiceWriteContractRequiresSKI(t *testing.T) {
	ctx := context.Background()
	bus := eebus.NewEventBus()
	registry := eebus.NewDeviceRegistry()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "device register",
			call: func() error {
				_, err := NewDeviceService(nil, bus, "", registry, nil).
					RegisterRemoteSKI(ctx, &pb.RegisterSKIRequest{})
				return err
			},
		},
		{
			name: "device unregister",
			call: func() error {
				_, err := NewDeviceService(nil, bus, "", registry, nil).
					UnregisterRemoteSKI(ctx, &pb.RegisterSKIRequest{})
				return err
			},
		},
		{
			name: "lpc consumption",
			call: func() error {
				_, err := NewLPCService(nil, bus, registry).
					WriteConsumptionLimit(ctx, &pb.WriteLoadLimitRequest{})
				return err
			},
		},
		{
			name: "lpc failsafe",
			call: func() error {
				_, err := NewLPCService(nil, bus, registry).
					WriteFailsafeLimit(ctx, &pb.WriteFailsafeLimitRequest{})
				return err
			},
		},
		{
			name: "lpc start heartbeat",
			call: func() error {
				_, err := NewLPCService(nil, bus, registry).
					StartHeartbeat(ctx, &pb.DeviceRequest{})
				return err
			},
		},
		{
			name: "lpc stop heartbeat",
			call: func() error {
				_, err := NewLPCService(nil, bus, registry).
					StopHeartbeat(ctx, &pb.DeviceRequest{})
				return err
			},
		},
		{
			name: "ohpcf control",
			call: func() error {
				_, err := NewOHPCFService(nil, bus, registry).
					ControlCompressorFlexibility(ctx, &pb.ControlCompressorRequest{})
				return err
			},
		},
		{
			name: "dhw setpoint",
			call: func() error {
				_, err := NewDHWService(nil, nil, bus, registry).
					SetDHWSetpoint(ctx, &pb.SetDHWSetpointRequest{})
				return err
			},
		},
		{
			name: "dhw boost",
			call: func() error {
				_, err := NewDHWService(nil, nil, bus, registry).
					SetDHWBoost(ctx, &pb.SetDHWBoostRequest{})
				return err
			},
		},
		{
			name: "dhw operation mode",
			call: func() error {
				_, err := NewDHWService(nil, nil, bus, registry).
					SetDHWOperationMode(ctx, &pb.SetDHWOperationModeRequest{})
				return err
			},
		},
		{
			name: "hvac temperature",
			call: func() error {
				_, err := NewHVACService(nil, nil, nil, bus, registry).
					SetRoomHeatingTemperature(ctx, &pb.SetRoomHeatingTemperatureRequest{})
				return err
			},
		},
		{
			name: "hvac mode",
			call: func() error {
				_, err := NewHVACService(nil, nil, nil, bus, registry).
					SetRoomHeatingMode(ctx, &pb.SetRoomHeatingModeRequest{})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code := status.Code(tt.call()); code != codes.InvalidArgument {
				t.Fatalf("status code = %v, want %v", code, codes.InvalidArgument)
			}
		})
	}
}
