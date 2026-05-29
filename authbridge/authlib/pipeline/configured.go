package pipeline

import (
	"context"
	"encoding/json"
)

// configuredPlugin wraps a plugin built via Configurable.Configure with the
// raw config bytes it was constructed from, so the session API can surface
// them on /v1/pipeline. All Plugin interface methods forward to the embedded
// plugin — zero observable behavior change at the request hot path.
//
// Optional plugin interfaces (Initializer, Shutdowner, Finisher, Readier)
// are NOT promoted through the embedded Plugin interface — Go does not
// promote method-set membership through an embedded interface. The wrapper
// implements each of those four interfaces explicitly and forwards
// conditionally; see the methods below (added in a follow-up commit).
type configuredPlugin struct {
	Plugin
	raw json.RawMessage
}

// WrapConfigured returns a Plugin whose dynamic type retains the raw
// config bytes the plugin was built from, so the session API can
// surface them on /v1/pipeline. Callers (plugins.Build) invoke this
// only after Configurable.Configure returns nil; non-Configurable
// plugins pass through unwrapped.
func WrapConfigured(p Plugin, raw json.RawMessage) Plugin {
	return &configuredPlugin{Plugin: p, raw: raw}
}

// RawConfig returns the raw config bytes the wrapped plugin was configured
// with. Used by sessionapi describePipeline to populate /v1/pipeline's
// Config field.
func (c *configuredPlugin) RawConfig() json.RawMessage { return c.raw }

// Init forwards to the wrapped plugin if it implements Initializer; otherwise
// returns nil (no deferred initialization). Required because Initializer is
// not declared on the Plugin interface, so the embedded Plugin's Init method
// (if any) is NOT promoted to *configuredPlugin's method set — only methods
// declared on Plugin are. Without this explicit forwarding, the framework's
// Init dispatcher would silently skip wrapped plugins.
func (c *configuredPlugin) Init(ctx context.Context) error {
	if init, ok := c.Plugin.(Initializer); ok {
		return init.Init(ctx)
	}
	return nil
}

// Shutdown forwards to the wrapped plugin if it implements Shutdowner; otherwise
// returns nil (no resources to release).
func (c *configuredPlugin) Shutdown(ctx context.Context) error {
	if sh, ok := c.Plugin.(Shutdowner); ok {
		return sh.Shutdown(ctx)
	}
	return nil
}

// OnFinish forwards to the wrapped plugin if it implements Finisher; otherwise
// no-ops. The framework's OnFinish dispatcher type-asserts against Finisher,
// so wrapped plugins must implement it explicitly to participate.
func (c *configuredPlugin) OnFinish(ctx context.Context, pctx *Context) {
	if fin, ok := c.Plugin.(Finisher); ok {
		fin.OnFinish(ctx, pctx)
	}
}

// Ready forwards to the wrapped plugin if it implements Readier; otherwise
// returns true. This matches the existing semantics in Pipeline.Ready
// (pipeline.go:287-289): plugins without Readier are considered always-ready.
func (c *configuredPlugin) Ready() bool {
	if r, ok := c.Plugin.(Readier); ok {
		return r.Ready()
	}
	return true
}
