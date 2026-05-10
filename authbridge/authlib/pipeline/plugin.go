package pipeline

import "context"

// Plugin is the interface that all pipeline extensions implement.
type Plugin interface {
	Name() string
	Capabilities() PluginCapabilities
	OnRequest(ctx context.Context, pctx *Context) Action
	OnResponse(ctx context.Context, pctx *Context) Action
}

// PluginCapabilities declares what extension slots a plugin reads and
// writes, plus whether it accesses the request / response body.
//
// The pipeline validates at startup that all Reads are satisfied by an
// earlier plugin's Writes. Body-access fields drive the listener's
// body-buffering handshake (ext_proc ProcessingMode, net/http read-body).
type PluginCapabilities struct {
	// Reads / Writes name extension slots (A2A, MCP, Inference, Custom
	// map keys). Checked at pipeline.New.
	Reads  []string
	Writes []string

	// ReadsBody: the plugin reads pctx.Body in OnRequest and/or
	// pctx.ResponseBody in OnResponse. The listener buffers the body
	// when any plugin declares this; without it, pctx.Body is nil and
	// a read silently sees "no body."
	ReadsBody bool

	// WritesBody: the plugin may mutate pctx.Body / pctx.ResponseBody
	// (call pctx.SetBody / pctx.SetResponseBody). Implies ReadsBody —
	// Normalize() auto-promotes. Listener propagates the mutation to
	// the wire (ext_proc BodyMutation, or the outbound http.Request /
	// downstream http.Response for proxy listeners).
	//
	// Pipeline.New rejects a pipeline that has more than one WritesBody
	// plugin per direction — mutation ordering would be ambiguous.
	// Waypoint mode (ext_authz) cannot support WritesBody at all:
	// ext_authz has no body-mutation field. main.go enforces this at
	// process boot.
	WritesBody bool

	// BodyAccess is a deprecated alias for ReadsBody, kept so existing
	// plugins compile unchanged through one release. Normalize() folds
	// BodyAccess into ReadsBody before validation and listener
	// negotiation read the normalized fields.
	//
	// Deprecated: use ReadsBody. Will be removed in a future release.
	BodyAccess bool
}

// Normalize applies compatibility rules to a PluginCapabilities:
//   - BodyAccess (deprecated) is folded into ReadsBody.
//   - WritesBody implies ReadsBody (you can't mutate what you didn't see).
//
// Called by Pipeline.New for every plugin's declared capabilities so the
// rest of the framework reads a normalized form. Plugins never need to
// call this themselves.
func (c PluginCapabilities) Normalize() PluginCapabilities {
	if c.BodyAccess {
		c.ReadsBody = true
	}
	if c.WritesBody {
		c.ReadsBody = true
	}
	return c
}

// Initializer is an optional interface a plugin may implement when it
// needs to run work once before the pipeline starts serving traffic.
// Typical uses: load a model, warm a cache, open a database connection,
// register Prometheus metrics, spawn a background goroutine. Init is
// called by Pipeline.Start exactly once, in plugin declaration order.
// If any plugin's Init returns an error the pipeline fails fast —
// Pipeline.Start returns the error without calling Init on later
// plugins (nothing to unwind: earlier plugins succeeded).
//
// Plugins that don't need initialization simply don't implement this
// interface; the pipeline skips them. Keeping it optional preserves
// backward compatibility with every existing plugin.
type Initializer interface {
	Init(ctx context.Context) error
}

// Shutdowner is an optional interface a plugin may implement when it
// needs to release resources on graceful shutdown. Typical uses: flush
// in-flight audit events, close a DB connection, cancel a background
// goroutine it spawned in Init. Shutdown is called by Pipeline.Stop
// exactly once, in reverse declaration order (LIFO — symmetric with
// OnResponse dispatch) so a plugin that depends on an earlier plugin's
// resources can still use them while shutting down.
//
// Shutdown is best-effort: errors are logged but do not prevent other
// plugins from shutting down. The caller-supplied ctx carries a
// shutdown deadline; plugins must respect it and return rather than
// block indefinitely.
type Shutdowner interface {
	Shutdown(ctx context.Context) error
}

// Readier is an optional interface a plugin may implement when it has
// deferred initialization that matters to a /readyz probe. The host
// ANDs Ready() across all implementers to decide whether the pipeline
// is ready to serve traffic. A plugin whose Configure succeeded but
// whose Init is still waiting (e.g. for a credential file to be
// written by client-registration) returns false — the kubelet keeps
// traffic off the pod until Init completes.
//
// Plugins without deferred state don't implement this interface and
// are treated as always-ready. Pipeline.Ready() returns true when
// every Readier-implementing plugin returns true.
//
// Ready is expected to be cheap (pointer read / atomic load). The
// /readyz handler calls it on every probe (~10s cadence from kubelet).
type Readier interface {
	Ready() bool
}
