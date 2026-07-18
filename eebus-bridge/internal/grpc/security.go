package grpc

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/config"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const bearerPrefix = "Bearer "

type bearerTokenCredentials struct {
	token string
}

func (c bearerTokenCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": bearerPrefix + c.token}, nil
}

func (bearerTokenCredentials) RequireTransportSecurity() bool { return true }

func loadServerSecurity(security config.GRPCSecurityConfig, serving *atomic.Bool) ([]grpcgo.ServerOption, error) {
	readinessUnary := func(ctx context.Context, req any, info *grpcgo.UnaryServerInfo, handler grpcgo.UnaryHandler) (any, error) {
		if !serving.Load() && !isHealthMethod(info.FullMethod) {
			return nil, status.Error(codes.Unavailable, "bridge is not ready")
		}
		return handler(ctx, req)
	}
	readinessStream := func(srv any, stream grpcgo.ServerStream, info *grpcgo.StreamServerInfo, handler grpcgo.StreamHandler) error {
		if !serving.Load() && !isHealthMethod(info.FullMethod) {
			return status.Error(codes.Unavailable, "bridge is not ready")
		}
		return handler(srv, stream)
	}
	if security.Mode == "" || security.Mode == config.GRPCSecurityModeLoopback {
		return []grpcgo.ServerOption{
			grpcgo.ChainUnaryInterceptor(readinessUnary),
			grpcgo.ChainStreamInterceptor(readinessStream),
		}, nil
	}
	if security.Mode != config.GRPCSecurityModeTLSToken {
		return nil, fmt.Errorf("unsupported gRPC security mode %q", security.Mode)
	}
	certificate, err := tls.LoadX509KeyPair(security.TLSCertFile, security.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("loading gRPC TLS certificate/key: %w", err)
	}
	tokenBytes, err := os.ReadFile(security.TokenFile) // #nosec G304 -- operator-supplied configuration path
	if err != nil {
		return nil, fmt.Errorf("reading gRPC token file %q: %w", security.TokenFile, err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return nil, fmt.Errorf("gRPC token file %q is empty", security.TokenFile)
	}
	authenticate := func(ctx context.Context) error {
		values := metadata.ValueFromIncomingContext(ctx, "authorization")
		provided := ""
		if len(values) == 1 {
			provided = values[0]
		}
		expectedDigest := sha256.Sum256([]byte(bearerPrefix + token))
		providedDigest := sha256.Sum256([]byte(provided))
		if len(values) != 1 || subtle.ConstantTimeCompare(providedDigest[:], expectedDigest[:]) != 1 {
			return status.Error(codes.Unauthenticated, "valid bearer token required")
		}
		return nil
	}
	authUnary := func(ctx context.Context, req any, _ *grpcgo.UnaryServerInfo, handler grpcgo.UnaryHandler) (any, error) {
		if err := authenticate(ctx); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
	authStream := func(srv any, stream grpcgo.ServerStream, _ *grpcgo.StreamServerInfo, handler grpcgo.StreamHandler) error {
		if err := authenticate(stream.Context()); err != nil {
			return err
		}
		return handler(srv, stream)
	}
	return []grpcgo.ServerOption{
		grpcgo.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})),
		grpcgo.ChainUnaryInterceptor(authUnary, readinessUnary),
		grpcgo.ChainStreamInterceptor(authStream, readinessStream),
	}, nil
}

func isHealthMethod(fullMethod string) bool {
	return strings.HasPrefix(fullMethod, "/grpc.health.v1.Health/")
}

// ClientSecurityConfig is shared by bridge-side clients such as the Docker
// healthcheck and eebus-watch. TLS mode always attaches the file-backed token.
type ClientSecurityConfig struct {
	Mode       config.GRPCSecurityMode
	CACertFile string
	TokenFile  string
	ServerName string
}

// NewClient creates a gRPC client using the same security modes as the server.
func NewClient(target string, security ClientSecurityConfig) (*grpcgo.ClientConn, error) {
	if security.Mode == "" || security.Mode == config.GRPCSecurityModeLoopback {
		return grpcgo.NewClient(target, grpcgo.WithTransportCredentials(insecure.NewCredentials()))
	}
	if security.Mode != config.GRPCSecurityModeTLSToken {
		return nil, fmt.Errorf("unsupported gRPC security mode %q", security.Mode)
	}
	transport, err := credentials.NewClientTLSFromFile(security.CACertFile, security.ServerName)
	if err != nil {
		return nil, fmt.Errorf("loading gRPC CA certificate %q: %w", security.CACertFile, err)
	}
	tokenBytes, err := os.ReadFile(security.TokenFile) // #nosec G304 -- operator-supplied configuration path
	if err != nil {
		return nil, fmt.Errorf("reading gRPC token file %q: %w", security.TokenFile, err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return nil, fmt.Errorf("gRPC token file %q is empty", security.TokenFile)
	}
	return grpcgo.NewClient(
		target,
		grpcgo.WithTransportCredentials(transport),
		grpcgo.WithPerRPCCredentials(bearerTokenCredentials{token: token}),
	)
}

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
// only when the server is bound to loopback or protected by tls_token.
//
// These RPCs let a client inject grid/PV/battery values into EEBUS state that
// downstream equipment (e.g. the Vaillant VR940) consumes for PV-surplus
// optimisation and display. Refusing registration on exposed plaintext keeps
// that mutating surface inside a trusted transport boundary.
func RegisterPushServices(srv *Server, bind string, mode config.GRPCSecurityMode, grid *GridService, viz *VisualizationService) bool {
	if mode != config.GRPCSecurityModeTLSToken && !isLoopbackBind(bind) {
		return false
	}
	pb.RegisterGridServiceServer(srv.GRPCServer(), grid)
	pb.RegisterVisualizationServiceServer(srv.GRPCServer(), viz)
	return true
}
