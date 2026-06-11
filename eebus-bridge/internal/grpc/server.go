package grpc

import (
	"fmt"
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

type Server struct {
	grpcServer *grpc.Server
	listener   net.Listener
	bind       string
	port       int
	mu         sync.RWMutex
}

func NewServer(bind string, port int, enableReflection bool) *Server {
	grpcServer := grpc.NewServer()

	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	if enableReflection {
		reflection.Register(grpcServer)
	}

	return &Server{
		grpcServer: grpcServer,
		bind:       bind,
		port:       port,
	}
}

func (s *Server) GRPCServer() *grpc.Server {
	return s.grpcServer
}

func (s *Server) Start() error {
	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.bind, s.port))
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
