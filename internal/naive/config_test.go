package naive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestMain(m *testing.M) {
	if os.Getenv("NAIVE_FAKE_CADDY_CHILD") == "1" {
		file, err := os.OpenFile(os.Getenv("NAIVE_FAKE_CADDY_PIDFILE"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintln(file, os.Getpid())
			_ = file.Close()
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(m.Run())
}

func TestInstanceFromInboundUsesTLSAndFiltersInactiveClients(t *testing.T) {
	past := time.Now().Add(-time.Minute).UnixMilli()
	inbound := &model.Inbound{
		Id:       7,
		Tag:      "naive-443",
		Listen:   "127.0.0.1",
		Port:     443,
		Protocol: model.Naive,
		Settings: `{"domain":"proxy.example.com","clients":[` +
			`{"email":"active","password":"one","enable":true},` +
			`{"email":"disabled","password":"two","enable":false},` +
			`{"email":"expired","password":"three","enable":true,"expiryTime":` + fmt.Sprint(past) + `},` +
			`{"email":"delayed","password":"four","enable":true,"expiryTime":-86400000},` +
			`{"email":"missing-password","enable":true}]}`,
		StreamSettings: `{"network":"tcp","security":"tls","tlsSettings":{"certificates":[` +
			`{"usage":"verify","certificate":["CA"]},` +
			`{"usage":"encipherment","certificate":["CERTIFICATE LINE 1","CERTIFICATE LINE 2"],"key":["PRIVATE KEY"]}]}}`,
	}

	inst, ok := InstanceFromInbound(inbound)
	if !ok {
		t.Fatal("expected a usable instance")
	}
	if inst.Id != 7 || inst.Port != 443 || inst.Listen != "127.0.0.1" {
		t.Fatalf("inbound fields not preserved: %+v", inst)
	}
	if inst.Domain != "proxy.example.com" || inst.FallbackRoot != "" {
		t.Fatalf("settings not parsed: %+v", inst)
	}
	if inst.CertificatePEM != "CERTIFICATE LINE 1\nCERTIFICATE LINE 2" || inst.KeyPEM != "PRIVATE KEY" {
		t.Fatalf("inline TLS not selected: cert=%q key=%q", inst.CertificatePEM, inst.KeyPEM)
	}
	if !inst.ProbeResistance || !inst.HideIP || !inst.HideVia || !inst.Encode {
		t.Fatalf("privacy defaults not enabled: %+v", inst)
	}
	want := []Client{{Email: "active", Password: "one"}, {Email: "delayed", Password: "four"}}
	if len(inst.Clients) != len(want) {
		t.Fatalf("active clients = %+v, want %+v", inst.Clients, want)
	}
	for i := range want {
		if inst.Clients[i] != want[i] {
			t.Fatalf("client %d = %+v, want %+v", i, inst.Clients[i], want[i])
		}
	}
}

func TestInstanceFromInboundUsesFileTLSAndExplicitOptions(t *testing.T) {
	inbound := &model.Inbound{
		Id:             3,
		Protocol:       model.Naive,
		Settings:       `{"domain":"naive.example.net","fallbackRoot":"/srv/site","probeResistance":false,"hideIp":false,"hideVia":true,"encode":false,"clients":[{"email":"alice","password":"secret","enable":true}]}`,
		StreamSettings: `{"security":"tls","tlsSettings":{"certificates":[{"usage":"encipherment","certificateFile":"/etc/cert.pem","keyFile":"/etc/key.pem"}]}}`,
	}
	inst, ok := InstanceFromInbound(inbound)
	if !ok {
		t.Fatal("expected a usable instance")
	}
	if inst.CertificateFile != "/etc/cert.pem" || inst.KeyFile != "/etc/key.pem" {
		t.Fatalf("file TLS paths = %q, %q", inst.CertificateFile, inst.KeyFile)
	}
	if inst.FallbackRoot != "/srv/site" || inst.ProbeResistance || inst.HideIP || !inst.HideVia || inst.Encode {
		t.Fatalf("explicit options not preserved: %+v", inst)
	}
}

func TestGenerateConfig(t *testing.T) {
	config, err := GenerateConfig(Instance{
		Listen:          "127.0.0.1",
		Port:            8443,
		Domain:          "proxy.example.com",
		CertificateFile: `/etc/x-ui/naive/cert.pem`,
		KeyFile:         `/etc/x-ui/naive/key.pem`,
		FallbackRoot:    `/srv/site files`,
		ProbeResistance: true,
		HideIP:          true,
		HideVia:         true,
		Encode:          true,
		Clients: []Client{
			{Email: `user name`, Password: `p\"ass`},
		},
	})
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	text := string(config)
	for _, want := range []string{
		"order forward_proxy before file_server",
		":8443 {",
		`bind "127.0.0.1"`,
		`tls "/etc/x-ui/naive/cert.pem" "/etc/x-ui/naive/key.pem"`,
		"encode gzip",
		`basic_auth "user name" "p\\\"ass"`,
		"hide_ip",
		"hide_via",
		`probe_resistance "proxy.example.com"`,
		`root * "/srv/site files"`,
		"file_server",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Caddyfile missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "order forward_proxy") > strings.Index(text, ":8443 {") {
		t.Fatalf("directive order must be configured before the site block:\n%s", text)
	}
}

func TestGenerateConfigRejectsInvalidValues(t *testing.T) {
	base := Instance{
		Port:            443,
		Domain:          "proxy.example.com",
		CertificateFile: "cert.pem",
		KeyFile:         "key.pem",
		Clients:         []Client{{Email: "alice", Password: "secret"}},
	}
	cases := []struct {
		name  string
		edit  func(*Instance)
		match string
	}{
		{"bad port", func(i *Instance) { i.Port = 0 }, "invalid port"},
		{"bad host", func(i *Instance) { i.Domain = "proxy.example.com:443" }, "invalid domain"},
		{"missing certificate", func(i *Instance) { i.CertificateFile = "" }, "TLS certificate and key are required"},
		{"inline not materialized", func(i *Instance) { i.CertificateFile = ""; i.CertificatePEM = "CERT" }, "must be materialized"},
		{"missing clients", func(i *Instance) { i.Clients = nil }, "at least one client"},
		{"empty password", func(i *Instance) { i.Clients[0].Password = "" }, "client credentials"},
		{"colon in username", func(i *Instance) { i.Clients[0].Email = "a:b" }, "client credentials"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst := base
			inst.Clients = append([]Client(nil), base.Clients...)
			tc.edit(&inst)
			_, err := GenerateConfig(inst)
			if err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("GenerateConfig error = %v, want substring %q", err, tc.match)
			}
		})
	}
}

func TestFingerprintIncludesCertificateContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	inst := Instance{CertificateFile: path, Clients: []Client{{Email: "a", Password: "b"}}}
	first := inst.Fingerprint()
	if err := os.WriteFile(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if second := inst.Fingerprint(); second == first {
		t.Fatal("certificate content change must change the runtime fingerprint")
	}
}

func TestWriteTLSFilesSupportsMixedSourcesAndRemovesFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", dir)
	certPath, keyPath, err := WriteTLSFiles(12, "inline cert", "")
	if err != nil {
		t.Fatal(err)
	}
	if certPath != CertificatePathForID(12) || keyPath != "" {
		t.Fatalf("paths = %q, %q", certPath, keyPath)
	}
	data, err := os.ReadFile(certPath)
	if err != nil || string(data) != "inline cert" {
		t.Fatalf("certificate = %q, err=%v", data, err)
	}
	if err := RemoveTLSFiles(12); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(certPath); !os.IsNotExist(err) {
		t.Fatalf("certificate file remains: %v", err)
	}
}

