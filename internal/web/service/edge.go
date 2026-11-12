package service

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/edge"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/web/entity"
)

// EdgeSettings is the nginx perimeter's configuration, read fresh on every apply
// so a saved change takes effect without restarting the panel.
type EdgeSettings struct {
	Enable        bool
	Listen        string
	Port          int
	ServerNames   []string
	CertFile      string
	KeyFile       string
	NginxBin      string
	ProxyProtocol bool
	// HTTPPort is the public plaintext port for ACME validation and the HTTPS
	// redirect; 0 leaves it off.
	HTTPPort int
}

// ErrNginxMissing reports that the perimeter cannot start because the binary is
// absent; the API maps it to the install prompt rather than a generic failure.
var ErrNginxMissing = errors.New("nginx is not installed")

// StateDir is where the generated config, logs and certificates-adjacent state
// live. Keeping it beside the database means a panel backup carries settings,
// not a machine-specific nginx tree.
func (s *SettingService) StateDir() string {
	return filepath.Join(config.GetDBFolderPath(), "edge")
}

// GetEdgeSettings reads the perimeter settings, filling the certificate from the
// panel's own pair when the perimeter has none: one certificate file, one place
// to renew it.
func (s *SettingService) GetEdgeSettings() (EdgeSettings, error) {
	var es EdgeSettings
	var err error
	if es.Enable, err = s.getBool("edgeEnable"); err != nil {
		return es, err
	}
	if es.Listen, err = s.getString("edgeListen"); err != nil {
		return es, err
	}
	if es.Port, err = s.getInt("edgePort"); err != nil {
		return es, err
	}
	names, err := s.getString("edgeServerNames")
	if err != nil {
		return es, err
	}
	es.ServerNames = splitDomains(names)
	if es.CertFile, err = s.getString("edgeCertFile"); err != nil {
		return es, err
	}
	if es.KeyFile, err = s.getString("edgeKeyFile"); err != nil {
		return es, err
	}
	if strings.TrimSpace(es.CertFile) == "" {
		if es.CertFile, err = s.GetCertFile(); err != nil {
			return es, err
		}
	}
	if strings.TrimSpace(es.KeyFile) == "" {
		if es.KeyFile, err = s.GetKeyFile(); err != nil {
			return es, err
		}
	}
	if es.NginxBin, err = s.getString("edgeNginxBin"); err != nil {
		return es, err
	}
	if es.ProxyProtocol, err = s.getBool("edgeProxyProtocol"); err != nil {
		return es, err
	}
	if es.HTTPPort, err = s.getInt("edgeHTTPPort"); err != nil {
		return es, err
	}
	return es, nil
}

// BuildEdgeConfig renders the panel's desired perimeter state. Everything the
// config needs is derived: the panel and subscription listen on loopback because
// the perimeter is what faces the internet.
func (s *SettingService) BuildEdgeConfig() (edge.Config, error) {
	settings, err := s.GetEdgeSettings()
	if err != nil {
		return edge.Config{}, err
	}
	if !settings.Enable {
		return edge.Config{}, common.NewError("the nginx perimeter is disabled")
	}
	stateDir := s.StateDir()
	cfg := edge.Config{
		Listen:        settings.Listen,
		Port:          settings.Port,
		ProxyProtocol: settings.ProxyProtocol,
		CertFile:      settings.CertFile,
		KeyFile:       settings.KeyFile,
		DecoyIndex:    "index.html",
		PanelPrefix:   normalizePrefix(mustString(s.GetBasePath())),
		PidFile:       filepath.Join(stateDir, "nginx.pid"),
		LogDir:        filepath.Join(stateDir, "logs"),
		TempDir:       filepath.Join(stateDir, "tmp"),
		NotFoundFile:  filepath.Join(stateDir, "404.html"),
		HTTPPort:      settings.HTTPPort,
		AcmeRoot:      filepath.Join(stateDir, "acme"),
	}

	names := settings.ServerNames
	if len(names) == 0 {
		names = certDNSNames(settings.CertFile)
	}
	if len(names) == 0 {
		return edge.Config{}, common.NewError("no server names: set edgeServerNames or use a certificate with a DNS name")
	}
	cfg.ServerNames = names

	panelPort, err := s.GetPort()
	if err != nil {
		return edge.Config{}, err
	}
	cfg.PanelUpstream = fmt.Sprintf("127.0.0.1:%d", panelPort)

	if subEnabled, err := s.GetSubEnable(); err == nil && subEnabled {
		subPort, portErr := s.GetSubPort()
		if portErr != nil {
			return edge.Config{}, portErr
		}
		cfg.SubUpstream = fmt.Sprintf("127.0.0.1:%d", subPort)
		for _, get := range []func() (string, error){s.GetSubPath, s.GetSubJsonPath, s.GetSubClashPath} {
			p, pathErr := get()
			if pathErr != nil {
				return edge.Config{}, pathErr
			}
			if prefix := normalizePrefix(p); prefix != "" {
				cfg.SubPrefixes = append(cfg.SubPrefixes, prefix)
			}
		}
	}

	root, index, err := s.decoyRoot(stateDir)
	if err != nil {
		return edge.Config{}, err
	}
	cfg.DecoyRoot, cfg.DecoyIndex = root, index
	if err := writeNotFoundPage(root, cfg.NotFoundFile); err != nil {
		return edge.Config{}, err
	}
	return cfg, nil
}

