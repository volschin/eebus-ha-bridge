package grpc_test

import (
	"context"
	"testing"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPublishPVDataValidation(t *testing.T) {
	// nil providers: the service must report Unavailable, never panic, so the HA
	// integration can detect the provider is disabled.
	svc := bridgegrpc.NewVisualizationService(nil, nil)
	ctx := context.Background()

	if _, err := svc.PublishPVData(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("PublishPVData(nil request) code = %v, want InvalidArgument", status.Code(err))
	}

	yield := 12340.0
	if _, err := svc.PublishPVData(ctx, &pb.PVData{PowerW: 1500, YieldWh: &yield}); status.Code(err) != codes.Unavailable {
		t.Errorf("PublishPVData with nil provider code = %v, want Unavailable", status.Code(err))
	}
}

func TestPublishBatteryDataValidation(t *testing.T) {
	svc := bridgegrpc.NewVisualizationService(nil, nil)
	ctx := context.Background()

	if _, err := svc.PublishBatteryData(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("PublishBatteryData(nil request) code = %v, want InvalidArgument", status.Code(err))
	}

	soc := 55.0
	if _, err := svc.PublishBatteryData(ctx, &pb.BatteryData{PowerW: -800, StateOfChargePct: &soc}); status.Code(err) != codes.Unavailable {
		t.Errorf("PublishBatteryData with nil provider code = %v, want Unavailable", status.Code(err))
	}
}
