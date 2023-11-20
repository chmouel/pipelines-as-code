package info

import "context"

type Info struct {
	Pac  *PacOpts
	Kube *KubeOpts
}

type (
	_infoContextKey struct{}
	_nsContextKey   struct{}
)

var (
	infoContextKey = _infoContextKey{}
	nsContextKey   = _nsContextKey{}
)

type CtxInfo struct {
	Pac  map[string]*PacOpts
	Kube map[string]*KubeOpts
}

// GetInfo Pac Settings for namespace
func GetInfo(ctx context.Context, ns string) *Info {
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

// StoreInfo Pac Settings for that namespace configuration in context
func StoreInfo(ctx context.Context, ns string, info *Info) context.Context {
	if val := ctx.Value(infoContextKey); val != nil {
		if ctxInfo, ok := val.(CtxInfo); ok {
			if ctxInfo.Pac == nil {
				ctxInfo.Pac = map[string]*PacOpts{}
			}
			if ctxInfo.Kube == nil {
				ctxInfo.Kube = map[string]*KubeOpts{
					ns: {Namespace: ns},
				}
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

// StoreNS stores namespace in context
func StoreNS(ctx context.Context, ns string) context.Context {
	return context.WithValue(ctx, nsContextKey, ns)
}

// GetNS gets namespace from context
func GetNS(ctx context.Context) string {
	if val := ctx.Value(nsContextKey); val != nil {
		if ns, ok := val.(string); ok {
			return ns
		}
	}
	return ""
}
