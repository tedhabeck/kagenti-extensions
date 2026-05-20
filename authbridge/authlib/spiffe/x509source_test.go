package spiffe_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kagenti/kagenti-extensions/authbridge/authlib/spiffe"
)

// generateSVID produces a freshly-signed cert + key pair plus the
// PEM-encoded CA bundle that signed it. Mirrors the shape of what
// spiffe-helper would write to disk: cert PEM, key PEM, bundle PEM
// (a single self-signed CA cert).
//
// The returned URI SAN uses the spiffe:// scheme so tests can verify
// peer-identity extraction in downstream packages.
func generateSVID(t *testing.T, spiffeID string) (certPEM, keyPEM, bundlePEM []byte) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}
	uri, err := url.Parse(spiffeID)
	if err != nil {
		t.Fatalf("spiffe id: %v", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		URIs:         []*url.URL{uri},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caTmpl, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("leaf cert: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	bundlePEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	return
}

// writeSVIDFiles drops cert/key/bundle PEM into a temp dir at
// the spiffe-helper-style filenames the source expects.
func writeSVIDFiles(t *testing.T, certPEM, keyPEM, bundlePEM []byte) (certPath, keyPath, bundlePath string) {
	t.Helper()
	dir := t.TempDir()
	certPath = filepath.Join(dir, "svid.pem")
	keyPath = filepath.Join(dir, "svid_key.pem")
	bundlePath = filepath.Join(dir, "svid_bundle.pem")
	for _, w := range []struct {
		path string
		data []byte
	}{
		{certPath, certPEM}, {keyPath, keyPEM}, {bundlePath, bundlePEM},
	} {
		if err := os.WriteFile(w.path, w.data, 0o644); err != nil {
			t.Fatalf("writing %s: %v", w.path, err)
		}
	}
	return
}

// Happy path — Certificate loads cert+key and TrustBundle parses the
// bundle into a pool with the expected number of CAs.
func TestFileX509Source_Certificate_Success(t *testing.T) {
	certPEM, keyPEM, bundlePEM := generateSVID(t, "spiffe://test/workload/a")
	certPath, keyPath, bundlePath := writeSVIDFiles(t, certPEM, keyPEM, bundlePEM)

	src := spiffe.NewFileX509Source(certPath, keyPath, bundlePath)
	cert, err := src.Certificate()
	if err != nil {
		t.Fatalf("Certificate: %v", err)
	}
	if cert.PrivateKey == nil {
		t.Errorf("PrivateKey nil — cert/key load was incomplete")
	}
	if len(cert.Certificate) == 0 {
		t.Errorf("cert.Certificate empty")
	}
}

// Bundle parses into a non-empty pool.
func TestFileX509Source_TrustBundle_Success(t *testing.T) {
	certPEM, keyPEM, bundlePEM := generateSVID(t, "spiffe://test/workload/a")
	_, _, bundlePath := writeSVIDFiles(t, certPEM, keyPEM, bundlePEM)

	src := spiffe.NewFileX509Source("", "", bundlePath)
	pool, err := src.TrustBundle()
	if err != nil {
		t.Fatalf("TrustBundle: %v", err)
	}
	// crypto/x509.CertPool doesn't expose a count, but Subjects()
	// returns one DER-encoded subject per cert in the pool.
	if got := len(pool.Subjects()); got != 1 { //nolint:staticcheck // SA1019: Subjects() is the only count knob exposed
		t.Errorf("pool subject count = %d, want 1", got)
	}
}

// Rotation: write new SVID files mid-flight, next call sees the new
// data. Critical for spiffe-helper's ~2.5min rotation cycle.
func TestFileX509Source_Rotation(t *testing.T) {
	certPEM1, keyPEM1, bundlePEM1 := generateSVID(t, "spiffe://test/workload/old")
	certPath, keyPath, bundlePath := writeSVIDFiles(t, certPEM1, keyPEM1, bundlePEM1)

	src := spiffe.NewFileX509Source(certPath, keyPath, bundlePath)

	cert1, err := src.Certificate()
	if err != nil {
		t.Fatalf("first Certificate: %v", err)
	}

	// Rotate: overwrite all three files with a brand-new pair.
	certPEM2, keyPEM2, bundlePEM2 := generateSVID(t, "spiffe://test/workload/new")
	if err := os.WriteFile(certPath, certPEM2, 0o644); err != nil {
		t.Fatalf("rewrite cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM2, 0o644); err != nil {
		t.Fatalf("rewrite key: %v", err)
	}
	if err := os.WriteFile(bundlePath, bundlePEM2, 0o644); err != nil {
		t.Fatalf("rewrite bundle: %v", err)
	}

	cert2, err := src.Certificate()
	if err != nil {
		t.Fatalf("second Certificate: %v", err)
	}
	// Different cert serial / issuer → DER bytes differ. Compare the
	// leaf DER to confirm rotation actually surfaces.
	if string(cert1.Certificate[0]) == string(cert2.Certificate[0]) {
		t.Errorf("rotation didn't change the cert; both calls returned the same DER")
	}
}

// Mid-rotation race: cert and key files briefly disagree (e.g. the
// helper wrote cert but not yet key). tls.LoadX509KeyPair returns an
// error in that case. The source must propagate it cleanly so the
// caller's next handshake retries — not panic, not return a
// silently-broken cert.
func TestFileX509Source_CertKeyMismatch(t *testing.T) {
	certPEM1, _, _ := generateSVID(t, "spiffe://test/workload/a")
	_, keyPEM2, _ := generateSVID(t, "spiffe://test/workload/b") // unrelated key
	dir := t.TempDir()
	certPath := filepath.Join(dir, "svid.pem")
	keyPath := filepath.Join(dir, "svid_key.pem")
	if err := os.WriteFile(certPath, certPEM1, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM2, 0o644); err != nil {
		t.Fatalf("write key: %v", err)
	}
	src := spiffe.NewFileX509Source(certPath, keyPath, "")
	_, err := src.Certificate()
	if err == nil {
		t.Fatalf("expected error on cert/key mismatch")
	}
}

// Missing cert file — error mentions the path so operators can
// diagnose without grepping the source.
func TestFileX509Source_MissingCert(t *testing.T) {
	src := spiffe.NewFileX509Source("/nonexistent/svid.pem", "/nonexistent/svid_key.pem", "/nonexistent/bundle.pem")
	_, err := src.Certificate()
	if err == nil {
		t.Fatal("expected error on missing cert file")
	}
}

// Missing bundle file — error mentions the path.
func TestFileX509Source_MissingBundle(t *testing.T) {
	src := spiffe.NewFileX509Source("/nonexistent/svid.pem", "/nonexistent/svid_key.pem", "/nonexistent/bundle.pem")
	_, err := src.TrustBundle()
	if err == nil {
		t.Fatal("expected error on missing bundle file")
	}
}

// Empty bundle file — must be rejected, not silently accepted (an
// empty pool would let any cert through, defeating mTLS).
func TestFileX509Source_EmptyBundle(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "svid_bundle.pem")
	if err := os.WriteFile(bundlePath, []byte(""), 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	src := spiffe.NewFileX509Source("", "", bundlePath)
	_, err := src.TrustBundle()
	if err == nil {
		t.Fatal("expected error on empty bundle (would accept any cert)")
	}
}

// Bundle with only non-CERTIFICATE PEM blocks — same outcome as
// empty: rejected as a degenerate case.
func TestFileX509Source_BundleOnlyNonCertBlocks(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "svid_bundle.pem")
	junk := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not a cert")})
	if err := os.WriteFile(bundlePath, junk, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := spiffe.NewFileX509Source("", "", bundlePath)
	_, err := src.TrustBundle()
	if err == nil {
		t.Fatal("expected error on bundle with no CERTIFICATE blocks")
	}
}

// Bundle with multiple CAs — pool counts both.
func TestFileX509Source_BundleMultipleCAs(t *testing.T) {
	_, _, ca1PEM := generateSVID(t, "spiffe://test/a")
	_, _, ca2PEM := generateSVID(t, "spiffe://test/b")
	combined := append(append([]byte{}, ca1PEM...), ca2PEM...)
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "svid_bundle.pem")
	if err := os.WriteFile(bundlePath, combined, 0o644); err != nil {
		t.Fatalf("write combined: %v", err)
	}
	src := spiffe.NewFileX509Source("", "", bundlePath)
	pool, err := src.TrustBundle()
	if err != nil {
		t.Fatalf("TrustBundle: %v", err)
	}
	if got := len(pool.Subjects()); got != 2 { //nolint:staticcheck // SA1019: Subjects() is the only count knob exposed
		t.Errorf("pool subject count = %d, want 2", got)
	}
}

// Default paths kick in when empty strings are passed. We can't read
// /opt/svid.pem on the test runner, but we can verify the default
// paths land in the right struct fields.
func TestNewFileX509Source_Defaults(t *testing.T) {
	src := spiffe.NewFileX509Source("", "", "")
	if src.CertPath != spiffe.DefaultCertPath {
		t.Errorf("CertPath = %q, want %q", src.CertPath, spiffe.DefaultCertPath)
	}
	if src.KeyPath != spiffe.DefaultKeyPath {
		t.Errorf("KeyPath = %q, want %q", src.KeyPath, spiffe.DefaultKeyPath)
	}
	if src.BundlePath != spiffe.DefaultBundlePath {
		t.Errorf("BundlePath = %q, want %q", src.BundlePath, spiffe.DefaultBundlePath)
	}
}
