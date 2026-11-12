package edge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// ErrNotRunning reports that nginx has no master process to signal. It is
// returned by the -s operations so a caller can start instead of reloading.
var ErrNotRunning = errors.New("edge: nginx is not running")

// Manager owns the generated nginx configuration and drives the nginx CLI.
//
// nginx is managed through its own command line rather than as a supervised
// child: it daemonises itself, keeps its own pid file and master process, and
// reloading through `-s reload` is the only supported way to apply a config
// without dropping the listening socket.
type Manager struct {
	// Bin is the nginx executable; "" means "nginx" from PATH.
	Bin string
	// ConfigPath is the generated config this manager owns.
	ConfigPath string
	// StateDir holds the pid file, logs and temp directories.
	StateDir string
	// Listen is the address probed to decide whether the perimeter is up.
	Listen string
	Port   int
}

// runCLI runs the nginx command line; tests substitute it to assert the argv
// each operation builds without needing nginx installed.
var runCLI = func(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ensureDirs creates the directories the generated config refers to, so nginx
// never fails on a missing temp or log path.
func (m *Manager) ensureDirs() error {
	for _, dir := range []string{m.StateDir, filepath.Join(m.StateDir, "logs"), filepath.Join(m.StateDir, "tmp")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	return nil
}

// Apply renders cfg, validates it, and only then swaps it in and reloads.
// A config nginx rejects never reaches the running process, so a bad setting
// cannot take the perimeter down.
func (m *Manager) Apply(ctx context.Context, cfg Config) error {
	if err := m.ensureDirs(); err != nil {
		return err
	}
	rendered, err := Render(cfg)
	if err != nil {
		return err
	}
	logger.Debugf("edge: applying perimeter, listen=%s:%d proxy_protocol=%v names=%v panel=%s sub=%s decoy=%s cert=%s (%d bytes)",
		cfg.Listen, cfg.Port, cfg.ProxyProtocol, cfg.ServerNames, cfg.PanelUpstream, cfg.SubUpstream, cfg.DecoyRoot, cfg.CertFile, len(rendered))
	if err := writeFileAtomic(m.ConfigPath, []byte(rendered), 0o600); err != nil {
		return fmt.Errorf("edge: write config: %w", err)
	}
	out, err := m.Test(ctx)
	if err != nil {
		logger.Errorf("edge: nginx rejected the generated config %s: %v\n%s", m.ConfigPath, err, out)
		return fmt.Errorf("edge: nginx rejected the generated config: %w: %s", err, out)
	}
	logger.Debugf("edge: nginx -t accepted %s: %s", m.ConfigPath, out)
	return m.reloadOrStart(ctx)
}

// Test runs `nginx -t` against the generated config.
func (m *Manager) Test(ctx context.Context) (string, error) {
	out, err := m.run(ctx, "-t", "-c", m.ConfigPath)
	return strings.TrimSpace(out), err
}

// Reload signals the running master to re-read the config, keeping the socket.
func (m *Manager) Reload(ctx context.Context) error {
	out, err := m.run(ctx, "-s", "reload", "-c", m.ConfigPath)
	logger.Debugf("edge: reload %s -> err=%v out=%q", m.ConfigPath, err, out)
	return err
}

// Stop asks the master to exit after finishing in-flight requests.
func (m *Manager) Stop(ctx context.Context) error {
	if !m.Running() {
		logger.Debug("edge: stop skipped, nothing answers on the listen address")
		return nil
	}
	out, err := m.run(ctx, "-s", "quit", "-c", m.ConfigPath)
	logger.Debugf("edge: quit -> err=%v out=%q", err, out)
	return err
}

// reloadOrStart reloads when a master exists and starts one otherwise, so the
// first Apply after boot and every later Apply take the same path.
func (m *Manager) reloadOrStart(ctx context.Context) error {
	if m.Running() {
		logger.Debugf("edge: %s is up, reloading", m.addr())
		return m.Reload(ctx)
	}
	logger.Infof("edge: starting nginx (%s -c %s), nothing answered on %s yet", m.bin(), m.ConfigPath, m.addr())
	out, err := m.run(ctx, "-c", m.ConfigPath)
	if err != nil {
		logger.Errorf("edge: nginx failed to start: %v\n%s", err, out)
		return err
	}
	logger.Infof("edge: nginx started and answers on %s", m.addr())
	return nil
}

// Running reports whether the perimeter answers on its listen address. A dial is
// used instead of a pid lookup because it is the property that actually matters:
// whether Xray's REALITY fallback has somewhere to go.
func (m *Manager) Running() bool {
	if m.Listen == "" || m.Port == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", m.addr())
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (m *Manager) addr() string {
	host := m.Listen
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("%s:%d", host, m.Port)
}

func (m *Manager) bin() string {
	if bin := strings.TrimSpace(m.Bin); bin != "" {
		return bin
	}
	return "nginx"
}

func (m *Manager) run(ctx context.Context, args ...string) (string, error) {
	return runCLI(ctx, m.bin(), args...)
}

// writeFileAtomic writes data via a same-directory temp file renamed into place,
// so a crash cannot leave a half-written config that nginx then refuses to load.
func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nginx-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
