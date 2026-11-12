package edge

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeCLI records every nginx invocation and answers with scripted results.
type fakeCLI struct {
	calls  [][]string
	result func(args []string) (string, error)
}

func (f *fakeCLI) install(t *testing.T) {
	t.Helper()
	previous := runCLI
	runCLI = func(_ context.Context, bin string, args ...string) (string, error) {
		f.calls = append(f.calls, append([]string{bin}, args...))
		if f.result != nil {
			return f.result(args)
		}
		return "", nil
	}
	t.Cleanup(func() { runCLI = previous })
}

func (f *fakeCLI) sawArgs(want ...string) bool {
	for _, call := range f.calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, strings.Join(want, " ")) {
			return true
		}
	}
	return false
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	return &Manager{
		Bin:        "nginx-test",
		ConfigPath: filepath.Join(dir, "nginx.conf"),
		StateDir:   dir,
	}
}

func testConfig(t *testing.T, m *Manager) Config {
	t.Helper()
	cfg := baseConfig()
	cfg.LogDir = filepath.Join(m.StateDir, "logs")
	cfg.TempDir = filepath.Join(m.StateDir, "tmp")
	cfg.PidFile = filepath.Join(m.StateDir, "nginx.pid")
	cfg.NotFoundFile = filepath.Join(m.StateDir, "404.html")
	return cfg
}

// A config nginx rejects must never reach the running process: swapping it in
// first and reloading would take the perimeter — and the panel behind it — down.
func TestApplyRefusesToReloadAConfigNginxRejects(t *testing.T) {
	cli := &fakeCLI{result: func(args []string) (string, error) {
		if len(args) > 0 && args[0] == "-t" {
			return "nginx: [emerg] unknown directive", errors.New("exit status 1")
		}
		return "", nil
	}}
	cli.install(t)
	m := newTestManager(t)

	err := m.Apply(context.Background(), testConfig(t, m))
	if err == nil {
		t.Fatal("Apply accepted a config nginx rejected")
	}
	if cli.sawArgs("-s", "reload") {
		t.Fatal("Apply reloaded even though `nginx -t` failed, so the live perimeter was replaced by a broken config")
	}
}

func TestApplyWritesTheRenderedConfig(t *testing.T) {
	cli := &fakeCLI{}
	cli.install(t)
	m := newTestManager(t)

	if err := m.Apply(context.Background(), testConfig(t, m)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(m.ConfigPath)
	if err != nil {
		t.Fatalf("config was not written: %v", err)
	}
	if !strings.Contains(string(data), "server_tokens off;") {
		t.Fatalf("written config is not the rendered one:\n%s", data)
	}
	if !cli.sawArgs("-t", "-c", m.ConfigPath) {
		t.Fatalf("Apply did not validate the config before using it: %v", cli.calls)
	}
}

// The first Apply after boot must start nginx; every later one must reload it,
// or the panel either forks a second master or never picks the new config up.
func TestApplyStartsWhenDownAndReloadsWhenUp(t *testing.T) {
	cli := &fakeCLI{}
	cli.install(t)
	m := newTestManager(t)

	if err := m.Apply(context.Background(), testConfig(t, m)); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if cli.sawArgs("-s", "reload") {
		t.Fatal("the first Apply reloaded instead of starting, so nothing would ever start nginx")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	m.Listen = "127.0.0.1"
	m.Port = port
	cli.calls = nil

	if err := m.Apply(context.Background(), testConfig(t, m)); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if !cli.sawArgs("-s", "reload") {
		t.Fatalf("Apply did not reload a running nginx: %v", cli.calls)
	}
}

func TestRunningFollowsTheListeningSocket(t *testing.T) {
	m := newTestManager(t)
	m.Listen, m.Port = "127.0.0.1", 1
	if m.Running() {
		t.Fatal("Running reported true with nothing listening")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	m.Port = ln.Addr().(*net.TCPAddr).Port
	if !m.Running() {
		t.Fatalf("Running reported false while something listens on %d", m.Port)
	}
}

// Stopping an absent nginx must be silent: the panel calls it on every shutdown
// and a hard error there would look like a failed shutdown.
func TestStopIsANoOpWhenNothingRuns(t *testing.T) {
	cli := &fakeCLI{}
	cli.install(t)
	m := newTestManager(t)
	m.Listen, m.Port = "127.0.0.1", 1

	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop with nothing running: %v", err)
	}
	if cli.sawArgs("-s", "quit") {
		t.Fatal("Stop signalled a master that is not there")
	}
}

// The binary path is a setting; an empty one must fall back to PATH rather than
// trying to exec "".
func TestBinFallsBackToPATH(t *testing.T) {
	m := &Manager{}
	if got := m.bin(); got != "nginx" {
		t.Fatalf("bin() = %q, want nginx", got)
	}
	m.Bin = " /usr/sbin/nginx "
	if got := m.bin(); got != "/usr/sbin/nginx" {
		t.Fatalf("bin() = %q, want the trimmed path", got)
	}
}

func TestAddrDefaultsToLoopback(t *testing.T) {
	m := &Manager{Port: 8443}
	if got := m.addr(); got != net.JoinHostPort("127.0.0.1", strconv.Itoa(8443)) {
		t.Fatalf("addr() = %q", got)
	}
}
