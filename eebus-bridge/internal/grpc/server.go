package grpc

import (
	"fmt"
	"net"
	"sync"

	"github.com/volschin/eebus-bridge/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

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
	s.grpcServer.GracefulStop()
}
