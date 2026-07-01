package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	pb "github.com/volschin/eebus-bridge/gen/proto/eebus/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type snapshot struct {
	Host          string
	Port          int
	RequestedSKI  string
	SelectedSKI   string
	LocalSKI      string
	Connected     bool
	Registered    bool
	Discovered    []*pb.DiscoveredDevice
	PairedDevices []*pb.PairedDevice
	Status        *pb.ServiceStatus
	Power         *pb.PowerMeasurement
	Energy        *pb.EnergyMeasurement
	Measurements  []*pb.MeasurementEntry
	Consumption   *pb.LoadLimit
	Failsafe      *pb.FailsafeLimit
	Heartbeat     *pb.HeartbeatStatus
	Flexibility   *pb.CompressorFlexibility
	Errors        []string
}

func main() {
	host := flag.String("host", "127.0.0.1", "bridge gRPC host")
	port := flag.Int("port", 50051, "bridge gRPC port")
	ski := flag.String("ski", "", "remote device SKI to inspect; defaults to the first paired device")
	interval := flag.Duration("interval", 2*time.Second, "refresh interval")
	once := flag.Bool("once", false, "print a single snapshot and exit")
	noClear := flag.Bool("no-clear", false, "do not clear the terminal between snapshots")
	debug := flag.Bool("debug", false, "show all RPC errors, including not found/unavailable")
	register := flag.Bool("register", false, "register/trust the requested SKI before reading")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, *host, *port, *ski, *interval, *once, !*noClear, *debug, *register, os.Stdout, os.Stderr); err != nil {
		log.Fatalf("%v", err)
	}
}

