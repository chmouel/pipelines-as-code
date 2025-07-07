package concurrency

import (
	"testing"

	"go.uber.org/zap"
)

func TestLoadConfigFromSettings(t *testing.T) {
	tests := []struct {
		name        string
		settings    map[string]string
		expectError bool
	}{
		{
			name: "disabled etcd",
			settings: map[string]string{
				"etcd-enabled": "false",
			},
			expectError: false, // Disabled etcd should not cause an error
		},
		{
			name: "enabled etcd with endpoints",
			settings: map[string]string{
				"etcd-enabled":   "true",
				"etcd-endpoints": "localhost:2379",
			},
			expectError: false,
		},
		{
			name: "enabled etcd with explicit etcd mode but no endpoints",
			settings: map[string]string{
				"etcd-enabled":   "true",
				"etcd-mode":      "etcd", // Explicitly set mode to etcd
				"etcd-endpoints": "",     // Explicitly set empty endpoints to override default
			},
			expectError: true, // Should error because no endpoints provided
		},
		{
			name: "enabled etcd with invalid TLS config",
			settings: map[string]string{
				"etcd-enabled":   "true",
				"etcd-mode":      "etcd",
				"etcd-endpoints": "localhost:2379",
				"etcd-cert-file": "cert.pem",
				// Missing key file should cause validation error
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := LoadConfigFromSettings(tt.settings)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
					// Debug output
					t.Logf("Config: Enabled=%v, Mode=%s, Endpoints=%v", config.Enabled, config.Mode, config.Endpoints)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if config == nil {
				t.Errorf("expected config but got nil")
			}
		})
	}
}

func TestCreateManagerFromSettings(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	// Test with memory driver (no external dependencies)
	settings := map[string]string{
		"etcd-enabled": "false", // This will default to memory driver
	}

	manager, err := CreateManagerFromSettings(settings, sugar)
	if err != nil {
		t.Errorf("failed to create manager: %v", err)
		return
	}
	defer manager.Close()

	if manager == nil {
		t.Errorf("expected manager but got nil")
	}

	if manager.GetDriverType() != "memory" {
		t.Errorf("expected driver type 'memory', got '%s'", manager.GetDriverType())
	}
}

func TestGetDefaultSettings(t *testing.T) {
	settings := GetDefaultSettings()

	expectedKeys := []string{
		"etcd-enabled",
		"etcd-mode",
		"etcd-endpoints",
		"etcd-dial-timeout",
	}

	for _, key := range expectedKeys {
		if _, exists := settings[key]; !exists {
			t.Errorf("expected key '%s' in default settings", key)
		}
	}

	if settings["etcd-enabled"] != "false" {
		t.Errorf("expected etcd-enabled to be 'false', got '%s'", settings["etcd-enabled"])
	}
}
