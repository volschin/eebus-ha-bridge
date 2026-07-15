package config

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

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
	// TrustSKI, when set, makes the bridge trust (RegisterRemoteSKI) this remote
	// SKI at startup instead of waiting for Home Assistant to send it via gRPC.
	// Lets a spike container complete the SHIP handshake with a known device
	// (e.g. the VR940) in isolation for hardware testing. Spike-only.
	TrustSKI string `yaml:"trust_ski"`
}

type GRPCConfig struct {
	Port             int                `yaml:"port"`
	Bind             string             `yaml:"bind"`
	EnableReflection bool               `yaml:"enable_reflection"`
	Security         GRPCSecurityConfig `yaml:"security"`
}

type GRPCSecurityMode string

const (
	GRPCSecurityModeLoopback GRPCSecurityMode = "loopback"
	GRPCSecurityModeTLSToken GRPCSecurityMode = "tls_token"
)

// GRPCSecurityConfig controls transport security for the bridge API. The
// certificate, private key, and bearer token are deliberately referenced by
// path so secret material never becomes part of the parsed configuration.
type GRPCSecurityConfig struct {
	Mode        GRPCSecurityMode `yaml:"mode"`
	TLSCertFile string           `yaml:"tls_cert_file"`
	TLSKeyFile  string           `yaml:"tls_key_file"`
	TokenFile   string           `yaml:"token_file"`
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
	if err := validateGRPCSecurity(cfg.GRPC); err != nil {
		return nil, fmt.Errorf("validating gRPC security: %w", err)
	}

	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.GRPC.Port == 0 {
		cfg.GRPC.Port = 50051
	}
	if cfg.GRPC.Bind == "" {
		cfg.GRPC.Bind = "127.0.0.1"
	}
	if cfg.GRPC.Security.Mode == "" {
		cfg.GRPC.Security.Mode = GRPCSecurityModeLoopback
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
	if v := os.Getenv("EEBUS_GRPC_SECURITY_MODE"); v != "" {
		cfg.GRPC.Security.Mode = GRPCSecurityMode(v)
	}
	if v := os.Getenv("EEBUS_GRPC_TLS_CERT_FILE"); v != "" {
		cfg.GRPC.Security.TLSCertFile = v
	}
	if v := os.Getenv("EEBUS_GRPC_TLS_KEY_FILE"); v != "" {
		cfg.GRPC.Security.TLSKeyFile = v
	}
	if v := os.Getenv("EEBUS_GRPC_TOKEN_FILE"); v != "" {
		cfg.GRPC.Security.TokenFile = v
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
	if v := os.Getenv("EEBUS_OHPCF_ENABLED"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			cfg.OHPCF.Enabled = &enabled
		}
	}
	if v := os.Getenv("EEBUS_EXP_TRUST_SKI"); v != "" {
		cfg.Experimental.TrustSKI = v
	}
}

func validateGRPCSecurity(cfg GRPCConfig) error {
	switch cfg.Security.Mode {
	case GRPCSecurityModeLoopback:
		if !isLoopbackBind(cfg.Bind) {
			return fmt.Errorf("security mode %q requires a loopback bind address; bind %q requires tls_token", cfg.Security.Mode, cfg.Bind)
		}
		return nil
	case GRPCSecurityModeTLSToken:
		if cfg.Security.TLSCertFile == "" || cfg.Security.TLSKeyFile == "" || cfg.Security.TokenFile == "" {
			return fmt.Errorf("security mode %q requires tls_cert_file, tls_key_file, and token_file", cfg.Security.Mode)
		}
		if _, err := tls.LoadX509KeyPair(cfg.Security.TLSCertFile, cfg.Security.TLSKeyFile); err != nil {
			return fmt.Errorf("loading gRPC TLS certificate/key: %w", err)
		}
		token, err := os.ReadFile(cfg.Security.TokenFile) // #nosec G304 -- operator-supplied configuration path
		if err != nil {
			return fmt.Errorf("reading gRPC token file %q: %w", cfg.Security.TokenFile, err)
		}
		if strings.TrimSpace(string(token)) == "" {
			return fmt.Errorf("gRPC token file %q is empty", cfg.Security.TokenFile)
		}
		return nil
	default:
		return fmt.Errorf("unknown security mode %q (expected loopback or tls_token)", cfg.Security.Mode)
	}
}

func isLoopbackBind(bind string) bool {
	if strings.EqualFold(bind, "localhost") {
		return true
	}
	ip := net.ParseIP(bind)
	return ip != nil && ip.IsLoopback()
}
