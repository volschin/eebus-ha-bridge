package grpc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
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
const maxConcurrentStreams = 64

type Server struct {
	grpcServer    *grpc.Server
	healthSrv     *health.Server
	listener      net.Listener
	bind          string
	port          int
	mu            sync.RWMutex
	ready         chan struct{}
	readyOnce     sync.Once
	startErr      error
	serving       *atomic.Bool
	deviceHealthy atomic.Bool
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
	serving := &atomic.Bool{}
	serverOptions, err := loadServerSecurity(security, serving)
	if err != nil {
		return nil, err
	}
	serverOptions = append(serverOptions, grpc.MaxConcurrentStreams(maxConcurrentStreams))
	grpcServer := grpc.NewServer(serverOptions...)

	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	if enableReflection {
		reflection.Register(grpcServer)
	}

	server := &Server{
		grpcServer: grpcServer,
		healthSrv:  healthSrv,
		bind:       bind,
		port:       port,
		ready:      make(chan struct{}),
		serving:    serving,
	}
	server.deviceHealthy.Store(true)
	return server, nil
}

// SetHealthy atomically controls both the health-service response and the
// application-RPC readiness gate. This is the bridge STARTUP/SHUTDOWN
// lifecycle signal only — call it exactly once each way, from Start's commit
// point and from Stop. Per-device connectivity must go through
// SetDeviceHealthy instead: gating every client RPC on "is every device fully
// healthy right now" would reject HA's calls (GetDeviceStatus,
// GetDeviceSnapshot, ...) during the ordinary reconnect/grace-period window
// when they matter most.
func (s *Server) SetHealthy(healthy bool) {
	s.serving.Store(healthy)
	s.publishHealth()
}

// SetDeviceHealthy reports device-level connectivity to the gRPC health
// service (Docker/k8s probes) without touching the RPC readiness gate, so a
// disconnected/recovering device is visible to orchestration tooling while
// client RPCs keep working.
func (s *Server) SetDeviceHealthy(healthy bool) {
	s.deviceHealthy.Store(healthy)
	s.publishHealth()
}

func (s *Server) publishHealth() {
	status := grpc_health_v1.HealthCheckResponse_SERVING
	if !s.serving.Load() || !s.deviceHealthy.Load() {
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
		s.mu.Lock()
		s.startErr = err
		s.mu.Unlock()
		s.readyOnce.Do(func() { close(s.ready) })
		return fmt.Errorf("listen: %w", err)
	}
	s.mu.Lock()
	s.listener = lis
	s.mu.Unlock()
	s.readyOnce.Do(func() { close(s.ready) })
	return s.grpcServer.Serve(lis)
}

// WaitReady reports whether Start acquired its listener. It lets the
// application keep health NOT_SERVING until the complete startup path has
// succeeded without using sleeps as a readiness contract.
func (s *Server) WaitReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ready:
		s.mu.RLock()
		err := s.startErr
		s.mu.RUnlock()
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	}
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
