package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// writeTestCertPair writes a self-signed pair for names into dir and returns the
// paths, so tests can rotate the file the way an operator would.
func writeTestCertPair(t *testing.T, dir string, names ...string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: names[0]},
		DNSNames:              names,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

// The front must pick the certificate whose names cover the client's SNI, and
// fall back only when nothing does — otherwise every visitor sees a mismatch.
func TestFrontCertServiceMatchesServerName(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	dir := t.TempDir()
	certPath, keyPath := writeTestCertPair(t, dir, "site.example")
	svc := &FrontCertService{}
	if err := svc.UpsertFrontCert(&model.FrontCert{Domains: "site.example", CertPath: certPath, KeyPath: keyPath}); err != nil {
		t.Fatalf("UpsertFrontCert: %v", err)
	}

	if got := svc.CertificateFor("site.example"); got == nil {
		t.Fatal("CertificateFor(site.example) returned nil, so the panel's own domain gets no certificate")
	}
	if got := svc.CertificateFor("SITE.example."); got == nil {
		t.Fatal("SNI matching must be case-insensitive and tolerate a trailing dot")
	}
	if got := svc.CertificateFor("other.example"); got != nil {
		t.Fatal("CertificateFor matched an unrelated name: a probe would get a certificate that cannot exist for its SNI")
	}
	if svc.Fallback() == nil {
		t.Fatal("Fallback returned nil, so a no-SNI client cannot complete a handshake")
	}
}

func TestWildcardCoversExactlyOneLabel(t *testing.T) {
	cases := []struct {
		pattern, host string
		want          bool
	}{
		{"*.example.com", "a.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "a.b.example.com", false},
		{"*.example.com", "notexample.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"/"+tc.host, func(t *testing.T) {
			if got := wildcardCovers(tc.pattern, tc.host); got != tc.want {
				t.Fatalf("wildcardCovers(%q,%q) = %v, want %v", tc.pattern, tc.host, got, tc.want)
			}
		})
	}
}

// Replacing the certificate file must be enough to rotate it: the operator is
// told to renew with certbot, and a panel restart would drop every session.
func TestFrontCertServicePicksUpARotatedFile(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	dir := t.TempDir()
	certPath, keyPath := writeTestCertPair(t, dir, "old.example")
	svc := &FrontCertService{}
	if err := svc.UpsertFrontCert(&model.FrontCert{Domains: "old.example", CertPath: certPath, KeyPath: keyPath}); err != nil {
		t.Fatalf("UpsertFrontCert: %v", err)
	}
	before := svc.CertificateFor("old.example")
	if before == nil {
		t.Fatal("seed certificate did not load")
	}

	writeTestCertPair(t, dir, "new.example")
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(certPath, later, later); err != nil {
		t.Fatalf("touch cert: %v", err)
	}
	svc.mu.Lock()
	svc.lastCheck = time.Time{}
	svc.mu.Unlock()

	reloaded := svc.CertificateFor("old.example")
	if reloaded == nil || reloaded == before {
		t.Fatal("a replaced certificate file was not reloaded: every renewal would need a panel restart")
	}
}

// A truncated file mid-rotation must not take the site down.
func TestFrontCertServiceKeepsTheLastGoodMaterial(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	dir := t.TempDir()
	certPath, keyPath := writeTestCertPair(t, dir, "site.example")
	svc := &FrontCertService{}
	if err := svc.UpsertFrontCert(&model.FrontCert{Domains: "site.example", CertPath: certPath, KeyPath: keyPath}); err != nil {
		t.Fatalf("UpsertFrontCert: %v", err)
	}
	good := svc.CertificateFor("site.example")
	if good == nil {
		t.Fatal("seed certificate did not load")
	}

	if err := os.WriteFile(certPath, []byte("half-written"), 0o600); err != nil {
		t.Fatalf("truncate cert: %v", err)
	}
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(certPath, later, later); err != nil {
		t.Fatalf("touch cert: %v", err)
	}
	svc.mu.Lock()
	svc.lastCheck = time.Time{}
	svc.mu.Unlock()

	if got := svc.CertificateFor("site.example"); got != good {
		t.Fatal("a broken rotation replaced the working certificate, so the site went dark until the next valid write")
	}
}

func TestUpsertFrontCertRejectsUnloadablePair(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	svc := &FrontCertService{}
	err := svc.UpsertFrontCert(&model.FrontCert{Domains: "site.example", CertPath: "/nope/cert.pem", KeyPath: "/nope/key.pem"})
	if err == nil {
		t.Fatal("a missing certificate pair was accepted, so the front would serve nothing on the next start")
	}
}
