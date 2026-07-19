package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"github.com/volschin/eebus-bridge/internal/config"
	bridgegrpc "github.com/volschin/eebus-bridge/internal/grpc"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type watchRPCFixture struct {
	mu          sync.Mutex
	registered  string
	failDetails bool
}

type watchDeviceServer struct {
	pb.UnimplementedDeviceServiceServer
	fixture *watchRPCFixture
}

func (s watchDeviceServer) GetStatus(context.Context, *pb.Empty) (*pb.ServiceStatus, error) {
	return &pb.ServiceStatus{Running: true, LocalSki: "local-ski"}, nil
}

func (s watchDeviceServer) ListPairedDevices(context.Context, *pb.Empty) (*pb.ListPairedDevicesResponse, error) {
	return &pb.ListPairedDevicesResponse{Devices: []*pb.PairedDevice{
		nil,
		{Ski: "remote-ski", Brand: "Paired Brand", Model: "Paired Model", Serial: "paired-serial", DeviceType: "HeatPump", SupportedUseCases: []string{"MPC", "LPC"}},
	}}, nil
}

func (s watchDeviceServer) ListDiscoveredDevices(context.Context, *pb.Empty) (*pb.ListDevicesResponse, error) {
	if s.fixture.failDetails {
		return nil, status.Error(codes.Unavailable, "discovery unavailable")
	}
	return &pb.ListDevicesResponse{Devices: []*pb.DiscoveredDevice{
		nil,
		{Ski: "discovered-ski", Brand: "Discovered Brand", Model: "Discovered Model", Serial: "discovered-serial", DeviceType: "Gateway", Host: "192.0.2.1"},
	}}, nil
}

func (s watchDeviceServer) RegisterRemoteSKI(_ context.Context, request *pb.RegisterSKIRequest) (*pb.Empty, error) {
	s.fixture.mu.Lock()
	s.fixture.registered = request.GetSki()
	s.fixture.mu.Unlock()
	return &pb.Empty{}, nil
}

func (f *watchRPCFixture) registeredSKI() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registered
}

type watchMonitoringServer struct {
	pb.UnimplementedMonitoringServiceServer
	fixture *watchRPCFixture
}

func (s watchMonitoringServer) GetPowerConsumption(context.Context, *pb.DeviceRequest) (*pb.PowerMeasurement, error) {
	if s.fixture.failDetails {
		return nil, status.Error(codes.NotFound, "power unavailable")
	}
	return &pb.PowerMeasurement{Watts: 1234.5}, nil
}

func (s watchMonitoringServer) GetEnergyConsumed(context.Context, *pb.DeviceRequest) (*pb.EnergyMeasurement, error) {
	if s.fixture.failDetails {
		return nil, status.Error(codes.Internal, "energy failed")
	}
	return &pb.EnergyMeasurement{KilowattHours: 42.25}, nil
}

func (s watchMonitoringServer) GetMeasurements(context.Context, *pb.DeviceRequest) (*pb.MeasurementList, error) {
	if s.fixture.failDetails {
		return nil, status.Error(codes.Unavailable, "measurements unavailable")
	}
	return &pb.MeasurementList{Measurements: []*pb.MeasurementEntry{
		nil,
		{Type: "room_temperature", Value: 21.5, Unit: "degC", Timestamp: timestamppb.New(time.Unix(10, 0))},
	}}, nil
}

type watchLPCServer struct {
	pb.UnimplementedLPCServiceServer
	fixture *watchRPCFixture
}

func (s watchLPCServer) GetConsumptionLimit(context.Context, *pb.DeviceRequest) (*pb.LoadLimit, error) {
	if s.fixture.failDetails {
		return nil, status.Error(codes.NotFound, "limit unavailable")
	}
	return &pb.LoadLimit{ValueWatts: 4200, IsActive: true, IsChangeable: true}, nil
}

func (s watchLPCServer) GetFailsafeLimit(context.Context, *pb.DeviceRequest) (*pb.FailsafeLimit, error) {
	if s.fixture.failDetails {
		return nil, status.Error(codes.Unimplemented, "failsafe unavailable")
	}
	return &pb.FailsafeLimit{ValueWatts: 5000, DurationMinimumSeconds: 60}, nil
}

func (s watchLPCServer) GetHeartbeatStatus(context.Context, *pb.DeviceRequest) (*pb.HeartbeatStatus, error) {
	if s.fixture.failDetails {
		return nil, status.Error(codes.Unavailable, "heartbeat unavailable")
	}
	return &pb.HeartbeatStatus{Running: true, WithinDuration: true}, nil
}

type watchOHPCFServer struct {
	pb.UnimplementedOHPCFServiceServer
	fixture *watchRPCFixture
}

