package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets/types"
)

// ResolveEnvVars resolves a list of EnvVar definitions into KEY=VALUE strings
// suitable for exec.Cmd.Env. Secret references are fetched from Kubernetes.
func ResolveEnvVars(ctx context.Context, envVars []v1alpha1.EnvVar, namespace string, kinteract kubeinteraction.Interface) ([]string, error) {
	baseEnv := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"LC_ALL=C",
		"LANG=C",
	}

	for _, ev := range envVars {
		if ev.Name == "" {
			return nil, fmt.Errorf("environment variable name is required")
		}

		var value string
		switch {
		case ev.SecretRef != nil:
			key := ev.SecretRef.Key
			if key == "" {
				key = "token"
			}
			secretValue, err := kinteract.GetSecret(ctx, types.GetSecretOpt{
				Namespace: namespace,
				Name:      ev.SecretRef.Name,
				Key:       key,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to resolve secret for env var %s: %w", ev.Name, err)
			}
			if secretValue == "" {
				return nil, fmt.Errorf("secret %s/%s key %s is empty for env var %s", namespace, ev.SecretRef.Name, key, ev.Name)
			}
			value = secretValue
		default:
			value = ev.Value
		}

		baseEnv = append(baseEnv, fmt.Sprintf("%s=%s", ev.Name, value))
	}

	return baseEnv, nil
}
