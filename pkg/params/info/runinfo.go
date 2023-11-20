package info

import "context"

type Info struct {
	Pac  *PacOpts
	Kube KubeOpts
}

type contextKey struct{}

var infoContextKey = contextKey{}

type CtxInfo struct {
	Pac map[string]*PacOpts
}

// Get Pac Settings for namespace
func Get(ctx context.Context, ns string) *PacOpts {
	if val := ctx.Value(infoContextKey); val != nil {
		if ctxInfo, ok := val.(CtxInfo); ok {
			if pac, ok := ctxInfo.Pac[ns]; ok {
				return pac
			}
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
			ctxInfo.Pac[ns] = info.Pac
			return context.WithValue(ctx, infoContextKey, ctxInfo)
		}
	}
	return context.WithValue(ctx, infoContextKey, CtxInfo{
		Pac: map[string]*PacOpts{
			ns: info.Pac,
		},
	})
}
