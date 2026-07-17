package config

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	AutoGenerate *bool  `yaml:"auto_generate"`
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

	cfg := defaultConfig()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	envOverrides, err := applyEnvOverrides(cfg)
	if err != nil {
		return nil, fmt.Errorf("applying environment overrides: %w", err)
	}
	if err := Validate(cfg); err != nil {
		if len(envOverrides) > 0 {
			return nil, fmt.Errorf("validating config after environment overrides (%s): %w", strings.Join(envOverrides, ", "), err)
		}
		return nil, err
	}

	return cfg, nil
}

func defaultConfig() *Config {
	certAutoGenerate := true
	ohpcfEnabled := true
	return &Config{
		GRPC: GRPCConfig{
			Port: 50051,
			Bind: "127.0.0.1",
			Security: GRPCSecurityConfig{
				Mode: GRPCSecurityModeLoopback,
			},
		},
		EEBUS: EEBUSConfig{
			Port:   4712,
			Vendor: "HomeAssistant",
			Brand:  "Home Assistant",
			Model:  "eebus-bridge",
		},
		Certificates: CertificatesConfig{
			AutoGenerate: &certAutoGenerate,
			StoragePath:  "/data/certs",
		},
		OHPCF: OHPCFConfig{
			Enabled: &ohpcfEnabled,
		},
	}
}

