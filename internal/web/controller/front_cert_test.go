package controller

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// writeFrontCertPair writes a self-signed pair with a chosen expiry, so a test
// can tell a freshly loaded certificate from an older one by NotAfter alone.
func writeFrontCertPair(t *testing.T, dir, name string, notAfter time.Time, dns ...string) (string, string) {
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
		Subject:               pkix.Name{CommonName: dns[0]},
		DNSNames:              dns,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter,
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
	certPath := filepath.Join(dir, name+".crt")
	keyPath := filepath.Join(dir, name+".key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

type frontCertEnvelope struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

func newFrontCertTestEngine(t *testing.T) (*gin.Engine, *service.FrontCertService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	certs := &service.FrontCertService{}
	engine := gin.New()
	NewFrontCertController(engine.Group("/panel/api"), certs)
	return engine, certs
}

func doFrontCert(t *testing.T, engine *gin.Engine, method, path string, body any) frontCertEnvelope {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s: status %d, body=%s", method, path, w.Code, w.Body.String())
	}
	var env frontCertEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("%s %s: decode envelope: %v body=%s", method, path, err, w.Body.String())
	}
	return env
}

// A JSON body is the documented contract; binding a form instead would make
// every save of a certificate fail with no visible cause beyond a 200/{"success":false}.
func TestFrontCertControllerSaveUsesJSONBody(t *testing.T) {
	engine, _ := newFrontCertTestEngine(t)
	dir := t.TempDir()
	want := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	certPath, keyPath := writeFrontCertPair(t, dir, "json", want, "json.example")

	env := doFrontCert(t, engine, http.MethodPost, "/panel/api/front/certs", map[string]any{
		"domains":  "JSON.Example",
		"certPath": certPath,
		"keyPath":  keyPath,
		"issuer":   "file",
	})
	if !env.Success {
		t.Fatalf("save over JSON body failed: %s", env.Msg)
	}
	var row model.FrontCert
	if err := json.Unmarshal(env.Obj, &row); err != nil {
		t.Fatalf("decode saved row: %v", err)
	}
	if row.Id == 0 {
		t.Fatal("the saved row came back without an id, so the UI cannot address it")
	}
	if row.Domains != "json.example" {
		t.Fatalf("stored domains = %q, want the normalised single name", row.Domains)
	}
	if row.NotAfter == nil || !row.NotAfter.UTC().Truncate(time.Second).Equal(want) {
		t.Fatalf("saved row NotAfter = %v, want %v", row.NotAfter, want)
	}
}

// An empty front must answer an empty array, not null: the settings page counts
// rows to decide whether to show the "no certificate" warning.
func TestFrontCertControllerListAnswersArrayWhenEmpty(t *testing.T) {
	engine, _ := newFrontCertTestEngine(t)
	env := doFrontCert(t, engine, http.MethodGet, "/panel/api/front/certs", nil)
	if !env.Success {
		t.Fatalf("list failed: %s", env.Msg)
	}
	if got := string(env.Obj); got != "[]" {
		t.Fatalf("empty list answered %s, want [] so the UI never reads null", got)
	}
}

// A rejected pair must not be stored: otherwise the row survives, the front
// fails to load it on every reload, and the operator keeps a broken TLS entry.
func TestFrontCertControllerRejectsUnloadablePairAndStoresNothing(t *testing.T) {
	engine, _ := newFrontCertTestEngine(t)
	missing := t.TempDir()

	env := doFrontCert(t, engine, http.MethodPost, "/panel/api/front/certs", map[string]any{
		"domains":  "broken.example",
		"certPath": filepath.Join(missing, "absent.crt"),
		"keyPath":  filepath.Join(missing, "absent.key"),
	})
	if env.Success {
		t.Fatal("a pair that does not load was accepted, so the front would serve nothing after a restart")
	}

	list := doFrontCert(t, engine, http.MethodGet, "/panel/api/front/certs", nil)
	if got := string(list.Obj); got != "[]" {
		t.Fatalf("a rejected save left rows behind: %s", got)
	}
}

// The reload answer is the operator's only view of what the front holds, so an
// empty front has to say so in words, not just with an empty list.
func TestFrontCertControllerReloadWarnsWhenNoCertificate(t *testing.T) {
	engine, _ := newFrontCertTestEngine(t)
	env := doFrontCert(t, engine, http.MethodPost, "/panel/api/front/certs/reload", nil)
	if !env.Success {
		t.Fatalf("reload failed: %s", env.Msg)
	}
	var payload struct {
		HasCertificate bool              `json:"hasCertificate"`
		Warning        string            `json:"warning"`
		Certificates   []json.RawMessage `json:"certificates"`
		Default        json.RawMessage   `json:"default"`
	}
	if err := json.Unmarshal(env.Obj, &payload); err != nil {
		t.Fatalf("decode reload payload: %v body=%s", err, env.Obj)
	}
	if payload.HasCertificate {
		t.Fatal("reload reported a certificate on an empty front")
	}
	if payload.Warning == "" {
		t.Fatal("reload did not say the front has no certificate, so an operator can believe TLS is set up")
	}
	if payload.Certificates == nil {
		t.Fatal("reload answered null certificates, want []")
	}
	if len(payload.Certificates) != 0 {
		t.Fatalf("reload listed %d certificates on an empty front", len(payload.Certificates))
	}
	if string(payload.Default) != "null" {
		t.Fatalf("reload named a default certificate on an empty front: %s", payload.Default)
	}
}

