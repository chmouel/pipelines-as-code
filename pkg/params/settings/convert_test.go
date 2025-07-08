package settings

import (
	"testing"

	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
)

// getDefaultExpectedConfig returns the default expected configuration map.
func getDefaultExpectedConfig() map[string]string {
	return map[string]string{
		"application-name":                           "Pipelines as Code CI",
		"auto-configure-new-github-repo":             "false",
		"auto-configure-repo-namespace-template":     "",
		"bitbucket-cloud-additional-source-ip":       "",
		"bitbucket-cloud-check-source-ip":            "true",
		"custom-console-name":                        "",
		"custom-console-url":                         "",
		"custom-console-url-namespace":               "",
		"custom-console-url-pr-details":              "",
		"custom-console-url-pr-tasklog":              "",
		"default-max-keep-runs":                      "0",
		"error-detection-from-container-logs":        "true",
		"error-detection-max-number-of-lines":        "50",
		"error-detection-simple-regexp":              "^(?P<filename>[^:]*):(?P<line>[0-9]+):(?P<column>[0-9]+)?([ ]*)?(?P<error>.*)",
		"error-log-snippet":                          "true",
		"enable-cancel-in-progress-on-pull-requests": "false",
		"enable-cancel-in-progress-on-push":          "false",
		"skip-push-event-for-pr-commits":             "true",
		"hub-catalog-name":                           "tekton",
		"hub-url":                                    "https://api.hub.tekton.dev/v1",
		"max-keep-run-upper-limit":                   "0",
		"remember-ok-to-test":                        "false",
		"remote-tasks":                               "true",
		"secret-auto-create":                         "true",
		"secret-github-app-scope-extra-repos":        "",
		"secret-github-app-token-scoped":             "true",
		"tekton-dashboard-url":                       "",
		"etcd-enabled":                               "false",
		"etcd-mode":                                  "",
		"etcd-endpoints":                             "",
		"etcd-dial-timeout":                          "0",
		"etcd-username":                              "",
		"etcd-password":                              "",
		"etcd-cert-file":                             "",
		"etcd-key-file":                              "",
		"etcd-ca-file":                               "",
		"etcd-server-name":                           "",
		"concurrency-enabled":                        "false",
		"concurrency-driver":                         "",
		"postgresql-host":                            "",
		"postgresql-port":                            "0",
		"postgresql-database":                        "",
		"postgresql-username":                        "",
		"postgresql-password":                        "",
		"postgresql-ssl-mode":                        "",
		"postgresql-max-connections":                 "0",
		"postgresql-connection-timeout":              "",
		"postgresql-lease-ttl":                       "",
		"memory-lease-ttl":                           "",
	}
}

func TestConvert(t *testing.T) {
	tests := []struct {
		name           string
		inputConfig    map[string]string
		expectedConfig map[string]string
	}{
		{
			name:        "empty_configmap",
			inputConfig: map[string]string{},
			expectedConfig: func() map[string]string {
				config := getDefaultExpectedConfig()
				// Override specific values for empty config test
				config["error-detection-max-number-of-lines"] = "50"
				return config
			}(),
		},
		{
			name: "with few fields",
			inputConfig: map[string]string{
				"application-name":                       "Pipelines as Code CI test name",
				"auto-configure-new-github-repo":         "false",
				"auto-configure-repo-namespace-template": "",
				"bitbucket-cloud-additional-source-ip":   "",
				"error-detection-from-container-logs":    "true",
				"error-detection-max-number-of-lines":    "100",
				"remote-tasks":                           "",
			},
			expectedConfig: func() map[string]string {
				config := getDefaultExpectedConfig()
				// Override specific values for this test case
				config["application-name"] = "Pipelines as Code CI test name"
				config["auto-configure-new-github-repo"] = "false"
				config["auto-configure-repo-namespace-template"] = ""
				config["bitbucket-cloud-additional-source-ip"] = ""
				config["error-detection-from-container-logs"] = "true"
				config["error-detection-max-number-of-lines"] = "100"
				config["remote-tasks"] = "true"
				return config
			}(),
		},
		{
			name: "with few fields and default catalog",
			inputConfig: map[string]string{
				"application-name":                       "Pipelines as Code CI test name",
				"auto-configure-new-github-repo":         "false",
				"auto-configure-repo-namespace-template": "",
				"bitbucket-cloud-additional-source-ip":   "",
				"error-detection-from-container-logs":    "true",
				"error-detection-max-number-of-lines":    "100",
				"hub-catalog-name":                       "test tekton",
				"hub-url":                                "https://api.hub.tekton.dev/v2",
			},
			expectedConfig: func() map[string]string {
				config := getDefaultExpectedConfig()
				// Override specific values for this test case
				config["application-name"] = "Pipelines as Code CI test name"
				config["auto-configure-new-github-repo"] = "false"
				config["auto-configure-repo-namespace-template"] = ""
				config["bitbucket-cloud-additional-source-ip"] = ""
				config["error-detection-from-container-logs"] = "true"
				config["error-detection-max-number-of-lines"] = "100"
				config["hub-catalog-name"] = "test tekton"
				config["hub-url"] = "https://api.hub.tekton.dev/v2"
				return config
			}(),
		},
		{
			name: "with few fields and multi catalogs",
			inputConfig: map[string]string{
				"application-name":                       "Pipelines as Code CI test name",
				"auto-configure-new-github-repo":         "false",
				"auto-configure-repo-namespace-template": "",
				"bitbucket-cloud-additional-source-ip":   "",
				"catalog-1-id":                           "anotherhub",
				"catalog-1-name":                         "tekton",
				"catalog-1-url":                          "https://api.other.com/v1",
				"catalog-5-id":                           "anotherhub5",
				"catalog-5-name":                         "tekton1",
				"catalog-5-url":                          "https://api.other.com/v2",
				"error-detection-from-container-logs":    "true",
				"error-detection-max-number-of-lines":    "100",
				"hub-catalog-name":                       "test tekton",
				"hub-url":                                "https://api.hub.tekton.dev/v2",
			},
			expectedConfig: func() map[string]string {
				config := getDefaultExpectedConfig()
				// Override specific values for this test case
				config["application-name"] = "Pipelines as Code CI test name"
				config["auto-configure-new-github-repo"] = "false"
				config["auto-configure-repo-namespace-template"] = ""
				config["bitbucket-cloud-additional-source-ip"] = ""
				config["catalog-1-id"] = "anotherhub"
				config["catalog-1-name"] = "tekton"
				config["catalog-1-url"] = "https://api.other.com/v1"
				config["catalog-5-id"] = "anotherhub5"
				config["catalog-5-name"] = "tekton1"
				config["catalog-5-url"] = "https://api.other.com/v2"
				config["error-detection-from-container-logs"] = "true"
				config["error-detection-max-number-of-lines"] = "100"
				config["hub-catalog-name"] = "test tekton"
				config["hub-url"] = "https://api.hub.tekton.dev/v2"
				return config
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, _ := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()
			if tt.inputConfig == nil {
				tt.inputConfig = map[string]string{}
			}
			settings := &Settings{}
			err := SyncConfig(fakelogger, settings, tt.inputConfig, map[string]func(string) error{})
			if err != nil {
				t.Errorf("not expecting error but got %s", err)
			}
			actualConfigData := ConvertPacStructToConfigMap(settings)
			assert.DeepEqual(t, actualConfigData, tt.expectedConfig)
		})
	}
}