// writeNotFoundPage reuses the site's own 404 when it has one, so an unknown path
// answers exactly like the rest of the site instead of like nginx.
func writeNotFoundPage(decoyRoot, target string) error {
	if data, err := os.ReadFile(filepath.Join(decoyRoot, "404.html")); err == nil {
		return os.WriteFile(target, data, 0o644)
	}
	return os.WriteFile(target, []byte(edgeNotFoundHTML), 0o644)
}

// decoyRoot resolves the site nginx serves at "/". With no uploaded site it uses
// a generated placeholder, so the perimeter always has something to show and a
// probe never reaches an empty root.
func (s *SettingService) decoyRoot(stateDir string) (string, string, error) {
	fallback := filepath.Join(stateDir, "decoy")
	if err := os.MkdirAll(fallback, 0o755); err != nil {
		return "", "", err
	}
	if err := ensurePlaceholderSite(fallback, stateDir); err != nil {
		return "", "", err
	}

	siteId := mustInt(s.getInt("frontSiteId"))
	if siteId <= 0 {
		return fallback, "index.html", nil
	}
	site, err := (&FrontSiteService{}).Get(siteId)
	if err != nil || site == nil {
		logger.Warningf("decoy site %d is unavailable, serving the placeholder: %v", siteId, err)
		return fallback, "index.html", nil
	}
	dir, err := (&FrontSiteService{}).Dir(site.Id)
	if err != nil {
		logger.Warningf("decoy site %d directory is unusable, serving the placeholder: %v", site.Id, err)
		return fallback, "index.html", nil
	}
	index := site.EntryFile
	if strings.TrimSpace(index) == "" {
		index = "index.html"
	}
	return dir, index, nil
}