func (s watchOHPCFServer) GetCompressorFlexibility(context.Context, *pb.DeviceRequest) (*pb.CompressorFlexibility, error) {
	if s.fixture.failDetails {
		return nil, status.Error(codes.NotFound, "flexibility unavailable")
	}
	return &pb.CompressorFlexibility{
		Available: true, State: pb.CompressorPowerConsumptionState_COMPRESSOR_STATE_AVAILABLE,
		IsStoppable: true, IsPausable: true, MinimalRunSeconds: 120, MinimalPauseSeconds: 30,
	}, nil
}

func startWatchTestServer(t *testing.T, fixture *watchRPCFixture) (string, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpcgo.NewServer()
	pb.RegisterDeviceServiceServer(server, watchDeviceServer{fixture: fixture})
	pb.RegisterMonitoringServiceServer(server, watchMonitoringServer{fixture: fixture})
	pb.RegisterLPCServiceServer(server, watchLPCServer{fixture: fixture})
	pb.RegisterOHPCFServiceServer(server, watchOHPCFServer{fixture: fixture})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

func TestSelectTargetSKI(t *testing.T) {
	t.Parallel()

	devices := []*pb.PairedDevice{
		{},
		{Ski: "  "},
		{Ski: "123456"},
	}

	if got := selectTargetSKI("", devices); got != "123456" {
		t.Fatalf("selectTargetSKI() = %q, want %q", got, "123456")
	}
	if got := selectTargetSKI(" explicit ", devices); got != "explicit" {
		t.Fatalf("selectTargetSKI() = %q, want %q", got, "explicit")
	}
	if got := selectTargetSKI("", nil); got != "" {
		t.Fatalf("selectTargetSKI() = %q, want empty string", got)
	}
}

func TestSortedMeasurements(t *testing.T) {
	t.Parallel()

	rows := sortedMeasurements([]*pb.MeasurementEntry{
		{Type: "b", Value: 2, Unit: "W", Timestamp: timestamppb.New(unixTime(t, 2))},
		{Type: "a", Value: 1, Unit: "W", Timestamp: timestamppb.New(unixTime(t, 1))},
		{Type: "a", Value: 3, Unit: "W", Timestamp: timestamppb.New(unixTime(t, 3))},
		nil,
	})

	if len(rows) != 3 {
		t.Fatalf("sortedMeasurements() len = %d, want 3", len(rows))
	}
	if rows[0].Type != "a" || rows[0].Timestamp != "1970-01-01T00:00:01Z" {
		t.Fatalf("sortedMeasurements()[0] = %+v, want type a at 1970-01-01T00:00:01Z", rows[0])
	}
	if rows[1].Type != "a" || rows[1].Timestamp != "1970-01-01T00:00:03Z" {
		t.Fatalf("sortedMeasurements()[1] = %+v, want type a at 1970-01-01T00:00:03Z", rows[1])
	}
	if rows[2].Type != "b" {
		t.Fatalf("sortedMeasurements()[2] = %+v, want type b", rows[2])
	}
}

func TestIgnorableErr(t *testing.T) {
	t.Parallel()

	for _, code := range []codes.Code{codes.NotFound, codes.Unavailable, codes.Unimplemented} {
		if !isIgnorableErr(status.Error(code, "test")) {
			t.Fatalf("isIgnorableErr(%v) = false, want true", code)
		}
	}
	if isIgnorableErr(status.Error(codes.Internal, "test")) {
		t.Fatal("isIgnorableErr(Internal) = true, want false")
	}
}

func TestFormatTimestamp(t *testing.T) {
	t.Parallel()

	if got := formatTimestamp(nil); got != "-" {
		t.Fatalf("formatTimestamp(nil) = %q, want -", got)
	}
	if got := formatTimestamp(timestamppb.New(unixTime(t, 5))); got != "1970-01-01T00:00:05Z" {
		t.Fatalf("formatTimestamp() = %q, want 1970-01-01T00:00:05Z", got)
	}
}

func TestRunOnceCollectsAndRendersCompleteSnapshot(t *testing.T) {
	fixture := &watchRPCFixture{}
	host, port := startWatchTestServer(t, fixture)
	var out bytes.Buffer
	var errOut bytes.Buffer

	err := run(
		context.Background(), host, port, "remote-ski", time.Second, true, true, false, true,
		bridgegrpc.ClientSecurityConfig{Mode: config.GRPCSecurityModeLoopback}, &out, &errOut,
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := fixture.registeredSKI(); got != "remote-ski" {
		t.Fatalf("registered SKI = %q, want remote-ski", got)
	}
	for _, want := range []string{
		"EEBUS watch", "local-ski", "Discovered Brand", "Paired Brand", "room_temperature",
		"1234.5 W", "42.250 kWh", "4200.0 W", "5000.0 W / 60 s", "Heartbeat", "OHPCF",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output does not contain %q:\n%s", want, out.String())
		}
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestRunValidatesIntervalAndRegistration(t *testing.T) {
	if err := run(context.Background(), "127.0.0.1", 1, "", 0, true, false, false, false,
		bridgegrpc.ClientSecurityConfig{Mode: config.GRPCSecurityModeLoopback}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "interval") {
		t.Fatalf("invalid interval error = %v", err)
	}

	fixture := &watchRPCFixture{}
	host, port := startWatchTestServer(t, fixture)
	err := run(context.Background(), host, port, "  ", time.Second, true, false, false, true,
		bridgegrpc.ClientSecurityConfig{Mode: config.GRPCSecurityModeLoopback}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--register requires --ski") {
		t.Fatalf("empty registration SKI error = %v", err)
	}
}

func TestCollectSnapshotFiltersExpectedErrorsAndReportsUnexpectedErrors(t *testing.T) {
	fixture := &watchRPCFixture{failDetails: true}
	host, port := startWatchTestServer(t, fixture)
	conn, err := bridgegrpc.NewClient(net.JoinHostPort(host, strconv.Itoa(port)), bridgegrpc.ClientSecurityConfig{Mode: config.GRPCSecurityModeLoopback})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	collect := func(debug bool) *snapshot {
		t.Helper()
		snap, err := collectSnapshot(
			context.Background(), pb.NewDeviceServiceClient(conn), pb.NewMonitoringServiceClient(conn),
			pb.NewLPCServiceClient(conn), pb.NewOHPCFServiceClient(conn), host, port, "", false, debug,
		)
		if err != nil {
			t.Fatalf("collectSnapshot(debug=%t): %v", debug, err)
		}
		return snap
	}

	withoutDebug := collect(false)
	if len(withoutDebug.Errors) != 1 || !strings.Contains(withoutDebug.Errors[0], "energy") {
		t.Fatalf("non-debug errors = %v, want only internal energy error", withoutDebug.Errors)
	}
	withDebug := collect(true)
	if len(withDebug.Errors) != 7 {
		t.Fatalf("debug errors = %v, want all seven detail errors", withDebug.Errors)
	}
	if withDebug.SelectedSKI != "remote-ski" {
		t.Fatalf("selected SKI = %q, want first paired device", withDebug.SelectedSKI)
	}
}

func TestRenderSnapshotHandlesEmptyValues(t *testing.T) {
	var out bytes.Buffer
	renderSnapshot(&out, &snapshot{Host: "localhost", Port: 50051})
	for _, want := range []string{
		"No visible SHIP/mDNS devices", "No paired devices available",
		"No monitoring measurements available", "No readable state values", "Local SKI",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("empty output does not contain %q:\n%s", want, out.String())
		}
	}
	if got := blankIfEmpty(" value "); got != " value " {
		t.Fatalf("blankIfEmpty(non-empty) = %q", got)
	}
	if got := blankIfEmpty(" \t "); got != "-" {
		t.Fatalf("blankIfEmpty(blank) = %q", got)
	}
	if !shouldReportErr(status.Error(codes.Internal, "failed"), false) || shouldReportErr(status.Error(codes.NotFound, "missing"), false) {
		t.Fatal("shouldReportErr returned unexpected classification")
	}
	if !shouldReportErr(status.Error(codes.NotFound, "missing"), true) || !isIgnorableErr(nil) {
		t.Fatal("debug or nil error classification was unexpected")
	}
}

func TestRunReturnsContextCancellationAfterSnapshot(t *testing.T) {
	fixture := &watchRPCFixture{}
	host, port := startWatchTestServer(t, fixture)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(25*time.Millisecond, cancel)
	var out bytes.Buffer
	err := run(ctx, host, port, "remote-ski", 5*time.Millisecond, false, true, false, false,
		bridgegrpc.ClientSecurityConfig{Mode: config.GRPCSecurityModeLoopback}, &out, &bytes.Buffer{})
	if !errors.Is(err, context.Canceled) && status.Code(err) != codes.Canceled {
		t.Fatalf("run cancellation error = %v", err)
	}
	if !strings.Contains(out.String(), "\033[H\033[2J") {
		t.Fatalf("repeated output did not clear screen: %q", out.String())
	}
}

func unixTime(t *testing.T, seconds int64) time.Time {
	t.Helper()
	return time.Unix(seconds, 0).UTC()
}
