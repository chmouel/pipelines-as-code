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
			name: "disabled concurrency",
			settings: map[string]string{
				"concurrency-enabled": "false",
			},
			expectError: true,
		},
		{
			name: "etcd driver",
			settings: map[string]string{
				"concurrency-enabled": "true",
				"concurrency-driver":  "etcd",
				"etcd-endpoints":      "localhost:2379",
			},
			expectError: false,
		},
		{
			name: "postgresql driver",
			settings: map[string]string{
				"concurrency-enabled": "true",
				"concurrency-driver":  "postgresql",
				"postgresql-host":     "localhost",
				"postgresql-username": "test",
				"postgresql-password": "test",
			},
			expectError: false,
		},
		{
			name: "memory driver",
			settings: map[string]string{
				"concurrency-enabled": "true",
				"concurrency-driver":  "memory",
			},
			expectError: false,
		},
		{
			name: "invalid driver",
			settings: map[string]string{
				"concurrency-enabled": "true",
				"concurrency-driver":  "invalid",
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
		"concurrency-enabled": "true",
		"concurrency-driver":  "memory",
		"memory-lease-ttl":    "1m",
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
		"concurrency-enabled",
		"concurrency-driver",
		"etcd-endpoints",
		"etcd-dial-timeout",
		"etcd-mode",
	}

	for _, key := range expectedKeys {
		if _, exists := settings[key]; !exists {
			t.Errorf("expected key '%s' in default settings", key)
		}
	}

	if settings["concurrency-enabled"] != "false" {
		t.Errorf("expected concurrency-enabled to be 'false', got '%s'", settings["concurrency-enabled"])
	}
}
