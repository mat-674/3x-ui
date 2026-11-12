package edge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ErrNginxInContainer refuses an in-place install inside a container: the package
// would vanish with the container, and the image is the right place to add it.
var ErrNginxInContainer = errors.New("edge: nginx must be installed on the host, not inside the panel container")

// ErrNginxNotRoot refuses an install the panel cannot perform: a package manager
// run without root fails half-way and leaves a lock behind.
var ErrNginxNotRoot = errors.New("edge: installing a package needs the panel to run as root")

// packageManager is one distribution's install recipe. Probe is the binary that
// proves the manager exists; Cmd is what actually runs (apt is probed as "apt"
// but driven as "apt-get", which is the scripting interface).
type packageManager struct {
	Name    string
	Probe   string
	Cmd     string
	Update  []string
	Install []string
}

// packageManagers is ordered by preference, so a host carrying both apt and dnf
// gets the native one first.
var packageManagers = []packageManager{
	{Name: "apt", Probe: "apt-get", Cmd: "apt-get", Update: []string{"update"}, Install: []string{"install", "-y", "-q", "nginx"}},
	{Name: "dnf", Probe: "dnf", Cmd: "dnf", Install: []string{"install", "-y", "-q", "nginx"}},
	{Name: "yum", Probe: "yum", Cmd: "yum", Install: []string{"install", "-y", "-q", "nginx"}},
	{Name: "pacman", Probe: "pacman", Cmd: "pacman", Install: []string{"-Sy", "--noconfirm", "nginx"}},
	{Name: "zypper", Probe: "zypper", Cmd: "zypper", Install: []string{"-q", "install", "-y", "nginx"}},
	{Name: "apk", Probe: "apk", Cmd: "apk", Install: []string{"add", "--no-cache", "nginx"}},
}

// InstallReport is what the panel tells the operator after an attempt: a silent
// success is not enough, the exact commands have to stay visible.
type InstallReport struct {
	AlreadyInstalled bool     `json:"alreadyInstalled" example:"false"`
	PackageManager   string   `json:"packageManager" example:"apt"`
	Version          string   `json:"version" example:"1.24.0"`
	Bin              string   `json:"bin" example:"/usr/sbin/nginx"`
	Steps            []string `json:"steps"`
}

// Installer installs the nginx binary through the distribution's package manager.
// Every outside effect is injectable, so the decisions are testable without a
// package manager, a container or root.
type Installer struct {
	// Bin is the configured nginx path; "" looks nginx up on PATH.
	Bin string
	// Timeout bounds one package-manager run; 0 means two minutes.
	Timeout time.Duration

	LookPath    func(string) (string, error)
	Run         func(ctx context.Context, name string, args ...string) (string, error)
	InContainer func() bool
	EUID        func() int
	// GOOS reports the host OS; nil means the build target. Only Linux has the
	// package managers this installer drives.
	GOOS func() string
}

