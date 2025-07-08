package concurrency

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/concurrency/test"
	"go.uber.org/zap"
)

// DefaultConfig returns a default etcd configuration.
func DefaultConfig() *Config {
	return &Config{
		Endpoints:   []string{"localhost:2379"},
		DialTimeout: 5 * time.Second,
		Enabled:     false,
		Mode:        "memory",
	}
}

// ConfigFromSettings creates etcd config from PipelinesAsCode settings.
func ConfigFromSettings(settings map[string]string) *Config {
	cfg := DefaultConfig()

	// Check if etcd is enabled
	if enabled := settings["etcd-enabled"]; enabled == "true" || enabled == "1" {
		cfg.Enabled = true
	}

	// Get etcd mode (memory, mock, etcd)
	if mode := settings["etcd-mode"]; mode != "" {
		cfg.Mode = mode
	}

	// ETCD_ENDPOINTS: comma-separated list of etcd endpoints
	if endpoints, exists := settings["etcd-endpoints"]; exists {
		if endpoints == "" {
			cfg.Endpoints = []string{} // Empty endpoints list
		} else {
			cfg.Endpoints = strings.Split(endpoints, ",")
		}
	}

	// ETCD_DIAL_TIMEOUT: dial timeout in seconds
	if timeoutStr := settings["etcd-dial-timeout"]; timeoutStr != "" {
		if timeout, err := strconv.Atoi(timeoutStr); err == nil {
			cfg.DialTimeout = time.Duration(timeout) * time.Second
		}
	}

	// ETCD_USERNAME: username for authentication
	if username := settings["etcd-username"]; username != "" {
		cfg.Username = username
	}

	// ETCD_PASSWORD: password for authentication
	if password := settings["etcd-password"]; password != "" {
		cfg.Password = password
	}

	// TLS configuration
	if certFile := settings["etcd-cert-file"]; certFile != "" {
		if cfg.TLSConfig == nil {
			cfg.TLSConfig = &TLSConfig{}
		}
		cfg.TLSConfig.CertFile = certFile
	}

	if keyFile := settings["etcd-key-file"]; keyFile != "" {
		if cfg.TLSConfig == nil {
			cfg.TLSConfig = &TLSConfig{}
		}
		cfg.TLSConfig.KeyFile = keyFile
	}

	if caFile := settings["etcd-ca-file"]; caFile != "" {
		if cfg.TLSConfig == nil {
			cfg.TLSConfig = &TLSConfig{}
		}
		cfg.TLSConfig.CAFile = caFile
	}

	if serverName := settings["etcd-server-name"]; serverName != "" {
		if cfg.TLSConfig == nil {
			cfg.TLSConfig = &TLSConfig{}
		}
		cfg.TLSConfig.ServerName = serverName
	}

	return cfg
}

// ValidateConfig validates the etcd configuration.
func ValidateConfig(cfg *Config) error {
	if !cfg.Enabled {
		return nil // No validation needed if etcd is disabled
	}

	if cfg.Mode == "etcd" {
		if len(cfg.Endpoints) == 0 {
			return fmt.Errorf("at least one etcd endpoint must be specified")
		}

		if cfg.DialTimeout <= 0 {
			return fmt.Errorf("dial timeout must be positive")
		}

		if cfg.TLSConfig != nil {
			if cfg.TLSConfig.CertFile != "" && cfg.TLSConfig.KeyFile == "" {
				return fmt.Errorf("key file must be specified when cert file is provided")
			}
			if cfg.TLSConfig.KeyFile != "" && cfg.TLSConfig.CertFile == "" {
				return fmt.Errorf("cert file must be specified when key file is provided")
			}
		}
	}

	return nil
}

// NewClientFromSettings creates a new etcd client using PipelinesAsCode settings.
func NewClientFromSettings(settings map[string]string, logger *zap.SugaredLogger) (Client, error) {
	cfg := ConfigFromSettings(settings)

	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid etcd configuration: %w", err)
	}

	if !cfg.Enabled {
		logger.Info("etcd is disabled, using mock client")
		return test.NewMockClient(logger), nil
	}

	logger.Infof("connecting to etcd at %v (mode: %s)", cfg.Endpoints, cfg.Mode)

	switch cfg.Mode {
	case "etcd":
		return NewClient(cfg, logger)
	case "memory", "mock":
		logger.Info("using mock etcd client for testing/development")
		return test.NewMockClient(logger), nil
	default:
		return nil, fmt.Errorf("unknown etcd mode: %s", cfg.Mode)
	}
}

// IsEtcdEnabled checks if etcd is enabled via settings.
func IsEtcdEnabled(settings map[string]string) bool {
	enabled := settings["etcd-enabled"]
	return enabled == "true" || enabled == "1"
}

// GetEtcdMode returns the etcd mode from settings.
// Options: "memory" (for testing), "etcd" (for production).
func GetEtcdMode(settings map[string]string) string {
	mode := settings["etcd-mode"]
	if mode == "" {
		if IsEtcdEnabled(settings) {
			return "etcd"
		}
		return "memory"
	}
	return mode
}

// NewClientByMode creates an etcd client based on the configured mode.
func NewClientByMode(settings map[string]string, logger *zap.SugaredLogger) (Client, error) {
	return NewClientFromSettings(settings, logger)
}

