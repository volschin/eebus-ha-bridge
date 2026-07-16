package grpc

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/volschin/eebus-bridge/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// gracefulStopTimeout bounds how long Stop waits for in-flight RPCs (notably
// HA's long-lived SubscribeMeasurements/SubscribeLPCEvents/SubscribeDeviceEvents
// streams) to drain before forcing the connection closed. Without a bound,
// GracefulStop blocks until every open stream's context is canceled by its
// client, which HA's local_push streams never do on their own — a controlled
// shutdown (SIGTERM or the RF-06 watchdog) would then hang indefinitely
// instead of restarting.
const gracefulStopTimeout = 5 * time.Second

type Server struct {
	grpcServer *grpc.Server
	healthSrv  *health.Server
	listener   net.Listener
	bind       string
	port       int
	mu         sync.RWMutex
}

func NewServer(bind string, port int, enableReflection bool) *Server {
	server, err := NewServerWithSecurity(bind, port, enableReflection, config.GRPCSecurityConfig{Mode: config.GRPCSecurityModeLoopback})
	if err != nil {
		panic(err)
	}
	return server
}

func NewServerWithSecurity(bind string, port int, enableReflection bool, security config.GRPCSecurityConfig) (*Server, error) {
	if (security.Mode == "" || security.Mode == config.GRPCSecurityModeLoopback) && !isLoopbackBind(bind) {
		return nil, fmt.Errorf("gRPC loopback security mode requires a loopback bind, got %q", bind)
	}
	serverOptions, err := loadServerSecurity(security)
	if err != nil {
		return nil, err
	}
	grpcServer := grpc.NewServer(serverOptions...)

	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	if enableReflection {
		reflection.Register(grpcServer)
	}

	return &Server{
		grpcServer: grpcServer,
		healthSrv:  healthSrv,
		bind:       bind,
		port:       port,
	}, nil
}

// SetHealthy toggles the gRPC health status the Docker HEALTHCHECK probes.
// Used by the monitoring watchdog to surface a stuck SPINE entity binding
// before it force-exits the process for a restart.
func (s *Server) SetHealthy(healthy bool) {
	status := grpc_health_v1.HealthCheckResponse_SERVING
	if !healthy {
		status = grpc_health_v1.HealthCheckResponse_NOT_SERVING
	}
	s.healthSrv.SetServingStatus("", status)
}

func (s *Server) GRPCServer() *grpc.Server {
	return s.grpcServer
}

func (s *Server) Start() error {
	lis, err := net.Listen("tcp", net.JoinHostPort(s.bind, fmt.Sprintf("%d", s.port)))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.mu.Lock()
	s.listener = lis
	s.mu.Unlock()
	return s.grpcServer.Serve(lis)
}

func (s *Server) Addr() string {
	s.mu.RLock()
	lis := s.listener
	s.mu.RUnlock()
	if lis == nil {
		return ""
	}
	return lis.Addr().String()
}

func (s *Server) Stop() {
	done := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(gracefulStopTimeout):
		s.grpcServer.Stop()
		<-done
	}
}