// The loaded certificate must come back with its domains and its expiry: the
// settings page flags expiry from this payload and nothing else.
func TestFrontCertControllerReloadDescribesLoadedCertificate(t *testing.T) {
	engine, _ := newFrontCertTestEngine(t)
	dir := t.TempDir()
	want := time.Now().Add(96 * time.Hour).UTC().Truncate(time.Second)
	certPath, keyPath := writeFrontCertPair(t, dir, "loaded", want, "panel.example", "www.example")

	saved := doFrontCert(t, engine, http.MethodPost, "/panel/api/front/certs", map[string]any{
		"domains":  "panel.example,www.example",
		"certPath": certPath,
		"keyPath":  keyPath,
	})
	if !saved.Success {
		t.Fatalf("save failed: %s", saved.Msg)
	}

	env := doFrontCert(t, engine, http.MethodPost, "/panel/api/front/certs/reload", nil)
	if !env.Success {
		t.Fatalf("reload failed: %s", env.Msg)
	}
	var payload struct {
		HasCertificate bool   `json:"hasCertificate"`
		Warning        string `json:"warning"`
		Certificates   []struct {
			Domains  []string `json:"domains"`
			NotAfter string   `json:"notAfter"`
			Loaded   bool     `json:"loaded"`
		} `json:"certificates"`
		Default *struct {
			Domains  []string `json:"domains"`
			NotAfter string   `json:"notAfter"`
		} `json:"default"`
	}
	if err := json.Unmarshal(env.Obj, &payload); err != nil {
		t.Fatalf("decode reload payload: %v body=%s", err, env.Obj)
	}
	if !payload.HasCertificate {
		t.Fatal("reload did not report the saved certificate as present")
	}
	if payload.Warning != "" {
		t.Fatalf("reload warned %q for a healthy front", payload.Warning)
	}
	if len(payload.Certificates) != 1 {
		t.Fatalf("reload listed %d certificates, want 1", len(payload.Certificates))
	}
	entry := payload.Certificates[0]
	if len(entry.Domains) != 2 || entry.Domains[0] != "panel.example" || entry.Domains[1] != "www.example" {
		t.Fatalf("reload reported domains %v, want both names the front keys on", entry.Domains)
	}
	if !entry.Loaded {
		t.Fatal("a loadable certificate was reported as not loaded")
	}
	got, err := time.Parse(time.RFC3339, entry.NotAfter)
	if err != nil {
		t.Fatalf("reload NotAfter %q is not RFC3339: %v", entry.NotAfter, err)
	}
	if !got.UTC().Equal(want) {
		t.Fatalf("reload NotAfter = %v, want %v", got.UTC(), want)
	}
	if payload.Default == nil {
		t.Fatal("reload omitted the certificate presented to clients that send no SNI")
	}
	defNotAfter, err := time.Parse(time.RFC3339, payload.Default.NotAfter)
	if err != nil {
		t.Fatalf("default NotAfter %q is not RFC3339: %v", payload.Default.NotAfter, err)
	}
	if !defNotAfter.UTC().Equal(want) {
		t.Fatalf("default NotAfter = %v, want %v", defNotAfter.UTC(), want)
	}
}

// Delete must be reachable and must leave the front with nothing to serve.
func TestFrontCertControllerDeleteRemovesRow(t *testing.T) {
	engine, _ := newFrontCertTestEngine(t)
	dir := t.TempDir()
	notAfter := time.Now().Add(24 * time.Hour)
	certPath, keyPath := writeFrontCertPair(t, dir, "deletable", notAfter, "delete.example")

	saved := doFrontCert(t, engine, http.MethodPost, "/panel/api/front/certs", map[string]any{
		"domains":  "delete.example",
		"certPath": certPath,
		"keyPath":  keyPath,
	})
	if !saved.Success {
		t.Fatalf("save failed: %s", saved.Msg)
	}
	var row model.FrontCert
	if err := json.Unmarshal(saved.Obj, &row); err != nil {
		t.Fatalf("decode saved row: %v", err)
	}

	deleted := doFrontCert(t, engine, http.MethodPost, "/panel/api/front/certs/"+strconv.Itoa(row.Id)+"/delete", nil)
	if !deleted.Success {
		t.Fatalf("delete failed: %s", deleted.Msg)
	}
	var removed model.FrontCert
	if err := json.Unmarshal(deleted.Obj, &removed); err != nil {
		t.Fatalf("decode deleted row: %v", err)
	}
	if removed.Id != row.Id || removed.Domains != "delete.example" {
		t.Fatalf("delete answered %+v, want the row it removed", removed)
	}
	list := doFrontCert(t, engine, http.MethodGet, "/panel/api/front/certs", nil)
	if got := string(list.Obj); got != "[]" {
		t.Fatalf("list after delete = %s, want []", got)
	}

	bad := doFrontCert(t, engine, http.MethodPost, "/panel/api/front/certs/not-a-number/delete", nil)
	if bad.Success {
		t.Fatal("a malformed id was accepted instead of answering the error envelope")
	}
}