func applyEnvOverrides(cfg *Config) ([]string, error) {
	applied := make([]string, 0)
	if v, ok := os.LookupEnv("EEBUS_GRPC_PORT"); ok {
		applied = append(applied, "EEBUS_GRPC_PORT")
		port, err := parseEnvPort("EEBUS_GRPC_PORT", v)
		if err != nil {
			return applied, err
		}
		cfg.GRPC.Port = port
	}
	if v, ok := os.LookupEnv("EEBUS_GRPC_BIND"); ok {
		applied = append(applied, "EEBUS_GRPC_BIND")
		if v == "" {
			return applied, fmt.Errorf("EEBUS_GRPC_BIND must not be empty")
		}
		cfg.GRPC.Bind = v
	}
	if v, ok := os.LookupEnv("EEBUS_GRPC_REFLECTION"); ok {
		applied = append(applied, "EEBUS_GRPC_REFLECTION")
		enabled, err := parseEnvBool("EEBUS_GRPC_REFLECTION", v)
		if err != nil {
			return applied, err
		}
		cfg.GRPC.EnableReflection = enabled
	}
	if v, ok := os.LookupEnv("EEBUS_GRPC_SECURITY_MODE"); ok {
		applied = append(applied, "EEBUS_GRPC_SECURITY_MODE")
		mode := GRPCSecurityMode(v)
		if mode != GRPCSecurityModeLoopback && mode != GRPCSecurityModeTLSToken {
			return applied, fmt.Errorf("EEBUS_GRPC_SECURITY_MODE must be loopback or tls_token, got %q", v)
		}
		cfg.GRPC.Security.Mode = GRPCSecurityMode(v)
	}
	if v, ok := os.LookupEnv("EEBUS_GRPC_TLS_CERT_FILE"); ok {
		applied = append(applied, "EEBUS_GRPC_TLS_CERT_FILE")
		cfg.GRPC.Security.TLSCertFile = v
	}
	if v, ok := os.LookupEnv("EEBUS_GRPC_TLS_KEY_FILE"); ok {
		applied = append(applied, "EEBUS_GRPC_TLS_KEY_FILE")
		cfg.GRPC.Security.TLSKeyFile = v
	}
	if v, ok := os.LookupEnv("EEBUS_GRPC_TOKEN_FILE"); ok {
		applied = append(applied, "EEBUS_GRPC_TOKEN_FILE")
		cfg.GRPC.Security.TokenFile = v
	}
	if v, ok := os.LookupEnv("EEBUS_PORT"); ok {
		applied = append(applied, "EEBUS_PORT")
		port, err := parseEnvPort("EEBUS_PORT", v)
		if err != nil {
			return applied, err
		}
		cfg.EEBUS.Port = port
	}
	if v, ok := os.LookupEnv("EEBUS_VENDOR"); ok {
		applied = append(applied, "EEBUS_VENDOR")
		cfg.EEBUS.Vendor = v
	}
	if v, ok := os.LookupEnv("EEBUS_BRAND"); ok {
		applied = append(applied, "EEBUS_BRAND")
		cfg.EEBUS.Brand = v
	}
	if v, ok := os.LookupEnv("EEBUS_MODEL"); ok {
		applied = append(applied, "EEBUS_MODEL")
		cfg.EEBUS.Model = v
	}
	if v, ok := os.LookupEnv("EEBUS_SERIAL"); ok {
		applied = append(applied, "EEBUS_SERIAL")
		cfg.EEBUS.Serial = v
	}
	if v, ok := os.LookupEnv("EEBUS_CERT_FILE"); ok {
		applied = append(applied, "EEBUS_CERT_FILE")
		cfg.Certificates.CertFile = v
	}
	if v, ok := os.LookupEnv("EEBUS_KEY_FILE"); ok {
		applied = append(applied, "EEBUS_KEY_FILE")
		cfg.Certificates.KeyFile = v
	}
	if v, ok := os.LookupEnv("EEBUS_CERT_STORAGE"); ok {
		applied = append(applied, "EEBUS_CERT_STORAGE")
		cfg.Certificates.StoragePath = v
	}
	if v, ok := os.LookupEnv("EEBUS_DEBUG_EVENTS"); ok {
		applied = append(applied, "EEBUS_DEBUG_EVENTS")
		enabled, err := parseEnvBool("EEBUS_DEBUG_EVENTS", v)
		if err != nil {
			return applied, err
		}
		cfg.Logging.DebugEvents = enabled
	}
	if v, ok := os.LookupEnv("EEBUS_SHIP_LOG"); ok {
		applied = append(applied, "EEBUS_SHIP_LOG")
		enabled, err := parseEnvBool("EEBUS_SHIP_LOG", v)
		if err != nil {
			return applied, err
		}
		cfg.Logging.ShipLog = enabled
	}
	if v, ok := os.LookupEnv("EEBUS_SHIP_TRACE"); ok {
		applied = append(applied, "EEBUS_SHIP_TRACE")
		enabled, err := parseEnvBool("EEBUS_SHIP_TRACE", v)
		if err != nil {
			return applied, err
		}
		cfg.Logging.ShipTrace = enabled
	}
	if v, ok := os.LookupEnv("EEBUS_EXP_MGCP_PROVIDER"); ok {
		applied = append(applied, "EEBUS_EXP_MGCP_PROVIDER")
		enabled, err := parseEnvBool("EEBUS_EXP_MGCP_PROVIDER", v)
		if err != nil {
			return applied, err
		}
		cfg.Experimental.MGCPProvider = enabled
	}
	if v, ok := os.LookupEnv("EEBUS_EXP_VAPD_PROVIDER"); ok {
		applied = append(applied, "EEBUS_EXP_VAPD_PROVIDER")
		enabled, err := parseEnvBool("EEBUS_EXP_VAPD_PROVIDER", v)
		if err != nil {
			return applied, err
		}
		cfg.Experimental.VAPDProvider = enabled
	}
	if v, ok := os.LookupEnv("EEBUS_EXP_VABD_PROVIDER"); ok {
		applied = append(applied, "EEBUS_EXP_VABD_PROVIDER")
		enabled, err := parseEnvBool("EEBUS_EXP_VABD_PROVIDER", v)
		if err != nil {
			return applied, err
		}
		cfg.Experimental.VABDProvider = enabled
	}
	if v, ok := os.LookupEnv("EEBUS_OHPCF_ENABLED"); ok {
		applied = append(applied, "EEBUS_OHPCF_ENABLED")
		enabled, err := parseEnvBool("EEBUS_OHPCF_ENABLED", v)
		if err != nil {
			return applied, err
		}
		cfg.OHPCF.Enabled = &enabled
	}
	if v, ok := os.LookupEnv("EEBUS_EXP_TRUST_SKI"); ok {
		applied = append(applied, "EEBUS_EXP_TRUST_SKI")
		if !isCanonicalSKI(v) {
			return applied, fmt.Errorf("EEBUS_EXP_TRUST_SKI must be a 40-character hexadecimal SKI")
		}
		cfg.Experimental.TrustSKI = v
	}
	return applied, nil
}

