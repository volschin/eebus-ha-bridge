package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	GRPC         GRPCConfig         `yaml:"grpc"`
	EEBUS        EEBUSConfig        `yaml:"eebus"`
	Certificates CertificatesConfig `yaml:"certificates"`
	Logging      LoggingConfig      `yaml:"logging"`
	OHPCF        OHPCFConfig        `yaml:"ohpcf"`
	Experimental ExperimentalConfig `yaml:"experimental"`
}

// OHPCFConfig controls the Optimization of Self-Consumption by Heat Pump
// Compressor Flexibility (OHPCF, a.k.a. OSCF) CEM client, which subscribes to a
// remote heat pump's Compressor / SmartEnergyManagementPs feature (e.g. Vaillant
// VR940) and drives schedule/pause/resume/abort via OHPCFService.
type OHPCFConfig struct {
	// Enabled turns the OHPCF client on or off. On by default; set to false to
	// disable.
	Enabled *bool `yaml:"enabled"`
}

// ExperimentalConfig gates spike / not-yet-stable features. Everything here is
// off by default and may change or be removed without notice.
type ExperimentalConfig struct {
	// MGCPProvider exposes a local GridConnectionPointOfPremises entity that
	// advertises the Monitoring of Grid Connection Point (MGCP) use case and
	// serves grid power/energy measurements, so a heat pump (e.g. Vaillant VR940,
	// which advertises the MGCP MonitoringAppliance role) can read the grid /
	// PV-surplus situation. SPIKE: see docs/eebus-vaillant-improvements.md.
	MGCPProvider bool `yaml:"mgcp_provider"`
	// VAPDProvider exposes a local PVSystem entity that advertises the
	// Visualization of Aggregated Photovoltaic Data (VAPD) use case and serves PV
	// power/yield/peak measurements, so a device (e.g. Vaillant VR940, which
	// advertises the VAPD VisualizationAppliance role) can display the home's PV
	// data. SPIKE: see docs/eebus-vaillant-improvements.md.
	VAPDProvider bool `yaml:"vapd_provider"`
	// VABDProvider exposes a local ElectricityStorageSystem entity that advertises
	// the Visualization of Aggregated Battery Data (VABD) use case and serves
	// battery power/energy/state-of-charge measurements, so a device (e.g. Vaillant
	// VR940, which advertises the VABD VisualizationAppliance role) can display the
	// home's battery state. SPIKE: see docs/eebus-vaillant-improvements.md.
	VABDProvider bool `yaml:"vabd_provider"`
	// HvacProbe enables the read-only HVAC/DHW diagnostic probe: on device
	// connect it requests all Setpoint/HVAC data from remote DHWCircuit/HVACRoom
	// entities and logs values plus advertised read/write operations, to map
	// which setpoints/modes a gateway (e.g. Vaillant VR940) exposes for control.
	// Stage 1 of the HVAC control spike; sends SPINE reads only, never writes.
	HvacProbe bool `yaml:"hvac_probe"`
	// HvacProbeBind is stage 2 of the HVAC control spike: in addition to the
	// reads, request a SPINE binding to each remote Setpoint/HVAC server
	// feature (the precondition for writes) and log whether the device accepts
	// it. Requires HvacProbe. Still performs no writes.
	HvacProbeBind bool `yaml:"hvac_probe_bind"`
	// HvacProbeWrite is stage 3 of the HVAC control spike: after a Setpoint
	// binding was accepted, write the device's own current SetpointListData
	// back unchanged (echo write) and log whether the device accepts the write
	// command. Values are not modified, so no temperature changes. Requires
	// HvacProbe and HvacProbeBind.
	HvacProbeWrite bool `yaml:"hvac_probe_write"`
	// TrustSKI, when set, makes the bridge trust (RegisterRemoteSKI) this remote
	// SKI at startup instead of waiting for Home Assistant to send it via gRPC.
	// Lets a spike container complete the SHIP handshake with a known device
	// (e.g. the VR940) in isolation for hardware testing. Spike-only.
	TrustSKI string `yaml:"trust_ski"`
}

type GRPCConfig struct {
	Port             int    `yaml:"port"`
	Bind             string `yaml:"bind"`
	EnableReflection bool   `yaml:"enable_reflection"`
}

type EEBUSConfig struct {
	Port   int    `yaml:"port"`
	Vendor string `yaml:"vendor"`
	Brand  string `yaml:"brand"`
	Model  string `yaml:"model"`
	Serial string `yaml:"serial"`
}

type CertificatesConfig struct {
	AutoGenerate bool   `yaml:"auto_generate"`
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
	StoragePath  string `yaml:"storage_path"`
}