// ensurePlaceholderSite writes a neutral landing page and 404 into the state dir,
// so the perimeter has a fallback site before any upload exists.
func ensurePlaceholderSite(dir, stateDir string) error {
	files := map[string]string{
		filepath.Join(dir, "index.html"):    edgePlaceholderHTML,
		filepath.Join(stateDir, "404.html"): edgeNotFoundHTML,
	}
	for path, body := range files {
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ApplyEdge renders, validates and activates the perimeter. A config nginx
// rejects leaves the running process untouched, so a bad setting cannot take the
// site down.
func (s *SettingService) ApplyEdge(ctx context.Context) error {
	cfg, err := s.BuildEdgeConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.LogDir, 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.TempDir, 0o750); err != nil {
		return err
	}
	// The challenge directory has to exist before nginx starts: without it the
	// location resolves to a 404 and a renewal fails for a reason nobody expects.
	if cfg.HTTPPort > 0 {
		if err := os.MkdirAll(filepath.Join(cfg.AcmeRoot, ".well-known", "acme-challenge"), 0o755); err != nil {
			return err
		}
	}
	// Naming the missing binary here beats letting `nginx -t` fail with a bare
	// exec error: the UI turns this into an install button.
	installer := edge.NewInstaller(mustString(s.getString("edgeNginxBin")))
	if _, ok := installer.Present(); !ok {
		if ok, hint := installer.CanInstall(); ok {
			return fmt.Errorf("%w: install it from the panel and apply again", ErrNginxMissing)
		} else {
			return fmt.Errorf("%w: %s", ErrNginxMissing, hint)
		}
	}
	mgr := &edge.Manager{
		Bin:        mustString(s.getString("edgeNginxBin")),
		ConfigPath: filepath.Join(s.StateDir(), "nginx.conf"),
		StateDir:   s.StateDir(),
		Listen:     cfg.Listen,
		Port:       cfg.Port,
	}
	// A taken plaintext port makes nginx refuse to start, which would take the
	// whole perimeter down; refuse the apply instead and name what holds it.
	if err := checkHTTPPortFree(mgr, cfg); err != nil {
		return err
	}
	return mgr.Apply(ctx, cfg)
}

// checkHTTPPortFree refuses an apply whose plaintext port something else already
// owns. A running perimeter that owns the socket is not a conflict: reload keeps
// the listener, so that case is left alone.
func checkHTTPPortFree(mgr *edge.Manager, cfg edge.Config) error {
	if cfg.HTTPPort <= 0 {
		return nil
	}
	addr := net.JoinHostPort(cfg.HTTPListen, strconv.Itoa(cfg.HTTPPort))
	if strings.TrimSpace(cfg.HTTPListen) == "" {
		addr = net.JoinHostPort("0.0.0.0", strconv.Itoa(cfg.HTTPPort))
	}
	probe, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err == nil {
		_ = probe.Close()
		return nil
	}
	if mgr.Running() {
		// The socket is ours; a reload reuses it.
		return nil
	}
	return fmt.Errorf("edge: port %d cannot be bound (%w): stop whatever owns it, or set edgeHTTPPort to 0", cfg.HTTPPort, err)
}

// EdgeManager returns the manager for status and lifecycle calls.
func (s *SettingService) EdgeManager() (*edge.Manager, error) {
	settings, err := s.GetEdgeSettings()
	if err != nil {
		return nil, err
	}
	return &edge.Manager{
		Bin:        settings.NginxBin,
		ConfigPath: filepath.Join(s.StateDir(), "nginx.conf"),
		StateDir:   s.StateDir(),
		Listen:     settings.Listen,
		Port:       settings.Port,
	}, nil
}

// EdgeStatus reports the perimeter's state without changing it: a settings page
// load must never start or reload anything.
func (s *SettingService) EdgeStatus() (entity.EdgeStatus, error) {
	var st entity.EdgeStatus
	settings, err := s.GetEdgeSettings()
	if err != nil {
		return st, err
	}
	st.Enabled = settings.Enable
	st.Listen = settings.Listen
	st.Port = settings.Port
	st.HTTPPort = settings.HTTPPort
	if leaf := certLeaf(settings.CertFile); leaf != nil {
		st.CertDomains = leaf.DNSNames
		st.CertNotAfter = leaf.NotAfter.UTC().Format("2006-01-02T15:04:05Z")
	}
	if cfg, err := s.BuildEdgeConfig(); err == nil {
		st.ConfigValid = true
		st.ServerNames = cfg.ServerNames
		st.PanelUpstream = cfg.PanelUpstream
		st.SubPrefixes = len(cfg.SubPrefixes)
	} else {
		st.Message = err.Error()
	}
	if mgr, err := s.EdgeManager(); err == nil {
		st.Running = mgr.Running()
	}
	if settings.Enable && !st.Running && st.Message == "" {
		st.Message = "the perimeter is enabled but nothing answers on its listen address"
	}

	// The perimeter cannot start at all without the binary, so its state is part
	// of the status rather than a separate question the UI has to remember to ask.
	installer := edge.NewInstaller(settings.NginxBin)
	if bin, ok := installer.Present(); ok {
		st.NginxInstalled = true
		st.NginxBin = bin
		st.NginxVersion = installer.Version(ctxOrBackground())
	} else {
		st.NginxInstallable, st.NginxHint = installer.CanInstall()
		// The install hint is reported next to the button, not as the perimeter's
		// own failure: an operator may enable the perimeter later.
		if settings.Enable && st.Message == "" && !st.NginxInstallable {
			st.Message = st.NginxHint
		}
	}
	return st, nil
}

// ctxOrBackground keeps status readable from a page load, where no request context
// is threaded through the settings service.
func ctxOrBackground() context.Context { return context.Background() }

// PreviewEdgeConfig returns the exact text ApplyEdge would hand to nginx.
func (s *SettingService) PreviewEdgeConfig() (string, error) {
	cfg, err := s.BuildEdgeConfig()
	if err != nil {
		return "", err
	}
	return edge.Render(cfg)
}

// certLeaf parses the first certificate of a PEM bundle.
func certLeaf(path string) *x509.Certificate {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return nil
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		leaf, parseErr := x509.ParseCertificate(block.Bytes)
		if parseErr != nil {
			return nil
		}
		return leaf
	}
}

func certDNSNames(path string) []string {
	if leaf := certLeaf(path); leaf != nil {
		return leaf.DNSNames
	}
	return nil
}

// normalizePrefix keeps a path usable as an nginx location prefix.
func normalizePrefix(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func mustInt(v int, err error) int {
	if err != nil {
		return 0
	}
	return v
}

const edgePlaceholderHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Welcome</title>
<style>
body{margin:0;font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
display:flex;min-height:100vh;align-items:center;justify-content:center;background:#f6f7f9;color:#14161a}
main{max-width:34rem;padding:2rem;text-align:center}
h1{font-size:1.5rem;font-weight:600;margin:0 0 .75rem}
p{margin:0;line-height:1.6;color:#5a616b}
@media (prefers-color-scheme:dark){body{background:#12141a;color:#eaecf0}p{color:#9aa2ad}}
</style>
</head>
<body>
<main>
<h1>Welcome</h1>
<p>This site is being set up. Please check back soon.</p>
</main>
</body>
</html>
`

const edgeNotFoundHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>404 Not Found</title>
<style>
body{margin:0;font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
display:flex;min-height:100vh;align-items:center;justify-content:center;color:#14161a}
main{text-align:center}
h1{font-size:3rem;margin:0}
</style>
</head>
<body>
<main>
<h1>404</h1>
<p>Page not found.</p>
</main>
</body>
</html>
`
