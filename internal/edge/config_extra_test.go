package edge

import (
	"strings"
	"testing"
)

// The generated config is what a scanner sees. Anything that betrays nginx's
// version, directory listing or default error page defeats the whole point of
// putting a real website behind REALITY.
func TestRenderKeepsEveryPathInsideTheSite(t *testing.T) {
	out, err := Render(baseConfig())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// The catch-all must fall through to the site's own 404, not to nginx's.
	if !strings.Contains(out, "try_files $uri $uri/ =404;") {
		t.Fatalf("the site root has no 404 fallthrough, so unknown paths answer 200 or nginx's page:\n%s", out)
	}
	if !strings.Contains(out, "error_page 404 /404.html;") {
		t.Fatalf("404s are not remapped to the site's own page:\n%s", out)
	}
	// A probe path must never reach the panel: only the secret prefix may.
	if strings.Contains(out, "location ~") {
		t.Fatalf("a regex location lets a crafted path bypass the prefix routing:\n%s", out)
	}
}

// The panel's WebSocket and the subscription's plain proxy blocks must stay
// distinct: giving the subscription the upgrade headers is harmless, but giving
// the panel no upgrade breaks every live update.
func TestRenderPanelAndSubscriptionBlocksDiffer(t *testing.T) {
	out, err := Render(baseConfig())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	panelAt := strings.Index(out, "location /adm9x/ {")
	subAt := strings.Index(out, "location /links7f3/ {")
	if panelAt < 0 || subAt < 0 || subAt < panelAt {
		t.Fatalf("panel and subscription blocks are missing or out of order:\n%s", out)
	}
	between := out[panelAt:subAt]
	if !strings.Contains(between, "Upgrade $http_upgrade") {
		t.Fatalf("the panel block lost its WebSocket upgrade:\n%s", between)
	}
	if !strings.Contains(between, "proxy_read_timeout") {
		t.Fatalf("the panel block has no long read timeout, so a slow request is cut:\n%s", between)
	}
}

// nginx refuses to start when two server blocks claim the same listen address, so
// a second look at the generated text is cheap insurance.
func TestRenderEmitsOneServerBlock(t *testing.T) {
	out, err := Render(baseConfig())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := strings.Count(out, "    server {"); got != 1 {
		t.Fatalf("generated config has %d server blocks, want exactly one:\n%s", got, out)
	}
	if got := strings.Count(out, "listen "); got != 1 {
		t.Fatalf("generated config has %d listen directives, want exactly one:\n%s", got, out)
	}
}