func Validate(cfg *Config) error {
	if err := validatePorts(cfg.GRPC.Port, cfg.EEBUS.Port); err != nil {
		return err
	}
	if err := validateCertificates(cfg.Certificates); err != nil {
		return fmt.Errorf("validating certificates: %w", err)
	}
	if err := validateGRPCSecurity(cfg.GRPC); err != nil {
		return fmt.Errorf("validating gRPC security: %w", err)
	}
	if err := validateProviderConfig(cfg.Experimental, cfg.GRPC); err != nil {
		return fmt.Errorf("validating experimental providers: %w", err)
	}
	if err := validateExperimentalConfig(cfg.Experimental); err != nil {
		return fmt.Errorf("validating experimental config: %w", err)
	}
	return nil
}

func parseEnvPort(name string, value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer port: %w", name, err)
	}
	if err := validatePort(name, port); err != nil {
		return 0, err
	}
	return port, nil
}

func parseEnvBool(name string, value string) (bool, error) {
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return enabled, nil
}

func validatePorts(grpcPort int, eebusPort int) error {
	if err := validatePort("grpc.port", grpcPort); err != nil {
		return err
	}
	if err := validatePort("eebus.port", eebusPort); err != nil {
		return err
	}
	return nil
}

func validatePort(name string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s must be in range 1..65535, got %d", name, port)
	}
	return nil
}

func validateCertificates(cfg CertificatesConfig) error {
	autoGenerate := cfg.AutoGenerate != nil && *cfg.AutoGenerate
	hasCert := cfg.CertFile != ""
	hasKey := cfg.KeyFile != ""
	if hasCert != hasKey {
		return fmt.Errorf("cert_file and key_file must be configured together")
	}
	if autoGenerate {
		if hasCert || hasKey {
			return fmt.Errorf("auto_generate cannot be combined with explicit cert_file/key_file")
		}
		if cfg.StoragePath == "" {
			return fmt.Errorf("auto_generate requires storage_path")
		}
		return nil
	}
	if hasCert {
		return nil
	}
	if cfg.StoragePath == "" {
		return fmt.Errorf("auto_generate=false requires cert_file/key_file or storage_path with existing cert.pem/key.pem")
	}
	if !fileExists(filepath.Join(cfg.StoragePath, "cert.pem")) || !fileExists(filepath.Join(cfg.StoragePath, "key.pem")) {
		return fmt.Errorf("auto_generate=false requires existing cert.pem and key.pem in storage_path %q", cfg.StoragePath)
	}
	return nil
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

func validateProviderConfig(experimental ExperimentalConfig, grpc GRPCConfig) error {
	if !experimental.MGCPProvider && !experimental.VAPDProvider && !experimental.VABDProvider {
		return nil
	}
	if grpc.Security.Mode == GRPCSecurityModeLoopback && !isLoopbackBind(grpc.Bind) {
		return fmt.Errorf("experimental provider push services on non-loopback bind require tls_token security")
	}
	return nil
}

func validateExperimentalConfig(cfg ExperimentalConfig) error {
	if cfg.TrustSKI == "" {
		return nil
	}
	if !isCanonicalSKI(cfg.TrustSKI) {
		return fmt.Errorf("trust_ski must be a 40-character hexadecimal SKI")
	}
	return nil
}

func isCanonicalSKI(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

func isLoopbackBind(bind string) bool {
	if strings.EqualFold(bind, "localhost") {
		return true
	}
	ip := net.ParseIP(bind)
	return ip != nil && ip.IsLoopback()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
