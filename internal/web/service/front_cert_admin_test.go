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
	"reflect"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// writeAdminCertPair writes a self-signed pair with a chosen expiry, so a test
// can tell the served certificate from a rotated one by NotAfter alone.
func writeAdminCertPair(t *testing.T, dir, name string, notAfter time.Time, dns ...string) (string, string) {
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

// certRound puts a fixed second-precision instant on the certificate the way a
// CA does, so a PEM round trip compares equal.
func certRound(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

// summaryFor returns the reported entry covering domain, or nil.
func summaryFor(report FrontCertReloadReport, domain string) *FrontCertSummary {
	for i := range report.Certificates {
		for _, d := range report.Certificates[i].Domains {
			if d == domain {
				return &report.Certificates[i]
			}
		}
	}
	return nil
}

// A pair the front cannot serve must be refused at save time: storing it would
// make the settings page claim TLS is configured while every handshake fails.
func TestSaveFrontCertRejectsUnloadablePairAndStoresNothing(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	missing := t.TempDir()
	svc := &FrontCertService{}

	_, err := svc.SaveFrontCert(&model.FrontCert{
		Domains:  "site.example",
		CertPath: filepath.Join(missing, "absent.crt"),
		KeyPath:  filepath.Join(missing, "absent.key"),
	})
	if err == nil {
		t.Fatal("a certificate pair that does not load was accepted, so the front would serve nothing after a restart")
	}

	rows, listErr := svc.ListFrontCerts()
	if listErr != nil {
		t.Fatalf("ListFrontCerts: %v", listErr)
	}
	if len(rows) != 0 {
		t.Fatalf("a rejected certificate was stored anyway: %+v", rows)
	}
	if report := svc.ReloadReport(); report.HasCertificate {
		t.Fatal("the front reports a certificate after a rejected save, so the reload answer lies about TLS being up")
	}
}

// The stored row must carry the leaf's expiry, because that is the only value
// the API and the UI use to warn before a certificate expires.
func TestSaveFrontCertRecordsLeafNotAfter(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	dir := t.TempDir()
	want := certRound(time.Now().Add(72 * time.Hour))
	certPath, keyPath := writeAdminCertPair(t, dir, "panel", want, "panel.example", "www.example")
	svc := &FrontCertService{}

	stored, err := svc.SaveFrontCert(&model.FrontCert{Domains: "Panel.Example", CertPath: certPath, KeyPath: keyPath})
	if err != nil {
		t.Fatalf("SaveFrontCert: %v", err)
	}
	if stored.Id == 0 {
		t.Fatal("SaveFrontCert returned a row without an id, so the UI cannot edit or delete it")
	}
	if stored.NotAfter == nil || !stored.NotAfter.Equal(want) {
		t.Fatalf("stored NotAfter = %v, want the leaf expiry %v", stored.NotAfter, want)
	}

	reloaded, err := svc.GetFrontCert(stored.Id)
	if err != nil {
		t.Fatalf("GetFrontCert: %v", err)
	}
	if reloaded.NotAfter == nil || !reloaded.NotAfter.Equal(want) {
		t.Fatalf("persisted NotAfter = %v, want the leaf expiry %v", reloaded.NotAfter, want)
	}
}

// An edit carries the row id. Re-creating instead of updating would leave the
// old, wrong pair in the table and the front serving whichever came first.
func TestSaveFrontCertUpdatesExistingRowInPlace(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	dir := t.TempDir()
	stale := certRound(time.Now().Add(24 * time.Hour))
	fresh := certRound(time.Now().Add(720 * time.Hour))
	oldCert, oldKey := writeAdminCertPair(t, dir, "old", stale, "old.example")
	newCert, newKey := writeAdminCertPair(t, dir, "new", fresh, "new.example")
	svc := &FrontCertService{}

	created, err := svc.SaveFrontCert(&model.FrontCert{Domains: "old.example", CertPath: oldCert, KeyPath: oldKey})
	if err != nil {
		t.Fatalf("SaveFrontCert(create): %v", err)
	}

	updated, err := svc.SaveFrontCert(&model.FrontCert{
		Id:       created.Id,
		Domains:  "new.example",
		CertPath: newCert,
		KeyPath:  newKey,
		Issuer:   "acme",
	})
	if err != nil {
		t.Fatalf("SaveFrontCert(update): %v", err)
	}
	if updated.Id != created.Id {
		t.Fatalf("update returned id %d, want the edited row %d", updated.Id, created.Id)
	}
	if updated.NotAfter == nil || !updated.NotAfter.Equal(fresh) {
		t.Fatalf("updated NotAfter = %v, want the new leaf expiry %v", updated.NotAfter, fresh)
	}

	rows, err := svc.ListFrontCerts()
	if err != nil {
		t.Fatalf("ListFrontCerts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("after an edit there are %d rows, want the single updated one: %+v", len(rows), rows)
	}
	if rows[0].Domains != "new.example" || rows[0].Issuer != "acme" {
		t.Fatalf("edited row = %+v, want the new domains and issuer", rows[0])
	}
	if cert := svc.CertificateFor("old.example"); cert != nil {
		t.Fatal("the replaced domain still gets a certificate after the edit")
	}
	if cert := svc.CertificateFor("new.example"); cert == nil {
		t.Fatal("the edited row's domain gets no certificate, so the edit never reached the front")
	}
}

// Rotation is a file replacement: the reload report has to show the new expiry
// without a panel restart and without re-saving the row.
func TestReloadReportPicksUpRotatedFileExpiry(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	dir := t.TempDir()
	first := certRound(time.Now().Add(24 * time.Hour))
	second := certRound(time.Now().Add(240 * time.Hour))
	certPath, keyPath := writeAdminCertPair(t, dir, "rotating", first, "panel.example")
	svc := &FrontCertService{}
	if _, err := svc.SaveFrontCert(&model.FrontCert{Domains: "panel.example", CertPath: certPath, KeyPath: keyPath}); err != nil {
		t.Fatalf("SaveFrontCert: %v", err)
	}

	before := svc.ReloadReport()
	entry := summaryFor(before, "panel.example")
	if entry == nil || entry.NotAfter == nil || !entry.NotAfter.Equal(first) {
		t.Fatalf("first reload reported %+v, want the seeded expiry %v", entry, first)
	}

	rotatedCert, _ := writeAdminCertPair(t, dir, "rotating", second, "panel.example")
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(rotatedCert, later, later); err != nil {
		t.Fatalf("touch rotated cert: %v", err)
	}
	after := svc.ReloadReport()
	rotated := summaryFor(after, "panel.example")
	if rotated == nil || rotated.NotAfter == nil || !rotated.NotAfter.Equal(second) {
		t.Fatalf("reload after replacing the file reported %+v, want the new expiry %v — the hot-reload path is a no-op", rotated, second)
	}
	if after.Default == nil || after.Default.NotAfter == nil || !after.Default.NotAfter.Equal(second) {
		t.Fatalf("the no-SNI default still reports the old expiry: %+v", after.Default)
	}
}

// An operator must never read an empty answer as "TLS is set up": a front with
// nothing loadable has to say so in words, not only by listing no rows.
func TestReloadReportWarnsWhenNoCertificateConfigured(t *testing.T) {
	t.Run("no rows", func(t *testing.T) {
		dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
		svc := &FrontCertService{}
		report := svc.ReloadReport()
		if report.HasCertificate {
			t.Fatal("an empty front reported a certificate")
		}
		if report.Warning == "" {
			t.Fatal("a front with no certificate reported no warning, so the UI would show TLS as configured")
		}
		if report.Default != nil {
			t.Fatalf("a front with no certificate reported a default certificate: %+v", report.Default)
		}
	})

	t.Run("row whose files are gone", func(t *testing.T) {
		dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
		dir := t.TempDir()
		certPath, keyPath := writeAdminCertPair(t, dir, "vanished", certRound(time.Now().Add(24*time.Hour)), "gone.example")
		svc := &FrontCertService{}
		if _, err := svc.SaveFrontCert(&model.FrontCert{Domains: "gone.example", CertPath: certPath, KeyPath: keyPath}); err != nil {
			t.Fatalf("SaveFrontCert: %v", err)
		}
		if err := os.Remove(certPath); err != nil {
			t.Fatalf("remove cert: %v", err)
		}
		if err := os.Remove(keyPath); err != nil {
			t.Fatalf("remove key: %v", err)
		}

		report := svc.ReloadReport()
		if report.HasCertificate {
			t.Fatal("a row whose files are gone still reported a certificate, so the operator would think TLS is up")
		}
		if report.Warning == "" {
			t.Fatal("a front whose only row cannot load reported no warning")
		}
		if entry := summaryFor(report, "gone.example"); entry != nil && entry.Loaded {
			t.Fatalf("the unloadable row was reported as loaded: %+v", entry)
		}
	})
}

// The reload answer has to describe the certificate a client that sends no SNI
// receives; without it a probe with no server name looks like a healthy front.
func TestReloadReportDescribesDefaultCertificate(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	dir := t.TempDir()
	want := certRound(time.Now().Add(48 * time.Hour))
	certPath, keyPath := writeAdminCertPair(t, dir, "default", want, "panel.example", "www.example")
	svc := &FrontCertService{}
	if _, err := svc.SaveFrontCert(&model.FrontCert{Domains: "panel.example,www.example", CertPath: certPath, KeyPath: keyPath}); err != nil {
		t.Fatalf("SaveFrontCert: %v", err)
	}

	report := svc.ReloadReport()
	if !report.HasCertificate {
		t.Fatal("a loaded certificate was not reported")
	}
	if report.Default == nil {
		t.Fatal("the reload answer omitted the certificate presented to clients that send no SNI")
	}
	if got := report.Default.Domains; !reflect.DeepEqual(got, []string{"panel.example", "www.example"}) {
		t.Fatalf("default certificate domains = %v, want the leaf names", got)
	}
	if report.Default.NotAfter == nil || !report.Default.NotAfter.Equal(want) {
		t.Fatalf("default certificate NotAfter = %v, want %v", report.Default.NotAfter, want)
	}
}

// Deleting a row must take the certificate out of service immediately, not at
// the next cache expiry, or a revoked certificate keeps answering handshakes.
func TestDeleteFrontCertStopsServingDomain(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	dir := t.TempDir()
	notAfter := certRound(time.Now().Add(24 * time.Hour))
	firstPath, firstKey := writeAdminCertPair(t, dir, "first", notAfter, "a.example")
	secondPath, secondKey := writeAdminCertPair(t, dir, "second", notAfter, "b.example")
	svc := &FrontCertService{}
	first, err := svc.SaveFrontCert(&model.FrontCert{Domains: "a.example", CertPath: firstPath, KeyPath: firstKey})
	if err != nil {
		t.Fatalf("SaveFrontCert(a): %v", err)
	}
	second, err := svc.SaveFrontCert(&model.FrontCert{Domains: "b.example", CertPath: secondPath, KeyPath: secondKey})
	if err != nil {
		t.Fatalf("SaveFrontCert(b): %v", err)
	}
	if before := svc.ReloadReport(); len(before.Certificates) != 2 {
		t.Fatalf("reload reported %d certificates, want 2", len(before.Certificates))
	}

	if err := svc.DeleteFrontCert(first.Id); err != nil {
		t.Fatalf("DeleteFrontCert: %v", err)
	}
	after := svc.ReloadReport()
	if summaryFor(after, "a.example") != nil {
		t.Fatal("the deleted certificate is still reported after a reload")
	}
	if cert := svc.CertificateFor("a.example"); cert != nil {
		t.Fatal("the deleted certificate is still presented, so a revoked certificate keeps answering")
	}
	if len(after.Certificates) != 1 || after.Certificates[0].Id != second.Id {
		t.Fatalf("reload after delete reported %+v, want only the surviving row", after.Certificates)
	}
	if rows, err := svc.ListFrontCerts(); err != nil || len(rows) != 1 {
		t.Fatalf("ListFrontCerts after delete = %+v (err %v), want one row", rows, err)
	}

	if err := svc.DeleteFrontCert(second.Id); err != nil {
		t.Fatalf("DeleteFrontCert(b): %v", err)
	}
	empty := svc.ReloadReport()
	if empty.HasCertificate || empty.Warning == "" {
		t.Fatalf("deleting the last certificate left HasCertificate=%v warning=%q", empty.HasCertificate, empty.Warning)
	}
}
