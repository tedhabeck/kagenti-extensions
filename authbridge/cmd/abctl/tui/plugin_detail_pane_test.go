package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kagenti/kagenti-extensions/authbridge/cmd/abctl/apiclient"
)

func TestShowPluginDetailRendersConfig(t *testing.T) {
	m := newPickerModel(context.Background(), nil, nil)
	// The viewport defaults to 0×0 (sized by layout() on WindowSizeMsg);
	// in unit tests we set it manually so View() returns content.
	m.detailVp.Width = 80
	m.detailVp.Height = 20
	plugin := &apiclient.PipelinePlugin{
		Name:      "jwt-validation",
		Direction: "inbound",
		Position:  1,
		Writes:    []string{"security"},
		Config:    json.RawMessage(`{"issuer":"http://idp"}`),
	}
	m.showPluginDetail(plugin)
	view := m.detailVp.View()
	if !strings.Contains(view, "Config:") {
		t.Fatalf("rendered view missing Config section:\n%s", view)
	}
	if !strings.Contains(view, "issuer") {
		t.Fatalf("rendered view missing config key:\n%s", view)
	}
	if !strings.Contains(view, "http://idp") {
		t.Fatalf("rendered view missing config value:\n%s", view)
	}
}

func TestShowPluginDetailRendersNoneForEmptyConfig(t *testing.T) {
	m := newPickerModel(context.Background(), nil, nil)
	m.detailVp.Width = 80
	m.detailVp.Height = 20
	plugin := &apiclient.PipelinePlugin{
		Name:      "non-configurable",
		Direction: "inbound",
		Position:  1,
		Config:    nil,
	}
	m.showPluginDetail(plugin)
	view := m.detailVp.View()
	if !strings.Contains(view, "Config:") {
		t.Fatalf("rendered view missing Config section:\n%s", view)
	}
	if !strings.Contains(view, "(none)") {
		t.Fatalf("rendered view should say (none) for empty Config:\n%s", view)
	}
}

// TestShowPluginDetailHandlesMalformedConfig verifies the TUI degrades
// gracefully when Config bytes are not valid JSON. The server should
// never produce malformed bytes (Configure() validates), but corruption
// in transit isn't impossible — we lock the contract that the renderer
// writes *something* without panicking.
func TestShowPluginDetailHandlesMalformedConfig(t *testing.T) {
	m := newPickerModel(context.Background(), nil, nil)
	m.detailVp.Width = 80
	m.detailVp.Height = 20
	plugin := &apiclient.PipelinePlugin{
		Name:      "broken",
		Direction: "inbound",
		Position:  1,
		Config:    json.RawMessage(`{not valid`),
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("showPluginDetail panicked on malformed JSON: %v", r)
		}
	}()
	m.showPluginDetail(plugin)
	view := m.detailVp.View()
	if !strings.Contains(view, "Config:") {
		t.Fatalf("rendered view missing Config section:\n%s", view)
	}
	// ColorizeJSONBytes' fallback is to render the raw bytes as a muted
	// string. We don't assert exact escape-code output (style-dependent),
	// but the literal "{not" should appear somewhere.
	if !strings.Contains(view, "{not") {
		t.Fatalf("rendered view missing raw config fallback:\n%s", view)
	}
}
