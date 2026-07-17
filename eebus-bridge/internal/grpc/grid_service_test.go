package grpc_test

import (
	"context"
	"testing"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func ptrFloat64(value float64) *float64 {
	return &value
}

func TestPublishGridDataValidation(t *testing.T) {
	// nil provider: the service must report Unavailable, never panic, so the
	// HA integration can detect the provider is disabled (UNIMPLEMENTED-like).
	svc := bridgegrpc.NewGridService(nil)
	ctx := context.Background()

	if _, err := svc.PublishGridData(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("PublishGridData(nil request) code = %v, want InvalidArgument", status.Code(err))
	}

	feedIn := 12340.0
	_, err := svc.PublishGridData(ctx, &pb.GridData{PowerW: ptrFloat64(-1500), FeedInWh: &feedIn})
	if status.Code(err) != codes.Unavailable {
		t.Errorf("PublishGridData with nil provider code = %v, want Unavailable", status.Code(err))
	}
}
