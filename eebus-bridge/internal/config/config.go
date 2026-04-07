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
}

type GRPCConfig struct {
	Port int `yaml:"port"`
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

func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
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
	if cfg.EEBUS.Port == 0 {
		cfg.EEBUS.Port = 4712
	}
	if cfg.EEBUS.Vendor == "" {
		cfg.EEBUS.Vendor = "HomeAssistant"
	}
	if cfg.EEBUS.Brand == "" {
		cfg.EEBUS.Brand = "eebus-bridge"
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
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("EEBUS_GRPC_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.GRPC.Port = port
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
}
