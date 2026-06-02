package extproc

import (
	"context"
	"testing"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/kagenti/kagenti-extensions/authbridge/authlib/pipeline"
)

// mintPlugin rewrites the inbound Authorization header to a minted
// credential. Used to assert handleInbound emits a SetHeaders mutation
// (via replaceTokenResponse) carrying the new value so Envoy rewrites the
// request to the agent.
type mintPlugin struct{}

func (mintPlugin) Name() string { return "mint" }
func (mintPlugin) Capabilities() pipeline.PluginCapabilities {
	return pipeline.PluginCapabilities{}
}
func (mintPlugin) OnRequest(_ context.Context, pctx *pipeline.Context) pipeline.Action {
	pctx.Headers.Set("Authorization", "Bearer abph_minted")
	return pipeline.Action{Type: pipeline.Continue}
}
func (mintPlugin) OnResponse(_ context.Context, _ *pipeline.Context) pipeline.Action {
	return pipeline.Action{Type: pipeline.Continue}
}

// setHeaderValue extracts the value for the named SetHeaders key from a
// RequestHeaders ProcessingResponse. replaceTokenResponse stores the value
// in RawValue; fall back to Value for robustness. Returns ("", false) when
// the key is absent.
func setHeaderValue(resp *extprocv3.ProcessingResponse, key string) (string, bool) {
	rh := resp.GetRequestHeaders()
	if rh == nil || rh.GetResponse() == nil || rh.GetResponse().GetHeaderMutation() == nil {
		return "", false
	}
	for _, h := range rh.GetResponse().GetHeaderMutation().GetSetHeaders() {
		hv := h.GetHeader()
		if hv == nil || hv.GetKey() != key {
			continue
		}
		if rv := hv.GetRawValue(); len(rv) > 0 {
			return string(rv), true
		}
		return hv.GetValue(), true
	}
	return "", false
}

// TestExtProc_Inbound_AuthorizationMutation: a plugin that rewrites the
// inbound Authorization header must cause handleInbound to emit a SetHeaders
// mutation carrying the new value, so Envoy rewrites the request to the
// agent. Without the inbound emission path this returns a plain allow with
// no auth mutation.
func TestExtProc_Inbound_AuthorizationMutation(t *testing.T) {
	p, err := pipeline.New([]pipeline.Plugin{mintPlugin{}})
	if err != nil {
		t.Fatalf("building pipeline: %v", err)
	}
	srv := &Server{InboundPipeline: pipeline.NewHolder(p)}

	stream := &mockStream{ctx: context.Background()}
	headers := makeHeaders(
		"x-authbridge-direction", "inbound",
		"authorization", "Bearer real-user-token",
		":path", "/api/test",
	)

	resp, _ := srv.handleInbound(stream, headers, nil)

	got, ok := setHeaderValue(resp, "authorization")
	if !ok {
		t.Fatalf("expected SetHeaders mutation for authorization, got %+v", resp)
	}
	if got != "Bearer abph_minted" {
		t.Errorf("authorization = %q, want %q", got, "Bearer abph_minted")
	}
}
