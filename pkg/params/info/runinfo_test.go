package info

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"gotest.tools/v3/assert"
)

func TestRunInfoContext(t *testing.T) {
	ns1 := "ns1"
	info := &Info{
		Pac: &PacOpts{
			Settings: &settings.Settings{
				ApplicationName: "App for " + ns1,
			},
		},
		Kube: &KubeOpts{
			Namespace: ns1,
		},
	}
	ctx := context.TODO()
	ctx = Store(ctx, ns1, info)

	ns2 := "ns2"
	info2 := &Info{
		Pac: &PacOpts{
			Settings: &settings.Settings{
				ApplicationName: "App for " + ns2,
			},
		},
		Kube: &KubeOpts{
			Namespace: ns2,
		},
	}
	ctx = Store(ctx, ns2, info2)

	t.Run("Get", func(t *testing.T) {
		rinfo1 := Get(ctx, ns1)
		assert.Assert(t, rinfo1 != nil)
		assert.Assert(t, rinfo1.Pac.Settings.ApplicationName == "App for "+ns1)
		assert.Assert(t, rinfo1.Kube.Namespace == ns1)
		rinfo2 := Get(ctx, ns2)
		assert.Assert(t, rinfo2 != nil)
		assert.Assert(t, rinfo2.Pac.Settings.ApplicationName == "App for "+ns2)
		assert.Assert(t, rinfo2.Kube.Namespace == ns2)
	})
}