type LoggingConfig struct {
	DebugEvents bool `yaml:"debug_events"`
	// ShipLog forwards ship-go/eebus-go internal Debug/Info/Error logs (SHIP
	// handshake errors and abort reasons) to the bridge logger.
	ShipLog bool `yaml:"ship_log"`
	// ShipTrace additionally enables raw per-message Trace logs (very verbose).
	ShipTrace bool `yaml:"ship_trace"`
}

func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is an operator-supplied config file location, not user input
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	applyDefaults(cfg)
	applyEnvOverrides(cfg)

	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.GRPC.Port == 0 {
		cfg.GRPC.Port = 50051
	}
	if cfg.GRPC.Bind == "" {
		cfg.GRPC.Bind = "127.0.0.1"
	}
	if cfg.EEBUS.Port == 0 {
		cfg.EEBUS.Port = 4712
	}
	if cfg.EEBUS.Vendor == "" {
		cfg.EEBUS.Vendor = "HomeAssistant"
	}
	if cfg.EEBUS.Brand == "" {
		cfg.EEBUS.Brand = "Home Assistant"
	}
	if cfg.EEBUS.Model == "" {
		cfg.EEBUS.Model = "eebus-bridge"
	}
	if cfg.Certificates.StoragePath == "" {
		cfg.Certificates.StoragePath = "/data/certs"
	}
	if !cfg.Certificates.AutoGenerate && cfg.Certificates.CertFile == "" {
		cfg.Certificates.AutoGenerate = true
	}
	if cfg.OHPCF.Enabled == nil {
		enabled := true
		cfg.OHPCF.Enabled = &enabled
	}
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("EEBUS_GRPC_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.GRPC.Port = port
		}
	}
	if v := os.Getenv("EEBUS_GRPC_BIND"); v != "" {
		cfg.GRPC.Bind = v
	}
	if v := os.Getenv("EEBUS_GRPC_REFLECTION"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.GRPC.EnableReflection = enabled
		}
	}
	if v := os.Getenv("EEBUS_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.EEBUS.Port = port
		}
	}
	if v := os.Getenv("EEBUS_VENDOR"); v != "" {
		cfg.EEBUS.Vendor = v
	}
	if v := os.Getenv("EEBUS_BRAND"); v != "" {
		cfg.EEBUS.Brand = v
	}
	if v := os.Getenv("EEBUS_MODEL"); v != "" {
		cfg.EEBUS.Model = v
	}
	if v := os.Getenv("EEBUS_SERIAL"); v != "" {
		cfg.EEBUS.Serial = v
	}
	if v := os.Getenv("EEBUS_CERT_FILE"); v != "" {
		cfg.Certificates.CertFile = v
	}
	if v := os.Getenv("EEBUS_KEY_FILE"); v != "" {
		cfg.Certificates.KeyFile = v
	}
	if v := os.Getenv("EEBUS_CERT_STORAGE"); v != "" {
		cfg.Certificates.StoragePath = v
	}
	if v := os.Getenv("EEBUS_DEBUG_EVENTS"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Logging.DebugEvents = enabled
		}
	}
	if v := os.Getenv("EEBUS_SHIP_LOG"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Logging.ShipLog = enabled
		}
	}
	if v := os.Getenv("EEBUS_SHIP_TRACE"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Logging.ShipTrace = enabled
		}
	}
	if v := os.Getenv("EEBUS_EXP_MGCP_PROVIDER"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Experimental.MGCPProvider = enabled
		}
	}
	if v := os.Getenv("EEBUS_EXP_VAPD_PROVIDER"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Experimental.VAPDProvider = enabled
		}
	}
	if v := os.Getenv("EEBUS_EXP_VABD_PROVIDER"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Experimental.VABDProvider = enabled
		}
	}
	if v := os.Getenv("EEBUS_EXP_HVAC_PROBE"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Experimental.HvacProbe = enabled
		}
	}
	if v := os.Getenv("EEBUS_EXP_HVAC_PROBE_BIND"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Experimental.HvacProbeBind = enabled
		}
	}
	if v := os.Getenv("EEBUS_EXP_HVAC_PROBE_WRITE"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.Experimental.HvacProbeWrite = enabled
		}
	}
	if v := os.Getenv("EEBUS_OHPCF_ENABLED"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.OHPCF.Enabled = &enabled
		}
	}
	if v := os.Getenv("EEBUS_EXP_TRUST_SKI"); v != "" {
		cfg.Experimental.TrustSKI = v
	}
}
