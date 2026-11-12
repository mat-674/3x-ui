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
)

// writeEdgeCert writes a certificate for names and returns its paths.
func writeEdgeCert(t *testing.T, dir string, names ...string) (string, string) {
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

func edgeTestDB(t *testing.T) *SettingService {
	t.Helper()
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	return &SettingService{}
}

func seedEdge(t *testing.T, svc *SettingService, pairs ...[2]string) {
	t.Helper()
	for _, p := range pairs {
		if err := svc.setString(p[0], p[1]); err != nil {
			t.Fatalf("seed %s: %v", p[0], err)
		}
	}
}

// An operator who already pointed the panel at a certificate must not have to
// copy it again: an empty perimeter path has to fall through to the panel's.
func TestEdgeSettingsFallBackToThePanelCertificate(t *testing.T) {
	svc := edgeTestDB(t)
	dir := t.TempDir()
	certPath, keyPath := writeEdgeCert(t, dir, "site.example")
	seedEdge(t, svc, [2]string{"edgeEnable", "true"}, [2]string{"edgeCertFile", ""},
		[2]string{"edgeKeyFile", ""}, [2]string{"webCertFile", certPath}, [2]string{"webKeyFile", keyPath})

	settings, err := svc.GetEdgeSettings()
	if err != nil {
		t.Fatalf("GetEdgeSettings: %v", err)
	}
	if settings.CertFile != certPath || settings.KeyFile != keyPath {
		t.Fatalf("cert/key = %q/%q, want the panel's %q/%q", settings.CertFile, settings.KeyFile, certPath, keyPath)
	}
}

// With no explicit names the certificate is the only source a client can
// validate against, so the perimeter must take its names from it.
func TestBuildEdgeConfigTakesServerNamesFromTheCertificate(t *testing.T) {
	svc := edgeTestDB(t)
	dir := t.TempDir()
	certPath, keyPath := writeEdgeCert(t, dir, "site.example", "www.site.example")
	seedEdge(t, svc, [2]string{"edgeEnable", "true"}, [2]string{"edgeCertFile", certPath},
		[2]string{"edgeKeyFile", keyPath}, [2]string{"edgeServerNames", ""},
		[2]string{"webPort", "2053"}, [2]string{"webBasePath", "/adm9x/"})

	cfg, err := svc.BuildEdgeConfig()
	if err != nil {
		t.Fatalf("BuildEdgeConfig: %v", err)
	}
	if len(cfg.ServerNames) != 2 || cfg.ServerNames[0] != "site.example" {
		t.Fatalf("server names = %v, want the certificate's names", cfg.ServerNames)
	}
	if cfg.PanelUpstream != "127.0.0.1:2053" {
		t.Fatalf("panel upstream = %q, want loopback", cfg.PanelUpstream)
	}
	if cfg.PanelPrefix != "/adm9x/" {
		t.Fatalf("panel prefix = %q, want the secret base path", cfg.PanelPrefix)
	}
}

// A certificate with no DNS names (an IP certificate, say) leaves nginx with no
// server_name; refusing beats generating a config that cannot match anything.
func TestBuildEdgeConfigRefusesWithoutAnyServerName(t *testing.T) {
	svc := edgeTestDB(t)
	seedEdge(t, svc, [2]string{"edgeEnable", "true"}, [2]string{"edgeServerNames", ""},
		[2]string{"edgeCertFile", filepath.Join(t.TempDir(), "absent.pem")},
		[2]string{"edgeKeyFile", filepath.Join(t.TempDir(), "absent.key")})

	if _, err := svc.BuildEdgeConfig(); err == nil {
		t.Fatal("a perimeter with no server name was accepted, so nginx would serve the wrong certificate or none")
	}
}

// With no uploaded site the perimeter still needs a root to serve, or a probe
// gets an nginx 404 that looks nothing like a website.
func TestBuildEdgeConfigFallsBackToAPlaceholderSite(t *testing.T) {
	svc := edgeTestDB(t)
	dir := t.TempDir()
	certPath, keyPath := writeEdgeCert(t, dir, "site.example")
	seedEdge(t, svc, [2]string{"edgeEnable", "true"}, [2]string{"edgeCertFile", certPath},
		[2]string{"edgeKeyFile", keyPath}, [2]string{"frontSiteId", "0"})

	cfg, err := svc.BuildEdgeConfig()
	if err != nil {
		t.Fatalf("BuildEdgeConfig: %v", err)
	}
	index := filepath.Join(cfg.DecoyRoot, cfg.DecoyIndex)
	data, readErr := os.ReadFile(index)
	if readErr != nil {
		t.Fatalf("placeholder site has no entry page: %v", readErr)
	}
	if len(data) == 0 {
		t.Fatal("placeholder entry page is empty")
	}
}

// The subscription is only routed when it is actually enabled; a stale path in
// the config would expose a listener that should not be reachable.
func TestBuildEdgeConfigRoutesSubscriptionOnlyWhenEnabled(t *testing.T) {
	svc := edgeTestDB(t)
	dir := t.TempDir()
	certPath, keyPath := writeEdgeCert(t, dir, "site.example")

	base := [][2]string{
		{"edgeEnable", "true"},
		{"edgeCertFile", certPath},
		{"edgeKeyFile", keyPath},
		{"subPath", "/links7f3/"},
		{"subJsonPath", "/json9a1/"},
		{"subClashPath", "/clash2b8/"},
		{"subPort", "2096"},
	}
	seedEdge(t, svc, append(base, [2]string{"subEnable", "false"})...)
	cfg, err := svc.BuildEdgeConfig()
	if err != nil {
		t.Fatalf("BuildEdgeConfig: %v", err)
	}
	if len(cfg.SubPrefixes) != 0 {
		t.Fatalf("subscription paths %v were routed while the server is disabled", cfg.SubPrefixes)
	}

	seedEdge(t, svc, [2]string{"subEnable", "true"})
	cfg, err = svc.BuildEdgeConfig()
	if err != nil {
		t.Fatalf("BuildEdgeConfig: %v", err)
	}
	want := map[string]bool{"/links7f3/": true, "/json9a1/": true, "/clash2b8/": true}
	if len(cfg.SubPrefixes) != len(want) {
		t.Fatalf("subscription paths = %v, want the three configured ones", cfg.SubPrefixes)
	}
	for _, p := range cfg.SubPrefixes {
		if !want[p] {
			t.Fatalf("subscription paths = %v, want the three configured ones", cfg.SubPrefixes)
		}
	}
	if cfg.SubUpstream != "127.0.0.1:2096" {
		t.Fatalf("subscription upstream = %q, want loopback", cfg.SubUpstream)
	}
}

// A site's own 404 must be what an unknown path returns; the generated fallback
// only exists because nginx must never answer with its own error page.
func TestWriteNotFoundPagePrefersTheSitesOwn(t *testing.T) {
	site := t.TempDir()
	target := filepath.Join(t.TempDir(), "404.html")
	if err := os.WriteFile(filepath.Join(site, "404.html"), []byte("site-owned-404"), 0o644); err != nil {
		t.Fatalf("write site 404: %v", err)
	}
	if err := writeNotFoundPage(site, target); err != nil {
		t.Fatalf("writeNotFoundPage: %v", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "site-owned-404" {
		t.Fatalf("404 page = %q, want the site's own", data)
	}

	empty := t.TempDir()
	if err := writeNotFoundPage(empty, target); err != nil {
		t.Fatalf("writeNotFoundPage: %v", err)
	}
	data, _ = os.ReadFile(target)
	if string(data) == "site-owned-404" || len(data) == 0 {
		t.Fatalf("404 page = %q, want a generated fallback", data)
	}
}
