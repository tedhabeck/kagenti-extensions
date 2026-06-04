package cpexruntime

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNew(t *testing.T) {
	r := New()
	if r == nil {
		t.Fatal("New() returned nil")
	}
	if r.manager != nil {
		t.Error("manager should be nil before Init")
	}
}

func TestName(t *testing.T) {
	r := New()
	if got := r.Name(); got != pluginName {
		t.Errorf("Name() = %q, want %q", got, pluginName)
	}
}

func TestCapabilities(t *testing.T) {
	caps := New().Capabilities()

	if !caps.ReadsBody {
		t.Error("ReadsBody should be true (bridge reads LLM prompt body)")
	}
	if !caps.WritesBody {
		t.Error("WritesBody should be true (llm-pii-redactor rewrites body)")
	}

	wantRequiresAny := map[string]bool{"mcp-parser": true, "inference-parser": true}
	if len(caps.RequiresAny) != len(wantRequiresAny) {
		t.Errorf("RequiresAny len = %d, want %d", len(caps.RequiresAny), len(wantRequiresAny))
	}
	for _, name := range caps.RequiresAny {
		if !wantRequiresAny[name] {
			t.Errorf("unexpected RequiresAny entry %q", name)
		}
	}
}

func TestConfigure_InvalidJSON(t *testing.T) {
	err := New().Configure(json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("Configure: expected error for invalid JSON, got nil")
	}
}

func TestConfigure_UnknownField(t *testing.T) {
	// DisallowUnknownFields is set on the decoder — extra fields are rejected.
	err := New().Configure(json.RawMessage(`{"unknown_field": true}`))
	if err == nil {
		t.Fatal("Configure: expected error for unknown field, got nil")
	}
}

func TestConfigure_EmptyChain(t *testing.T) {
	// validate() must reject an empty chain — an empty pipeline is a no-op
	// and almost certainly a misconfiguration.
	err := New().Configure(json.RawMessage(`{"chain": []}`))
	if err == nil {
		t.Fatal("Configure: expected error for empty chain, got nil")
	}
}

func TestConfigure_ValidChain(t *testing.T) {
	raw := json.RawMessage(`{"chain": [{"name": "scope-tool-gate"}]}`)
	if err := New().Configure(raw); err != nil {
		t.Errorf("Configure: unexpected error for valid chain: %v", err)
	}
}

func TestConfigure_ChainWithPluginConfig(t *testing.T) {
	// Plugin-specific config payload is an opaque JSON blob forwarded to
	// the CPEX manager — the outer schema must accept it without error.
	raw := json.RawMessage(`{
		"chain": [
			{"name": "llm-pii-redactor", "config": {"mode": "redact"}},
			{"name": "scope-tool-gate"}
		]
	}`)
	if err := New().Configure(raw); err != nil {
		t.Errorf("Configure: unexpected error for chain with plugin config: %v", err)
	}
}

func TestShutdown_Uninit(t *testing.T) {
	// Shutdown must be a no-op when Init was never called (manager == nil).
	r := New()
	if err := r.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown on uninitialized plugin returned error: %v", err)
	}
	// Calling Shutdown twice must also be safe.
	if err := r.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown returned error: %v", err)
	}
}
