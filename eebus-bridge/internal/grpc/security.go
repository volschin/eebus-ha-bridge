package grpc

import (
	"net"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
)

// isLoopbackBind reports whether the gRPC server's bind address is restricted to
// the local host. A bare "localhost" is treated as loopback; an IP is loopback
// when it falls in 127.0.0.0/8 or ::1. Anything else — an empty bind (which
// net.Listen treats as all interfaces), 0.0.0.0, :: or a routable address/host —
// is considered exposed.
func isLoopbackBind(bind string) bool {
	if bind == "localhost" {
		return true
	}
	ip := net.ParseIP(bind)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// RegisterPushServices registers the provider push services (grid/PV/battery)
// only when the gRPC server is bound to loopback, and reports whether it did.
//
// These RPCs let a client inject grid/PV/battery values into EEBUS state that
// downstream equipment (e.g. the Vaillant VR940) consumes for PV-surplus
// optimisation and display. The server carries no transport credentials or
// auth interceptor, so registering them on a routable bind would let any
// reachable client forge those readings. Refusing registration off loopback
// keeps the mutating surface inside the host's trust boundary; the read-only
// device/LPC/monitoring services are unaffected.
func RegisterPushServices(srv *Server, bind string, grid *GridService, viz *VisualizationService) bool {
	if !isLoopbackBind(bind) {
		return false
	}
	pb.RegisterGridServiceServer(srv.GRPCServer(), grid)
	pb.RegisterVisualizationServiceServer(srv.GRPCServer(), viz)
	return true
}