func TestManagerReconcileAndStopAll(t *testing.T) {
	binDir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, GetBinaryName()), binary, 0o700); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(binDir, "caddy-pids.txt")
	t.Setenv("XUI_BIN_FOLDER", binDir)
	t.Setenv("NAIVE_FAKE_CADDY_CHILD", "1")
	t.Setenv("NAIVE_FAKE_CADDY_PIDFILE", pidFile)

	manager := &Manager{procs: make(map[int]*managed)}
	inst := Instance{
		Id:              23,
		Tag:             "naive-23",
		Port:            8443,
		Domain:          "proxy.example.com",
		CertificatePEM:  "inline cert",
		KeyPEM:          "inline key",
		ProbeResistance: true,
		Clients:         []Client{{Email: "alice", Password: "one"}},
	}
	t.Cleanup(manager.StopAll)

	if err := manager.Ensure(inst); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	waitForSpawns(t, pidFile, 1)
	if err := manager.Ensure(inst); err != nil {
		t.Fatalf("Ensure unchanged instance: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := countSpawns(t, pidFile); got != 1 {
		t.Fatalf("unchanged instance spawned %d processes, want 1", got)
	}

	inst.Clients[0].Password = "two"
	if err := manager.Ensure(inst); err != nil {
		t.Fatalf("Ensure changed instance: %v", err)
	}
	waitForSpawns(t, pidFile, 2)
	manager.Reconcile([]Instance{inst})
	if got := countSpawns(t, pidFile); got != 2 {
		t.Fatalf("reconciling unchanged instance spawned %d processes, want 2", got)
	}
	manager.Reconcile(nil)
	if len(manager.procs) != 0 {
		t.Fatalf("reconcile retained %d processes after removing all desired instances", len(manager.procs))
	}
	for _, path := range []string{ConfigPathForID(inst.Id), CertificatePathForID(inst.Id), KeyPathForID(inst.Id)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("managed file %q remains after StopAll: %v", path, err)
		}
	}
}

func waitForSpawns(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if countSpawns(t, path) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d Caddy starts; got %d", want, countSpawns(t, path))
}

func countSpawns(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	return len(strings.Fields(string(data)))
}
