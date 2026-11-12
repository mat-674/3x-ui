package edge

import (
	"strings"
	"testing"
)

func baseConfig() Config {
	return Config{
		Listen:        "127.0.0.1",
		Port:          8443,
		ProxyProtocol: true,
		ServerNames:   []string{"site.example"},
		CertFile:      "/etc/x-ui/certs/fullchain.pem",
		KeyFile:       "/etc/x-ui/certs/key.pem",
		DecoyRoot:     "/etc/x-ui/decoy/site1",
		DecoyIndex:    "index.html",
		NotFoundFile:  "/etc/x-ui/edge/404.html",
		PanelPrefix:   "/adm9x/",
		PanelUpstream: "127.0.0.1:2053",
		SubPrefixes:   []string{"/links7f3/", "/json9a1/"},
		SubUpstream:   "127.0.0.1:2096",
		PidFile:       "/etc/x-ui/edge/nginx.pid",
		LogDir:        "/etc/x-ui/edge/logs",
		TempDir:       "/etc/x-ui/edge/tmp",
	}
}

// A version banner or an nginx error page is exactly what tells a scanner it is
// looking at a panel and not at the site the certificate claims.
func TestRenderNeverLeaksNginxDefaults(t *testing.T) {
	out, err := Render(baseConfig())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"server_tokens off;",
		"error_page 404 /404.html;",
		"location = /404.html {",
		"internal;",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered config is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "autoindex on") {
		t.Fatalf("directory listing enabled:\n%s", out)
	}
}

// Every DPI probe on a random path must land in the decoy's 404, and every path
// the panel owns must reach the panel; a single mis-ordered location breaks one
// or the other.
func TestRenderRoutesPanelSubscriptionAndSite(t *testing.T) {
	out, err := Render(baseConfig())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	panelAt := strings.Index(out, "location /adm9x/ {")
	if panelAt < 0 {
		t.Fatalf("panel location missing:\n%s", out)
	}
	panelBlock := out[panelAt:]
	panelBlock = panelBlock[:strings.Index(panelBlock, "}")]
	for _, want := range []string{
		"proxy_pass http://127.0.0.1:2053;",
		"proxy_set_header Upgrade $http_upgrade;",
		"proxy_set_header Connection \"upgrade\";",
	} {
		if !strings.Contains(panelBlock, want) {
			t.Fatalf("panel block is missing %q (WebSocket would break):\n%s", want, panelBlock)
		}
	}
	for _, prefix := range []string{"/links7f3/", "/json9a1/"} {
		if !strings.Contains(out, "location "+prefix+" {") {
			t.Fatalf("subscription location %q missing:\n%s", prefix, out)
		}
	}
	if !strings.Contains(out, "proxy_pass http://127.0.0.1:2096;") {
		t.Fatalf("subscription upstream missing:\n%s", out)
	}
	if !strings.Contains(out, "try_files $uri $uri/ =404;") {
		t.Fatalf("the site has no 404 fallback, so unknown paths answer 200:\n%s", out)
	}
}

// The panel trusts loopback, so a forwarded address must be replaced rather than
// appended: otherwise anyone can spoof their IP past the login limiter.
func TestRenderOverwritesForwardedFor(t *testing.T) {
	out, err := Render(baseConfig())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "$proxy_add_x_forwarded_for") {
		t.Fatalf("X-Forwarded-For is appended, so a client can spoof its own IP:\n%s", out)
	}
	if !strings.Contains(out, "proxy_set_header X-Forwarded-For $remote_addr;") {
		t.Fatalf("X-Forwarded-For is not set from the resolved client address:\n%s", out)
	}
	if !strings.Contains(out, "real_ip_header proxy_protocol;") {
		t.Fatalf("the PROXY header is not used to recover the client address:\n%s", out)
	}
}

// xver and proxy_protocol must agree. Turning the listener's header off while
// Xray still sends one makes nginx fail every handshake.
func TestRenderProxyProtocolFollowsConfig(t *testing.T) {
	on := baseConfig()
	out, err := Render(on)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "ssl http2 proxy_protocol;") {
		t.Fatalf("listener does not accept the PROXY header Xray sends:\n%s", out)
	}

	off := baseConfig()
	off.ProxyProtocol = false
	out, err = Render(off)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "proxy_protocol") {
		t.Fatalf("listener still expects a PROXY header with proxyProtocol off:\n%s", out)
	}
}

// A prefix ending up in the config unvalidated would let a saved setting inject
// directives into the generated file.
func TestRenderRejectsUnsafePrefix(t *testing.T) {
	cases := map[string]Config{
		"whitespace": {PanelPrefix: "/ad min/"},
		"directive":  {PanelPrefix: "/x; return 200;"},
		"brace":      {PanelPrefix: "/x{}"},
		"relative":   {PanelPrefix: "admx/"},
	}
	for name, patch := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := baseConfig()
			cfg.PanelPrefix = patch.PanelPrefix
			if _, err := Render(cfg); err == nil {
				t.Fatalf("prefix %q was accepted into the generated config", patch.PanelPrefix)
			}
		})
	}
}

func TestRenderRejectsIncompleteConfig(t *testing.T) {
	cases := map[string]func(*Config){
		"no server name": func(c *Config) { c.ServerNames = nil },
		"no cert":        func(c *Config) { c.CertFile = "" },
		"no key":         func(c *Config) { c.KeyFile = "" },
		"no decoy root":  func(c *Config) { c.DecoyRoot = "" },
		"no 404 page":    func(c *Config) { c.NotFoundFile = "" },
		"no upstream":    func(c *Config) { c.PanelUpstream = "" },
		"bad port":       func(c *Config) { c.Port = 0 },
	}
	for name, patch := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := baseConfig()
			patch(&cfg)
			if _, err := Render(cfg); err == nil {
				t.Fatal("an incomplete config was rendered, so nginx would fail to start on a setting the panel accepted")
			}
		})
	}
}

// The certificate is the only thing a no-SNI or wrong-SNI probe sees, so it must
// come from the decoy's own domain and nothing else.
func TestRenderPinsTheCertificateAndServerNames(t *testing.T) {
	cfg := baseConfig()
	cfg.ServerNames = []string{"site.example", "www.site.example"}
	out, err := Render(cfg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "server_name site.example www.site.example;") {
		t.Fatalf("server_name does not carry the certificate's names:\n%s", out)
	}
	if !strings.Contains(out, "ssl_certificate /etc/x-ui/certs/fullchain.pem;") {
		t.Fatalf("certificate path missing:\n%s", out)
	}
	if !strings.Contains(out, "ssl_protocols TLSv1.2 TLSv1.3;") {
		t.Fatalf("TLS versions are not pinned:\n%s", out)
	}
}