func run(
	ctx context.Context,
	host string,
	port int,
	requestedSKI string,
	interval time.Duration,
	once bool,
	clearScreen bool,
	debug bool,
	register bool,
	out io.Writer,
	errOut io.Writer,
) error {
	if interval <= 0 {
		return fmt.Errorf("interval must be > 0")
	}

	conn, err := grpc.NewClient(
		fmt.Sprintf("%s:%d", host, port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dial bridge: %w", err)
	}
	defer func() { _ = conn.Close() }()

	client := pb.NewDeviceServiceClient(conn)
	monitoring := pb.NewMonitoringServiceClient(conn)
	lpc := pb.NewLPCServiceClient(conn)
	ohpcf := pb.NewOHPCFServiceClient(conn)

	registered := false
	if register {
		if strings.TrimSpace(requestedSKI) == "" {
			return fmt.Errorf("--register requires --ski")
		}
		rpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := client.RegisterRemoteSKI(rpcCtx, &pb.RegisterSKIRequest{Ski: requestedSKI})
		cancel()
		if err != nil {
			return fmt.Errorf("register remote SKI: %w", err)
		}
		registered = true
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	first := true
	for {
		snap, err := collectSnapshot(ctx, client, monitoring, lpc, ohpcf, host, port, requestedSKI, registered, debug)
		if err != nil {
			return err
		}
		if clearScreen && !first {
			fmt.Fprint(out, "\033[H\033[2J")
		}
		renderSnapshot(out, snap)
		first = false

		if once {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func collectSnapshot(
	ctx context.Context,
	device pb.DeviceServiceClient,
	monitoring pb.MonitoringServiceClient,
	lpc pb.LPCServiceClient,
	ohpcf pb.OHPCFServiceClient,
	host string,
	port int,
	requestedSKI string,
	registered bool,
	debug bool,
) (*snapshot, error) {
	rpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	statusResp, err := device.GetStatus(rpcCtx, &pb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("get status: %w", err)
	}

	pairedResp, err := device.ListPairedDevices(rpcCtx, &pb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("list paired devices: %w", err)
	}

	discoveredResp, err := device.ListDiscoveredDevices(rpcCtx, &pb.Empty{})
	if err != nil && debug {
		log.Printf("list discovered devices: %v", err)
	}

	selectedSKI := selectTargetSKI(requestedSKI, pairedResp.Devices)
	snap := &snapshot{
		Host:          host,
		Port:          port,
		RequestedSKI:  requestedSKI,
		SelectedSKI:   selectedSKI,
		LocalSKI:      statusResp.GetLocalSki(),
		Connected:     statusResp.GetRunning(),
		Registered:    registered,
		PairedDevices: pairedResp.Devices,
		Status:        statusResp,
	}
	if discoveredResp != nil {
		snap.Discovered = discoveredResp.GetDevices()
	}

	req := &pb.DeviceRequest{Ski: selectedSKI}
	if power, err := monitoring.GetPowerConsumption(rpcCtx, req); err == nil {
		snap.Power = power
	} else if shouldReportErr(err, debug) {
		snap.Errors = append(snap.Errors, fmt.Sprintf("power: %v", err))
	}

	if energy, err := monitoring.GetEnergyConsumed(rpcCtx, req); err == nil {
		snap.Energy = energy
	} else if shouldReportErr(err, debug) {
		snap.Errors = append(snap.Errors, fmt.Sprintf("energy: %v", err))
	}

	if measurements, err := monitoring.GetMeasurements(rpcCtx, req); err == nil {
		snap.Measurements = measurements.GetMeasurements()
	} else if shouldReportErr(err, debug) {
		snap.Errors = append(snap.Errors, fmt.Sprintf("measurements: %v", err))
	}

	if limit, err := lpc.GetConsumptionLimit(rpcCtx, req); err == nil {
		snap.Consumption = limit
	} else if shouldReportErr(err, debug) {
		snap.Errors = append(snap.Errors, fmt.Sprintf("lpc limit: %v", err))
	}

	if failsafe, err := lpc.GetFailsafeLimit(rpcCtx, req); err == nil {
		snap.Failsafe = failsafe
	} else if shouldReportErr(err, debug) {
		snap.Errors = append(snap.Errors, fmt.Sprintf("failsafe: %v", err))
	}

	if heartbeat, err := lpc.GetHeartbeatStatus(rpcCtx, req); err == nil {
		snap.Heartbeat = heartbeat
	} else if shouldReportErr(err, debug) {
		snap.Errors = append(snap.Errors, fmt.Sprintf("heartbeat: %v", err))
	}

	if flex, err := ohpcf.GetCompressorFlexibility(rpcCtx, req); err == nil {
		snap.Flexibility = flex
	} else if shouldReportErr(err, debug) {
		snap.Errors = append(snap.Errors, fmt.Sprintf("ohpcf: %v", err))
	}

	return snap, nil
}

func selectTargetSKI(requested string, devices []*pb.PairedDevice) string {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested
	}
	for _, device := range devices {
		if device != nil && strings.TrimSpace(device.GetSki()) != "" {
			return device.GetSki()
		}
	}
	return ""
}

func isIgnorableErr(err error) bool {
	if err == nil {
		return true
	}
	switch status.Code(err) {
	case codes.NotFound, codes.Unavailable, codes.Unimplemented:
		return true
	default:
		return false
	}
}

func shouldReportErr(err error, debug bool) bool {
	return debug || !isIgnorableErr(err)
}

func renderSnapshot(out io.Writer, snap *snapshot) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "EEBUS watch")
	fmt.Fprintln(w, "===========")
	fmt.Fprintf(w, "Bridge\t%s:%d\n", snap.Host, snap.Port)
	fmt.Fprintf(w, "Running\t%t\n", snap.Connected)
	fmt.Fprintf(w, "Local SKI\t%s\n", blankIfEmpty(snap.LocalSKI))
	fmt.Fprintf(w, "Requested SKI\t%s\n", blankIfEmpty(snap.RequestedSKI))
	fmt.Fprintf(w, "Selected SKI\t%s\n", blankIfEmpty(snap.SelectedSKI))
	fmt.Fprintf(w, "Register sent\t%t\n", snap.Registered)
	fmt.Fprintf(w, "Discovered devices\t%d\n", len(snap.Discovered))
	fmt.Fprintf(w, "Paired devices\t%d\n", len(snap.PairedDevices))

	if len(snap.Discovered) > 0 {
		fmt.Fprintln(w, "\nDiscovered devices")
		fmt.Fprintln(w, "------------------")
		fmt.Fprintln(w, "SKI\tBrand\tModel\tSerial\tType\tHost")
		devices := append([]*pb.DiscoveredDevice(nil), snap.Discovered...)
		sort.Slice(devices, func(i, j int) bool {
			return devices[i].GetSki() < devices[j].GetSki()
		})
		for _, device := range devices {
			if device == nil {
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				blankIfEmpty(device.GetSki()),
				blankIfEmpty(device.GetBrand()),
				blankIfEmpty(device.GetModel()),
				blankIfEmpty(device.GetSerial()),
				blankIfEmpty(device.GetDeviceType()),
				blankIfEmpty(device.GetHost()),
			)
		}
	} else {
		fmt.Fprintln(w, "\nDiscovered devices")
		fmt.Fprintln(w, "------------------")
		fmt.Fprintln(w, "No visible SHIP/mDNS devices")
	}

	if len(snap.PairedDevices) > 0 {
		fmt.Fprintln(w, "\nPaired devices")
		fmt.Fprintln(w, "--------------")
		fmt.Fprintln(w, "SKI\tBrand\tModel\tSerial\tType\tUse cases")
		devices := append([]*pb.PairedDevice(nil), snap.PairedDevices...)
		sort.Slice(devices, func(i, j int) bool {
			return devices[i].GetSki() < devices[j].GetSki()
		})
		for _, device := range devices {
			if device == nil {
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				blankIfEmpty(device.GetSki()),
				blankIfEmpty(device.GetBrand()),
				blankIfEmpty(device.GetModel()),
				blankIfEmpty(device.GetSerial()),
				blankIfEmpty(device.GetDeviceType()),
				strings.Join(device.GetSupportedUseCases(), ", "),
			)
		}
	} else {
		fmt.Fprintln(w, "\nPaired devices")
		fmt.Fprintln(w, "--------------")
		fmt.Fprintln(w, "No paired devices available")
	}

	fmt.Fprintln(w, "\nMeasurements")
	fmt.Fprintln(w, "------------")
	if len(snap.Measurements) == 0 {
		fmt.Fprintln(w, "No monitoring measurements available")
	} else {
		fmt.Fprintln(w, "Type\tValue\tUnit\tTimestamp")
		for _, row := range sortedMeasurements(snap.Measurements) {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", row.Type, row.Value, row.Unit, row.Timestamp)
		}
	}

	fmt.Fprintln(w, "\nState")
	fmt.Fprintln(w, "-----")
	emptyState := true
	if snap.Power != nil {
		emptyState = false
		fmt.Fprintf(w, "Power\t%.1f W\n", snap.Power.GetWatts())
	}
	if snap.Energy != nil {
		emptyState = false
		fmt.Fprintf(w, "Energy consumed\t%.3f kWh\n", snap.Energy.GetKilowattHours())
	}
	if snap.Consumption != nil {
		emptyState = false
		fmt.Fprintf(w, "LPC limit\t%.1f W (active=%t changeable=%t)\n",
			snap.Consumption.GetValueWatts(),
			snap.Consumption.GetIsActive(),
			snap.Consumption.GetIsChangeable(),
		)
	}
	if snap.Failsafe != nil {
		emptyState = false
		fmt.Fprintf(w, "Failsafe\t%.1f W / %d s\n",
			snap.Failsafe.GetValueWatts(),
			snap.Failsafe.GetDurationMinimumSeconds(),
		)
	}
	if snap.Heartbeat != nil {
		emptyState = false
		fmt.Fprintf(w, "Heartbeat\trunning=%t within_duration=%t\n",
			snap.Heartbeat.GetRunning(),
			snap.Heartbeat.GetWithinDuration(),
		)
	}
	if snap.Flexibility != nil {
		emptyState = false
		fmt.Fprintf(w, "OHPCF\tavailable=%t state=%s stoppable=%t pausable=%t min_run=%ds min_pause=%ds\n",
			snap.Flexibility.GetAvailable(),
			snap.Flexibility.GetState().String(),
			snap.Flexibility.GetIsStoppable(),
			snap.Flexibility.GetIsPausable(),
			snap.Flexibility.GetMinimalRunSeconds(),
			snap.Flexibility.GetMinimalPauseSeconds(),
		)
	}
	if emptyState {
		fmt.Fprintln(w, "No readable state values")
	}

	if len(snap.Errors) > 0 {
		fmt.Fprintln(w, "\nErrors")
		fmt.Fprintln(w, "------")
		for _, item := range snap.Errors {
			fmt.Fprintf(w, "%s\n", item)
		}
	}

	_ = w.Flush()
}

type measurementRow struct {
	Type      string
	Value     string
	Unit      string
	Timestamp string
}

func sortedMeasurements(measurements []*pb.MeasurementEntry) []measurementRow {
	rows := make([]measurementRow, 0, len(measurements))
	for _, measurement := range measurements {
		if measurement == nil {
			continue
		}
		rows = append(rows, measurementRow{
			Type:      measurement.GetType(),
			Value:     fmt.Sprintf("%.3f", measurement.GetValue()),
			Unit:      measurement.GetUnit(),
			Timestamp: formatTimestamp(measurement.GetTimestamp()),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Type == rows[j].Type {
			return rows[i].Timestamp < rows[j].Timestamp
		}
		return rows[i].Type < rows[j].Type
	})
	return rows
}

func formatTimestamp(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return "-"
	}
	return ts.AsTime().Format(time.RFC3339)
}

func blankIfEmpty(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}
