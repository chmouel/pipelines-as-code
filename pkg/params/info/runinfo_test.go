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
				ApplicationName: "App for ns1",
			},
		},
	}
	ctx := context.TODO()
	ctx = Store(ctx, ns1, info)

	ns2 := "ns2"
	info2 := &Info{
		Pac: &PacOpts{
			Settings: &settings.Settings{
				ApplicationName: "App for ns2",
			},
		},
	}
	ctx = Store(ctx, ns2, info2)

	t.Run("Get", func(t *testing.T) {
		pac1 := Get(ctx, ns1)
		assert.Assert(t, pac1 != nil)
		assert.Assert(t, pac1.Settings.ApplicationName == "App for ns1")
		pac2 := Get(ctx, ns2)
		assert.Assert(t, pac2 != nil)
		assert.Assert(t, pac2.Settings.ApplicationName == "App for ns2")
	})
}