// LoadConfigFromSettings loads etcd configuration from PipelinesAsCode settings.
func LoadConfigFromSettings(settings map[string]string) (*Config, error) {
	cfg := ConfigFromSettings(settings)

	// Check if etcd is enabled
	if !IsEtcdEnabled(settings) {
		cfg.Enabled = false
		return cfg, nil
	}

	cfg.Enabled = true

	// Validate the configuration
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid etcd configuration: %w", err)
	}

	return cfg, nil
}

// LoadPostgreSQLConfigFromSettings loads PostgreSQL configuration from PipelinesAsCode settings.
func LoadPostgreSQLConfigFromSettings(settings map[string]string) (*PostgreSQLConfig, error) {
	config := &PostgreSQLConfig{
		Host:              "localhost",
		Port:              5432,
		Database:          "pac_concurrency",
		Username:          "pac_user",
		SSLMode:           "disable",
		MaxConnections:    10,
		ConnectionTimeout: 30 * time.Second,
		LeaseTTL:          1 * time.Hour,
	}

	// PostgreSQL host
	if host := settings["postgresql-host"]; host != "" {
		config.Host = host
	}

	// PostgreSQL port
	if portStr := settings["postgresql-port"]; portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			config.Port = port
		}
	}

	// PostgreSQL database
	if database := settings["postgresql-database"]; database != "" {
		config.Database = database
	}

	// PostgreSQL username
	if username := settings["postgresql-username"]; username != "" {
		config.Username = username
	}

	// PostgreSQL password
	if password := settings["postgresql-password"]; password != "" {
		config.Password = password
	}

	// PostgreSQL SSL mode
	if sslMode := settings["postgresql-ssl-mode"]; sslMode != "" {
		config.SSLMode = sslMode
	}

	// PostgreSQL max connections
	if maxConnStr := settings["postgresql-max-connections"]; maxConnStr != "" {
		if maxConn, err := strconv.Atoi(maxConnStr); err == nil {
			config.MaxConnections = maxConn
		}
	}

	// PostgreSQL connection timeout
	if timeoutStr := settings["postgresql-connection-timeout"]; timeoutStr != "" {
		if timeout, err := time.ParseDuration(timeoutStr); err == nil {
			config.ConnectionTimeout = timeout
		}
	}

	// PostgreSQL lease TTL
	if ttlStr := settings["postgresql-lease-ttl"]; ttlStr != "" {
		if ttl, err := time.ParseDuration(ttlStr); err == nil {
			config.LeaseTTL = ttl
		}
	}

	return config, nil
}

// LoadMemoryConfigFromSettings loads memory configuration from PipelinesAsCode settings.
func LoadMemoryConfigFromSettings(settings map[string]string) (*MemoryConfig, error) {
	config := &MemoryConfig{
		LeaseTTL: 30 * time.Minute,
	}

	// Memory lease TTL
	if ttlStr := settings["memory-lease-ttl"]; ttlStr != "" {
		if ttl, err := time.ParseDuration(ttlStr); err == nil {
			config.LeaseTTL = ttl
		}
	}

	return config, nil
}

// CreateManagerFromSettings creates a concurrency manager from PipelinesAsCode settings.
func CreateManagerFromSettings(settings map[string]string, logger *zap.SugaredLogger) (*Manager, error) {
	// Load configuration from settings
	config := &DriverConfig{}

	// Determine which driver to use
	driver := settings["concurrency-driver"]
	if driver == "" {
		// Fallback to etcd-enabled check for backward compatibility
		if IsEtcdEnabled(settings) {
			driver = "etcd"
		} else {
			driver = "memory"
		}
	}

	switch driver {
	case "etcd":
		config.Driver = "etcd"
		etcdConfig, err := LoadConfigFromSettings(settings)
		if err != nil {
			return nil, fmt.Errorf("failed to load etcd config: %w", err)
		}
		config.EtcdConfig = &EtcdConfig{
			Endpoints:   etcdConfig.Endpoints,
			DialTimeout: etcdConfig.DialTimeout,
			Username:    etcdConfig.Username,
			Password:    etcdConfig.Password,
			TLSConfig:   etcdConfig.TLSConfig,
			Mode:        etcdConfig.Mode,
		}
	case "postgresql":
		config.Driver = "postgresql"
		postgresqlConfig, err := LoadPostgreSQLConfigFromSettings(settings)
		if err != nil {
			return nil, fmt.Errorf("failed to load postgresql config: %w", err)
		}
		config.PostgreSQLConfig = postgresqlConfig
	case "memory":
		config.Driver = "memory"
		memoryConfig, err := LoadMemoryConfigFromSettings(settings)
		if err != nil {
			return nil, fmt.Errorf("failed to load memory config: %w", err)
		}
		config.MemoryConfig = memoryConfig
	default:
		return nil, fmt.Errorf("unsupported concurrency driver: %s", driver)
	}

	return NewManager(config, logger)
}

// GetDefaultSettings returns the default concurrency settings.
func GetDefaultSettings() map[string]string {
	return map[string]string{
		"concurrency-driver":            "memory",
		"etcd-enabled":                  "false",
		"etcd-mode":                     "memory",
		"etcd-endpoints":                "localhost:2379",
		"etcd-dial-timeout":             "5",
		"postgresql-host":               "localhost",
		"postgresql-port":               "5432",
		"postgresql-database":           "pac_concurrency",
		"postgresql-username":           "pac_user",
		"postgresql-ssl-mode":           "disable",
		"postgresql-max-connections":    "10",
		"postgresql-connection-timeout": "30s",
		"postgresql-lease-ttl":          "1h",
		"memory-lease-ttl":              "30m",
	}
}
