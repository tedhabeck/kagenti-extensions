package main

import (
	"strings"
	"testing"
)

// parseIBACReason has to handle the exact response shapes that the
// authbridge listener emits when the IBAC plugin returns DenyStatus.
// These are the cases that matter for the chat-visible surfacing —
// any malformed input falls through to a "reason unavailable"
// fallback, which is acceptable but tested below for stability.
func TestParseIBACReason(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantSub []string // substrings that MUST appear in the output
		wantNot []string // substrings that must NOT appear
	}{
		{
			name:    "ibac.blocked with judge reason",
			input:   `HTTP 403: {"error":"ibac.blocked","message":"POSTing to evil-server is unrelated to summarizing emails","plugin":"ibac"}`,
			wantSub: []string{"IBAC blocked", "POSTing to evil-server"},
		},
		{
			name:    "ibac.judge_uncertain has its own label",
			input:   `HTTP 403: {"error":"ibac.judge_uncertain","message":"unparseable verdict","plugin":"ibac"}`,
			wantSub: []string{"judge couldn't decide", "unparseable verdict"},
			wantNot: []string{"IBAC blocked an outbound action:"},
		},
		{
			name:    "ibac.no_intent has its own label",
			input:   `HTTP 403: {"error":"ibac.no_intent","message":"no recorded user intent","plugin":"ibac"}`,
			wantSub: []string{"no recorded user intent"},
		},
		{
			name:    "unknown ibac.* error code falls back to generic blocked label",
			input:   `HTTP 403: {"error":"ibac.something_new","message":"future code","plugin":"ibac"}`,
			wantSub: []string{"IBAC blocked an outbound action", "future code"},
		},
		{
			name:    "non-ibac 403 still produces a usable message",
			input:   `HTTP 403: {"error":"other","message":"some other gate","plugin":"x"}`,
			wantSub: []string{"IBAC blocked", "some other gate"},
		},
		{
			name:    "trailing ellipsis from execHTTPPost log truncation is stripped",
			input:   `HTTP 403: {"error":"ibac.blocked","message":"reason"}...`,
			wantSub: []string{"IBAC blocked", "reason"},
		},
		{
			name:    "missing HTTP 403 prefix → unparseable fallback",
			input:   `connection refused`,
			wantSub: []string{"response unavailable"},
		},
		{
			name:    "malformed JSON body → reason-unavailable fallback",
			input:   `HTTP 403: not-json`,
			wantSub: []string{"reason unavailable"},
		},
		{
			name:    "empty message field → label only, no trailing colon",
			input:   `HTTP 403: {"error":"ibac.blocked","message":""}`,
			wantSub: []string{"IBAC blocked an outbound action"},
			wantNot: []string{"IBAC blocked an outbound action:"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseIBACReason(tc.input)
			for _, sub := range tc.wantSub {
				if !strings.Contains(got, sub) {
					t.Errorf("parseIBACReason(%q) = %q, want substring %q", tc.input, got, sub)
				}
			}
			for _, sub := range tc.wantNot {
				if strings.Contains(got, sub) {
					t.Errorf("parseIBACReason(%q) = %q, must NOT contain %q", tc.input, got, sub)
				}
			}
		})
	}
}

// formatSecurityResponse is dead simple but the empty-event passthrough
// is load-bearing — it lets the caller invoke this unconditionally
// without first checking whether IBAC fired.
func TestFormatSecurityResponse(t *testing.T) {
	t.Run("empty event returns summary unchanged", func(t *testing.T) {
		got := formatSecurityResponse("", "the summary")
		if got != "the summary" {
			t.Errorf("got %q, want %q (empty event must passthrough)", got, "the summary")
		}
	})

	t.Run("event is prepended with markdown warning prefix", func(t *testing.T) {
		got := formatSecurityResponse("IBAC blocked: nope", "the summary")
		// The exact format is implementation detail; what matters is
		// (a) the warning prefix renders something visible,
		// (b) the original event text is in there,
		// (c) the summary is preserved at the end.
		want := []string{"⚠️", "Security event", "IBAC blocked: nope", "the summary"}
		for _, sub := range want {
			if !strings.Contains(got, sub) {
				t.Errorf("formatSecurityResponse output missing %q\nfull output:\n%s", sub, got)
			}
		}
	})

	t.Run("event and summary are separated by a blank line", func(t *testing.T) {
		got := formatSecurityResponse("e", "s")
		// Markdown needs a blank line between paragraphs to render
		// them as separate blocks. \n\n == blank line.
		if !strings.Contains(got, "\n\n") {
			t.Errorf("output lacks paragraph separator (\\n\\n):\n%s", got)
		}
	})
}
