// Package spiffe provides framework-shared SPIFFE credential helpers.
// Today the only consumer is the mTLS layer in authlib/tls and the
// proxy-sidecar listeners; future LLM-judges or audit plugins that
// need workload identity can layer on top.
//
// Compare with authlib/plugins/tokenexchange/spiffe/, which holds the
// JWT-SVID source used exclusively by token-exchange. That one stays
// plugin-internal because only token-exchange consumes it; this
// package is framework-shared because mTLS spans every listener.
package spiffe

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

// X509Source produces the local X.509-SVID + trust bundle on demand.
// Implementations are responsible for hot-rotation handling — callers
// invoke Certificate / TrustBundle on every TLS handshake.
type X509Source interface {
	// Certificate returns the local SVID (cert + private key) for use
	// in tls.Config.GetCertificate / GetClientCertificate.
	Certificate() (*tls.Certificate, error)

	// TrustBundle returns the SPIRE trust bundle for verifying peer
	// certificates. Must be reloaded by the caller on every handshake
	// to pick up bundle rotation.
	TrustBundle() (*x509.CertPool, error)
}

// FileX509Source reads spiffe-helper output from a fixed set of paths
// and re-parses on every call. Re-reading is intentional: spiffe-helper
// rotates the underlying SVID every ~2.5 minutes, and TLS handshakes
// are rare enough (one per persistent connection) that the os.ReadFile
// cost is negligible compared to the cost of stale-cert connection
// failures.
//
// All three paths default to the conventional spiffe-helper output
// locations when constructed via DefaultFileX509Source.
type FileX509Source struct {
	CertPath   string
	KeyPath    string
	BundlePath string
}

// DefaultCertPath is the cert path spiffe-helper writes per the
// kagenti chart's helper.conf template.
const DefaultCertPath = "/opt/svid.pem"

// DefaultKeyPath is the private-key path spiffe-helper writes per the
// kagenti chart's helper.conf template.
const DefaultKeyPath = "/opt/svid_key.pem"

// DefaultBundlePath is the trust-bundle path spiffe-helper writes per
// the kagenti chart's helper.conf template.
const DefaultBundlePath = "/opt/svid_bundle.pem"

// NewFileX509Source constructs a source pointed at the given paths.
// Empty strings are replaced with the spiffe-helper defaults so most
// callers can pass three empty strings and get the right paths.
func NewFileX509Source(certPath, keyPath, bundlePath string) *FileX509Source {
	if certPath == "" {
		certPath = DefaultCertPath
	}
	if keyPath == "" {
		keyPath = DefaultKeyPath
	}
	if bundlePath == "" {
		bundlePath = DefaultBundlePath
	}
	return &FileX509Source{
		CertPath:   certPath,
		KeyPath:    keyPath,
		BundlePath: bundlePath,
	}
}

// Certificate reads cert + key in the same call and validates that
// they form a usable pair. Reading both atomically (and pairing them
// before returning) addresses the rotation race where spiffe-helper
// writes cert and key in two separate fs operations: a handshake that
// lands between the writes would otherwise pick up a mismatched pair
// and fail the connection. tls.LoadX509KeyPair already validates the
// pair internally; if it errors, the next handshake retries from
// fresh disk reads and succeeds.
func (s *FileX509Source) Certificate() (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(s.CertPath, s.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("loading X.509 SVID from %s + %s: %w",
			s.CertPath, s.KeyPath, err)
	}
	return &cert, nil
}

// TrustBundle parses the bundle file as concatenated PEM blocks and
// returns a pool containing every CERTIFICATE block found. Any block
// of a different type (PRIVATE KEY, etc) is silently skipped — the
// bundle is intended to hold only CA certificates, but spiffe-helper
// has historically been permissive about layout.
//
// Empty pool is treated as an error: a TLS handshake with no roots
// would silently accept any cert, defeating the point of mTLS.
func (s *FileX509Source) TrustBundle() (*x509.CertPool, error) {
	data, err := os.ReadFile(s.BundlePath)
	if err != nil {
		return nil, fmt.Errorf("reading trust bundle from %s: %w", s.BundlePath, err)
	}
	pool := x509.NewCertPool()
	rest := data
	count := 0
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing trust-bundle certificate (block %d) from %s: %w",
				count, s.BundlePath, err)
		}
		pool.AddCert(cert)
		count++
	}
	if count == 0 {
		return nil, fmt.Errorf("trust bundle %s contained no CERTIFICATE blocks", s.BundlePath)
	}
	return pool, nil
}

// ErrEmptySource indicates the source had no cert / key / bundle data.
// Callers can errors.Is against it to distinguish "spiffe-helper
// hasn't written yet" (expected on cold start) from real errors.
var ErrEmptySource = errors.New("X509Source has no data")
