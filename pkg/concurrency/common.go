package concurrency

import (
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// RepoKey generates a unique key for a repository
func RepoKey(repo *v1alpha1.Repository) string {
	return fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)
}

// PrKey generates a unique key for a PipelineRun
func PrKey(run *tektonv1.PipelineRun) string {
	return fmt.Sprintf("%s/%s", run.Namespace, run.Name)
}

// ParseRepositoryKey parses a repository key into namespace and name
func ParseRepositoryKey(repoKey string) (namespace, name string, err error) {
	parts := splitKey(repoKey)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid repository key format: %s", repoKey)
	}
	return parts[0], parts[1], nil
}

// splitKey splits a key by "/" separator
func splitKey(key string) []string {
	// Simple split by "/" - could be enhanced with more robust parsing if needed
	result := make([]string, 0, 2)
	start := 0
	for i, char := range key {
		if char == '/' {
			if i > start {
				result = append(result, key[start:i])
			}
			start = i + 1
		}
	}
	if start < len(key) {
		result = append(result, key[start:])
	}
	return result
}
