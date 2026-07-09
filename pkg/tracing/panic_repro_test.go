package tracing

import (
	"testing"

	kres "knative.dev/pkg/observability/resource"
)

func TestKnativeResourceDefaultNoPanic(t *testing.T) {
	t.Setenv("SYSTEM_NAMESPACE", "test-ns")
	r := kres.Default("pac-test")
	t.Logf("schema URL: %s", r.SchemaURL())
}
