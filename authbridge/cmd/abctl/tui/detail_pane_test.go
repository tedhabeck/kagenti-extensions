package tui

import (
	"strings"
	"testing"

	"github.com/kagenti/kagenti-extensions/authbridge/authlib/pipeline"
)

// tlsHeader returns "" for plaintext events so callers can prepend
// unconditionally. Locks the contract that the header is invisible
// on non-TLS connections.
func TestTLSHeader_NilProducesEmpty(t *testing.T) {
	if got := tlsHeader(nil); got != "" {
		t.Errorf("tlsHeader(nil) = %q, want empty", got)
	}
}

// Full TLS state — the header includes version, cipher, and peer.
func TestTLSHeader_FullState(t *testing.T) {
	got := tlsHeader(&pipeline.EventTLS{
		Version:      "TLS 1.3",
		CipherSuite:  "TLS_AES_128_GCM_SHA256",
		PeerSPIFFEID: "spiffe://kagenti.local/ns/team1/sa/caller-agent",
	})
	for _, want := range []string{
		"TLS:",
		"version: TLS 1.3",
		"cipher: TLS_AES_128_GCM_SHA256",
		"peer:    spiffe://kagenti.local/ns/team1/sa/caller-agent",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("tlsHeader missing %q\ngot:\n%s", want, got)
		}
	}
}

// Partial state — version+cipher only, no peer (e.g. peer cert had
// no SPIFFE URI). The peer line should be absent rather than showing
// "peer: " with an empty value.
func TestTLSHeader_NoPeerSPIFFEID(t *testing.T) {
	got := tlsHeader(&pipeline.EventTLS{
		Version:     "TLS 1.3",
		CipherSuite: "TLS_AES_128_GCM_SHA256",
	})
	if strings.Contains(got, "peer:") {
		t.Errorf("tlsHeader unexpectedly included peer line on no-SPIFFE cert\ngot:\n%s", got)
	}
	if !strings.Contains(got, "version: TLS 1.3") {
		t.Errorf("tlsHeader missing version on partial state\ngot:\n%s", got)
	}
}

// Empty version + cipher but with peer — the version/cipher line
// is omitted, but peer still shows. Tolerates partial wire data
// gracefully.
func TestTLSHeader_PeerOnly(t *testing.T) {
	got := tlsHeader(&pipeline.EventTLS{
		PeerSPIFFEID: "spiffe://test/example",
	})
	if !strings.Contains(got, "spiffe://test/example") {
		t.Errorf("tlsHeader missing peer\ngot:\n%s", got)
	}
	if strings.Contains(got, "version:") || strings.Contains(got, "cipher:") {
		t.Errorf("tlsHeader unexpectedly included version/cipher on peer-only state\ngot:\n%s", got)
	}
}
