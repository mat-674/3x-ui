package edge

import (
	"strings"
	"testing"
)

// Keeping 80 open is only worth it if ACME validation lands there and everything
// else behaves like a real site: a redirect, not a second copy of the panel.
func TestRenderServesACMEChellengeOnThePlaintextPort(t *testing.T) {
	cfg := baseConfig()
	cfg.HTTPPort = 80
	cfg.AcmeRoot = "/etc/x-ui/edge/acme"

	out, err := Render(cfg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "listen 80;") {
		t.Fatalf("no plaintext listener in the config:\n%s", out)
	}
	if !strings.Contains(out, "location ^~ /.well-known/acme-challenge/ {") {
		t.Fatalf("the challenge path is not served:\n%s", out)
	}
	if !strings.Contains(out, "root /etc/x-ui/edge/acme;") {
		t.Fatalf("the challenge webroot is missing:\n%s", out)
	}
	if !strings.Contains(out, "return 301 https://$host$request_uri;") {
		t.Fatalf("a plain request is not redirected to HTTPS:\n%s", out)
	}
}

// The challenge location must win over the redirect: with only the redirect a
// renewal would need the firewall opened, which is the whole thing this avoids.
func TestRenderPutsTheChallengeBeforeTheRedirect(t *testing.T) {
	cfg := baseConfig()
	cfg.HTTPPort = 80
	cfg.AcmeRoot = "/acme"

	out, err := Render(cfg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// The HTTP block is the second server block; scope the search to it so the
	// TLS block's own locations cannot satisfy the assertion.
	httpAt := strings.LastIndex(out, "    server {")
	if httpAt < 0 {
		t.Fatalf("no second server block:\n%s", out)
	}
	block := out[httpAt:]
	challenge := strings.Index(block, "/.well-known/acme-challenge/")
	redirect := strings.Index(block, "return 301")
	if challenge < 0 || redirect < 0 {
		t.Fatalf("the HTTP block is missing one of its locations:\n%s", block)
	}
	if challenge > redirect {
		t.Fatalf("the redirect is emitted before the challenge location:\n%s", block)
	}
	if strings.Contains(block, "proxy_pass") {
		t.Fatalf("the plaintext block proxies something, so it is not just a site:\n%s", block)
	}
	if !strings.Contains(block, "server_tokens off;") {
		t.Fatalf("the plaintext block advertises the nginx version:\n%s", block)
	}
}

// Port 80 is optional: a host that does not expose it must get exactly the
// previous config, with no plaintext listener at all.
func TestRenderWithoutAPlaintextPort(t *testing.T) {
	out, err := Render(baseConfig())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "listen 80") || strings.Contains(out, "acme-challenge") {
		t.Fatalf("a plaintext block was emitted although the feature is off:\n%s", out)
	}
	if got := strings.Count(out, "    server {"); got != 1 {
		t.Fatalf("got %d server blocks, want exactly the TLS one", got)
	}
}

// An unbindable address is a config nginx accepts but cannot start, and nginx
// refusing to start takes the whole perimeter down; a lone port or 0.0.0.0 has
// to render in the form nginx expects for a public listener.
func TestRenderHTTPListenForms(t *testing.T) {
	cases := map[string]string{
		"":          "listen 80;",
		"0.0.0.0":   "listen 80;",
		"127.0.0.1": "listen 127.0.0.1:80;",
	}
	for listen, want := range cases {
		t.Run(listen, func(t *testing.T) {
			cfg := baseConfig()
			cfg.HTTPPort = 80
			cfg.HTTPListen = listen
			cfg.AcmeRoot = "/acme"
			out, err := Render(cfg)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if !strings.Contains(out, want) {
				t.Fatalf("listen %q rendered without %q:\n%s", listen, want, out)
			}
		})
	}
}

// Two listeners on one port would make nginx refuse the config entirely.
func TestRenderRejectsTheSamePortTwice(t *testing.T) {
	cfg := baseConfig()
	cfg.HTTPPort = cfg.Port
	cfg.AcmeRoot = "/acme"
	if _, err := Render(cfg); err == nil {
		t.Fatal("the plaintext and TLS ports may not be the same")
	}
}

// The challenge directory is the only way a webroot renewal can work, so a
// missing webroot has to be refused rather than rendered as a broken location.
func TestRenderRequiresAnAcmeRootWhen80IsServed(t *testing.T) {
	cfg := baseConfig()
	cfg.HTTPPort = 80
	cfg.AcmeRoot = ""
	if _, err := Render(cfg); err == nil {
		t.Fatal("a plaintext block without a challenge webroot was accepted")
	}
}
