package concurrency

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// LoadConfigFromSettings loads concurrency configuration from PAC settings
func LoadConfigFromSettings(settingsMap map[string]string) (*DriverConfig, error) {
	// Check if concurrency is enabled
	enabled, _ := strconv.ParseBool(settingsMap["concurrency-enabled"])
	if !enabled {
		return nil, fmt.Errorf("concurrency is not enabled")
	}

	// Get driver type
	driver := settingsMap["concurrency-driver"]
	if driver == "" {
		driver = "etcd" // default to etcd
	}

	config := &DriverConfig{
		Driver: driver,
	}

	switch driver {
	case "etcd":
		etcdConfig, err := loadEtcdConfigFromSettings(settingsMap)
		if err != nil {
			return nil, fmt.Errorf("failed to load etcd config: %w", err)
		}
		config.EtcdConfig = etcdConfig

	case "postgresql":
		postgresConfig, err := loadPostgreSQLConfigFromSettings(settingsMap)
		if err != nil {
			return nil, fmt.Errorf("failed to load postgresql config: %w", err)
		}
		config.PostgreSQLConfig = postgresConfig

	case "memory":
		memoryConfig, err := loadMemoryConfigFromSettings(settingsMap)
		if err != nil {
			return nil, fmt.Errorf("failed to load memory config: %w", err)
		}
		config.MemoryConfig = memoryConfig

	default:
		return nil, fmt.Errorf("unsupported concurrency driver: %s", driver)
	}

	return config, nil
}

// loadEtcdConfigFromSettings loads etcd configuration from settings
func loadEtcdConfigFromSettings(settingsMap map[string]string) (*EtcdConfig, error) {
	endpointsStr := settingsMap["etcd-endpoints"]
	if endpointsStr == "" {
		endpointsStr = "localhost:2379"
	}
	endpoints := strings.Split(endpointsStr, ",")

	dialTimeout := 5 * time.Second
	if timeoutStr := settingsMap["etcd-dial-timeout"]; timeoutStr != "" {
		if timeout, err := time.ParseDuration(timeoutStr); err == nil {
			dialTimeout = timeout
		}
	}

	username := settingsMap["etcd-username"]
	password := settingsMap["etcd-password"]
	mode := settingsMap["etcd-mode"]
	if mode == "" {
		mode = "etcd"
	}

	var tlsConfig *TLSConfig
	if certFile := settingsMap["etcd-cert-file"]; certFile != "" {
		tlsConfig = &TLSConfig{
			CertFile:   certFile,
			KeyFile:    settingsMap["etcd-key-file"],
			CAFile:     settingsMap["etcd-ca-file"],
			ServerName: settingsMap["etcd-server-name"],
		}
	}

	return &EtcdConfig{
		Endpoints:   endpoints,
		DialTimeout: dialTimeout,
		Username:    username,
		Password:    password,
		TLSConfig:   tlsConfig,
		Mode:        mode,
	}, nil
}

// loadPostgreSQLConfigFromSettings loads PostgreSQL configuration from settings
func loadPostgreSQLConfigFromSettings(settingsMap map[string]string) (*PostgreSQLConfig, error) {
	host := settingsMap["postgresql-host"]
	if host == "" {
		host = "localhost"
	}

	port := 5432
	if portStr := settingsMap["postgresql-port"]; portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	database := settingsMap["postgresql-database"]
	if database == "" {
		database = "pac_concurrency"
	}

	username := settingsMap["postgresql-username"]
	if username == "" {
		return nil, fmt.Errorf("postgresql-username is required")
	}

	password := settingsMap["postgresql-password"]
	if password == "" {
		// Try to get password from secret reference
		passwordFromSecret := settingsMap["postgresql-password-from-secret"]
		if passwordFromSecret == "" {
			return nil, fmt.Errorf("postgresql-password is required (either directly or via postgresql-password-from-secret)")
		}
		// The actual password loading from secret should be handled by the caller
		// This is just a placeholder - the real implementation would need to
		// read from the Kubernetes secret
		return nil, fmt.Errorf("postgresql-password-from-secret is not yet implemented - use postgresql-password directly")
	}

	sslMode := settingsMap["postgresql-ssl-mode"]
	if sslMode == "" {
		sslMode = "disable"
	}

	maxConnections := 10
	if maxConnStr := settingsMap["postgresql-max-connections"]; maxConnStr != "" {
		if maxConn, err := strconv.Atoi(maxConnStr); err == nil {
			maxConnections = maxConn
		}
	}

	connectionTimeout := 30 * time.Second
	if timeoutStr := settingsMap["postgresql-connection-timeout"]; timeoutStr != "" {
		if timeout, err := time.ParseDuration(timeoutStr); err == nil {
			connectionTimeout = timeout
		}
	}

	leaseTTL := 1 * time.Hour
	if ttlStr := settingsMap["postgresql-lease-ttl"]; ttlStr != "" {
		if ttl, err := time.ParseDuration(ttlStr); err == nil {
			leaseTTL = ttl
		}
	}

	return &PostgreSQLConfig{
		Host:              host,
		Port:              port,
		Database:          database,
		Username:          username,
		Password:          password,
		SSLMode:           sslMode,
		MaxConnections:    maxConnections,
		ConnectionTimeout: connectionTimeout,
		LeaseTTL:          leaseTTL,
	}, nil
}

// loadMemoryConfigFromSettings loads memory configuration from settings
func loadMemoryConfigFromSettings(settingsMap map[string]string) (*MemoryConfig, error) {
	leaseTTL := 30 * time.Minute
	if ttlStr := settingsMap["memory-lease-ttl"]; ttlStr != "" {
		if ttl, err := time.ParseDuration(ttlStr); err == nil {
			leaseTTL = ttl
		}
	}

	return &MemoryConfig{
		LeaseTTL: leaseTTL,
	}, nil
}

// CreateManagerFromSettings creates a concurrency manager from PAC settings
func CreateManagerFromSettings(settingsMap map[string]string, logger *zap.SugaredLogger) (*Manager, error) {
	config, err := LoadConfigFromSettings(settingsMap)
	if err != nil {
		return nil, err
	}

	return NewManager(config, logger)
}

// GetDefaultSettings returns the default concurrency settings
func GetDefaultSettings() map[string]string {
	return map[string]string{
		"concurrency-enabled": "false",
		"concurrency-driver":  "etcd",
		"etcd-endpoints":      "localhost:2379",
		"etcd-dial-timeout":   "5s",
		"etcd-mode":           "etcd",
	}
}
