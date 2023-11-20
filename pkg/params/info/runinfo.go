package info

import "context"

type Info struct {
	Pac  *PacOpts
	Kube *KubeOpts
}

type contextKey struct{}

var infoContextKey = contextKey{}

type CtxInfo struct {
	Pac  map[string]*PacOpts
	Kube map[string]*KubeOpts
}

// Get Pac Settings for namespace
func Get(ctx context.Context, ns string) *Info {
	if val := ctx.Value(infoContextKey); val != nil {
		if ctxInfo, ok := val.(CtxInfo); ok {
			ret := &Info{}
			if pac, ok := ctxInfo.Pac[ns]; ok {
				ret.Pac = pac
			}
			if kube, ok := ctxInfo.Kube[ns]; ok {
				ret.Kube = kube
			}
			return ret
		}
	}
	return nil
}

// Store Pac Settings for that namespace configuration in context
func Store(ctx context.Context, ns string, info *Info) context.Context {
	if val := ctx.Value(infoContextKey); val != nil {
		if ctxInfo, ok := val.(CtxInfo); ok {
			if ctxInfo.Pac == nil {
				ctxInfo.Pac = map[string]*PacOpts{}
			}
			if ctxInfo.Kube == nil {
				ctxInfo.Kube = map[string]*KubeOpts{}
			}
			ctxInfo.Pac[ns] = info.Pac
			ctxInfo.Kube[ns] = info.Kube
			return context.WithValue(ctx, infoContextKey, ctxInfo)
		}
	}
	return context.WithValue(ctx, infoContextKey, CtxInfo{
		Pac: map[string]*PacOpts{
			ns: info.Pac,
		},
		Kube: map[string]*KubeOpts{
			ns: info.Kube,
		},
	})
}