// NewInstaller returns an installer wired to the real system.
func NewInstaller(bin string) *Installer {
	return &Installer{
		Bin:         bin,
		LookPath:    exec.LookPath,
		Run:         runCommand,
		InContainer: inContainer,
		EUID:        os.Geteuid,
	}
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// inContainer probes the usual markers; a panel in Docker must not install.
func inContainer() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

func (i *Installer) lookPath() func(string) (string, error) {
	if i.LookPath != nil {
		return i.LookPath
	}
	return exec.LookPath
}

func (i *Installer) run() func(ctx context.Context, name string, args ...string) (string, error) {
	if i.Run != nil {
		return i.Run
	}
	return runCommand
}

func (i *Installer) inContainer() bool {
	if i.InContainer != nil {
		return i.InContainer()
	}
	return inContainer()
}

// Present reports the nginx binary path when one is usable.
func (i *Installer) Present() (string, bool) {
	lookPath := i.lookPath()
	if bin := strings.TrimSpace(i.Bin); bin != "" {
		if info, err := os.Stat(bin); err == nil && !info.IsDir() {
			return bin, true
		}
		if path, err := lookPath(bin); err == nil {
			return path, true
		}
		return "", false
	}
	if path, err := lookPath("nginx"); err == nil {
		return path, true
	}
	return "", false
}

// Version returns the binary's version, which also proves it can execute.
func (i *Installer) Version(ctx context.Context) string {
	bin, ok := i.Present()
	if !ok {
		return ""
	}
	// `nginx -v` writes to stderr, which CombinedOutput captures.
	out, _ := i.run()(ctx, bin, "-v")
	return strings.TrimPrefix(firstLine(out), "nginx version: ")
}

// CanInstall reports whether an in-place install is possible, and if not, why —
// so the UI can disable the button with a reason instead of failing on click.
func (i *Installer) CanInstall() (bool, string) {
	if bin, ok := i.Present(); ok {
		return false, fmt.Sprintf("nginx is already installed at %s", bin)
	}
	if runtime.GOOS != "linux" {
		if i.GOOS == nil || i.GOOS() != "linux" {
			return false, "installing nginx from the panel is supported on Linux only"
		}
	}
	if i.inContainer() {
		return false, "the panel runs in a container: add nginx to the image instead of installing it at runtime"
	}
	if euid := i.EUID; euid != nil && euid() != 0 {
		return false, "the panel must run as root to install a package"
	}
	if _, err := i.packageManager(); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// packageManager picks the first supported package manager on the host.
func (i *Installer) packageManager() (packageManager, error) {
	lookPath := i.lookPath()
	for _, pm := range packageManagers {
		if _, err := lookPath(pm.Probe); err == nil {
			return pm, nil
		}
	}
	return packageManager{}, errors.New("no supported package manager found (apt, dnf, yum, pacman, zypper, apk)")
}

// Install installs nginx, then parks the packaged service: the panel runs its own
// instance from its own config, so the distribution's site must not hold port 80.
func (i *Installer) Install(ctx context.Context) (*InstallReport, error) {
	report := &InstallReport{}
	if bin, ok := i.Present(); ok {
		report.AlreadyInstalled = true
		report.Bin = bin
		report.Version = i.Version(ctx)
		return report, nil
	}
	if ok, reason := i.CanInstall(); !ok {
		return nil, refusalError(reason)
	}

	pm, err := i.packageManager()
	if err != nil {
		return nil, err
	}
	report.PackageManager = pm.Name
	run := i.run()
	timeout := i.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	step := func(name string, args ...string) error {
		cmdCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		out, runErr := run(cmdCtx, name, args...)
		report.Steps = append(report.Steps, formatStep(name, args, out, runErr))
		return runErr
	}

	if pm.Update != nil {
		// A stale index is the usual first-install failure; a failed update is not
		// fatal, because the package may already be cached locally.
		_ = step(pm.Cmd, pm.Update...)
	}
	if err := step(pm.Cmd, pm.Install...); err != nil {
		return report, fmt.Errorf("edge: %s %s failed, so nginx is not installed: %w",
			pm.Cmd, strings.Join(pm.Install, " "), err)
	}
	report.Steps = append(report.Steps, i.parkPackagedService(ctx)...)

	bin, ok := i.Present()
	if !ok {
		return report, errors.New("edge: the package manager reported success but no nginx binary is on PATH")
	}
	report.Bin = bin
	report.Version = i.Version(ctx)
	return report, nil
}

// refusalError maps a refusal reason back to its sentinel, so callers can tell
// "this host cannot do it" from "the install itself failed".
func refusalError(reason string) error {
	switch {
	case strings.Contains(reason, "container"):
		return fmt.Errorf("%w: %s", ErrNginxInContainer, reason)
	case strings.Contains(reason, "root"):
		return fmt.Errorf("%w: %s", ErrNginxNotRoot, reason)
	default:
		return errors.New(reason)
	}
}

// parkPackagedService stops and disables the distribution's nginx unit so it
// cannot answer on port 80 next to the panel's own instance.
func (i *Installer) parkPackagedService(ctx context.Context) []string {
	lookPath := i.lookPath()
	run := i.run()
	candidates := [][]string{
		{"systemctl", "disable", "--now", "nginx"},
		{"rc-service", "nginx", "stop"},
		{"service", "nginx", "stop"},
	}
	for _, cmd := range candidates {
		if _, err := lookPath(cmd[0]); err != nil {
			continue
		}
		cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		out, runErr := run(cmdCtx, cmd[0], cmd[1:]...)
		cancel()
		return []string{formatStep(cmd[0], cmd[1:], out, runErr)}
	}
	return nil
}

// formatStep renders one command and its outcome for the visible report.
func formatStep(name string, args []string, out string, err error) string {
	line := "$ " + name + " " + strings.Join(args, " ")
	if err != nil {
		line += "  -> " + err.Error()
	}
	if trimmed := strings.TrimSpace(out); trimmed != "" {
		if len(trimmed) > 500 {
			trimmed = trimmed[:500] + "..."
		}
		line += "\n" + trimmed
	}
	return line
}

// firstLine keeps the version line from a multi-line output.
func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}
