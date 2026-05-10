package pipeline

import (
	"encoding/json"
	"log/slog"

	"github.com/kagenti/kagenti-extensions/authbridge/authlib/contracts"
)

// This file wires the named protocol extensions (A2AExtension,
// MCPExtension, InferenceExtension) to contracts.ContentSource. The
// methods live alongside their receiver types rather than with the
// parser plugins because Go only allows defining methods on a type
// from that type's own package.
//
// Compile-time assertions make the implementation visible to
// grep/LSP and catch interface drift early.
var (
	_ contracts.ContentSource = (*A2AExtension)(nil)
	_ contracts.ContentSource = (*MCPExtension)(nil)
	_ contracts.ContentSource = (*InferenceExtension)(nil)
)

// Fragments implements contracts.ContentSource for A2A messages.
//
// Request-phase: iterates message Parts, emitting text and data parts
// tagged with the message role (normalized: A2A's native "agent" role
// is rewritten to "assistant" so guardrails match a single vocabulary
// across A2A and Inference). File parts carry URIs or base64 blobs,
// not prose; they're skipped.
//
// Response-phase: the final artifact is assistant-authored text.
func (e *A2AExtension) Fragments() []contracts.Fragment {
	if e == nil {
		return nil
	}
	var out []contracts.Fragment

	role := normalizeA2ARole(e.Role)
	for _, p := range e.Parts {
		switch p.Kind {
		case "text", "data":
			if p.Content != "" {
				out = append(out, contracts.Fragment{Role: role, Text: p.Content})
			}
		case "file":
			// File parts carry URIs or base64 blobs; not inspectable as
			// prose. A dedicated file-scanning guardrail can type-assert
			// to *A2AExtension and access the raw Parts directly.
		}
	}

	if e.Artifact != "" {
		out = append(out, contracts.Fragment{Role: contracts.RoleAssistant, Text: e.Artifact})
	}
	return out
}

// normalizeA2ARole rewrites A2A's native role vocabulary to match the
// Inference / OpenAI-style vocabulary. Keeping guardrails to a single
// role set across protocols is worth the small loss of A2A fidelity.
// A2A-aware consumers may type-assert to *A2AExtension to read the
// raw Role field directly; framework-generic guardrails consuming via
// the ContentSource interface treat the normalized value as
// authoritative (the interface deliberately does not surface the raw
// value).
func normalizeA2ARole(r string) string {
	switch r {
	case "agent":
		return contracts.RoleAssistant
	case "user":
		return contracts.RoleUser
	default:
		// Unknown / unset roles pass through so guardrails at least
		// see something to filter on. Empty string is tolerated too.
		return r
	}
}

// Fragments implements contracts.ContentSource for MCP messages.
//
// Request-phase: only tools/call is modeled — it's the one MCP method
// carrying user-intent content. Control-plane calls (initialize, ping,
// tools/list, resources/list, etc.) return nil. The tool name is
// emitted as role=tool; each argument value is emitted as
// role=tool_args, JSON-stringified if non-string.
//
// Response-phase: MCP tool results are conventionally shaped as
// {"content": [{"type":"text","text":"..."}, {"type":"image",...}, ...]}.
// Text items are emitted with role=tool_result; non-text items are
// skipped as not inspectable.
func (e *MCPExtension) Fragments() []contracts.Fragment {
	if e == nil {
		return nil
	}
	var out []contracts.Fragment

	if e.Method == "tools/call" && e.Params != nil {
		if name, _ := e.Params["name"].(string); name != "" {
			out = append(out, contracts.Fragment{Role: contracts.RoleTool, Text: name})
		}
		if args, ok := e.Params["arguments"].(map[string]any); ok {
			for _, v := range args {
				text := stringifyAny(v)
				if text != "" {
					out = append(out, contracts.Fragment{Role: contracts.RoleToolArgs, Text: text})
				}
			}
		}
	}

	if e.Result != nil {
		if items, ok := e.Result["content"].([]any); ok {
			for _, it := range items {
				m, ok := it.(map[string]any)
				if !ok {
					continue
				}
				if m["type"] != "text" {
					continue
				}
				if t, _ := m["text"].(string); t != "" {
					out = append(out, contracts.Fragment{Role: contracts.RoleToolResult, Text: t})
				}
			}
		}
	}
	return out
}

// Fragments implements contracts.ContentSource for Inference messages.
//
// Request-phase: walks the Messages slice. OpenAI's role vocabulary
// maps to our standard values directly, except that OpenAI's "tool"
// role marks a tool RESULT in the conversation history — remapped to
// "tool_result" so it lines up with MCP's tool result semantics.
//
// Response-phase: the model's completion (assistant) plus any tool
// calls the model emitted (tool name + arguments as separate fragments).
func (e *InferenceExtension) Fragments() []contracts.Fragment {
	if e == nil {
		return nil
	}
	// Use a nil slice so an empty result returns nil, consistent with
	// A2AExtension.Fragments and MCPExtension.Fragments — append
	// tolerates nil and the cap hint isn't measurable on this path.
	var out []contracts.Fragment

	for _, m := range e.Messages {
		if m.Content == "" {
			continue
		}
		role := m.Role
		if role == "tool" {
			role = contracts.RoleToolResult
		}
		out = append(out, contracts.Fragment{Role: role, Text: m.Content})
	}

	if e.Completion != "" {
		out = append(out, contracts.Fragment{Role: contracts.RoleAssistant, Text: e.Completion})
	}
	for _, tc := range e.ToolCalls {
		if tc.Name != "" {
			out = append(out, contracts.Fragment{Role: contracts.RoleTool, Text: tc.Name})
		}
		if tc.Arguments != "" {
			out = append(out, contracts.Fragment{Role: contracts.RoleToolArgs, Text: tc.Arguments})
		}
	}

	return out
}

// stringifyAny renders an arbitrary argument value as a string suitable
// for text scanning. Strings pass through unchanged; anything else goes
// through JSON so nested maps / slices become flat inspectable text.
//
// Precondition: v should be JSON-origin data (values that came out of
// json.Unmarshal into map[string]any / []any / primitives). Those
// round-trip through json.Marshal without error in practice. Values
// with unmarshalable types (channels, funcs, cyclic refs) will hit the
// error path — the function returns "" and logs at DEBUG so the skip
// is observable in verbose runs rather than silent. Callers filter
// empty strings regardless.
func stringifyAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		slog.Debug("pipeline/content: stringifyAny marshal error, returning empty",
			"error", err)
		return ""
	}
	return string(b)
}
