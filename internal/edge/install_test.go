package edge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// fakeSystem stands in for the package manager, the container probe and root.
type fakeSystem struct {
	globs     map[string]string // probe name -> resolved path
	commands  [][]string
	results   func(name string, args []string) (string, error)
	container bool
	euid      int
}

func (f *fakeSystem) installer() *Installer {
	return &Installer{
		LookPath: func(name string) (string, error) {
			if path, ok := f.globs[name]; ok {
				return path, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			f.commands = append(f.commands, append([]string{name}, args...))
			if f.results != nil {
				return f.results(name, args)
			}
			return "", nil
		},
		InContainer: func() bool { return f.container },
		GOOS:        func() string { return "linux" },
		EUID:        func() int { return f.euid },
	}
}

func (f *fakeSystem) ran(want string) bool {
	for _, cmd := range f.commands {
		if strings.Contains(strings.Join(cmd, " "), want) {
			return true
		}
	}
	return false
}

// The whole point: a first enable on a bare host installs nginx, so nobody has to
// remember an install-time flag or a manual apt command.
func TestInstallPicksTheHostPackageManagerAndInstalls(t *testing.T) {
	sys := &fakeSystem{globs: map[string]string{"apt-get": "/usr/bin/apt-get"}}
	sys.results = func(name string, args []string) (string, error) {
		if name == "/usr/bin/nginx" || name == "nginx" {
			return "nginx version: nginx/1.24.0", nil
		}
		// The package manager advertised nginx; the binary then exists.
		if name == "apt-get" && len(args) > 0 && args[0] == "install" {
			sys.globs["nginx"] = "/usr/bin/nginx"
		}
		return "ok", nil
	}
	inst := sys.installer()

	report, err := inst.Install(context.Background())
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if report.AlreadyInstalled {
		t.Fatal("reported an already-installed binary on a bare host")
	}
	if report.PackageManager != "apt" {
		t.Fatalf("package manager = %q, want apt", report.PackageManager)
	}
	if !sys.ran("apt-get update") || !sys.ran("apt-get install -y -q nginx") {
		t.Fatalf("did not run the expected install commands: %v", sys.commands)
	}
	if report.Bin == "" || report.Version != "nginx/1.24.0" {
		t.Fatalf("report = %+v, want the resolved binary and version", report)
	}
}

// Installing on a host that already has nginx must change nothing: an operator's
// own package is not ours to touch.
func TestInstallIsANoOpWhenNginxExists(t *testing.T) {
	sys := &fakeSystem{globs: map[string]string{"nginx": "/usr/sbin/nginx", "apt-get": "/usr/bin/apt-get"}}
	inst := sys.installer()

	report, err := inst.Install(context.Background())
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !report.AlreadyInstalled || report.Bin != "/usr/sbin/nginx" {
		t.Fatalf("report = %+v, want alreadyInstalled with the existing path", report)
	}
	if len(sys.commands) != 1 || sys.commands[0][0] != "/usr/sbin/nginx" {
		t.Fatalf("commands = %v, want only the version probe", sys.commands)
	}
}

// A panel in a container must refuse: the package would disappear with the
// container, and the image is where nginx belongs.
func TestInstallRefusesInsideAContainer(t *testing.T) {
	sys := &fakeSystem{globs: map[string]string{"apt-get": "/usr/bin/apt-get"}, container: true}
	_, err := sys.installer().Install(context.Background())
	if !errors.Is(err, ErrNginxInContainer) {
		t.Fatalf("error = %v, want ErrNginxInContainer", err)
	}
	if sys.ran("apt-get install") {
		t.Fatalf("a container install still ran the package manager: %v", sys.commands)
	}
}

// Running unprivileged must refuse before touching the package manager.
func TestInstallRefusesWithoutRoot(t *testing.T) {
	sys := &fakeSystem{globs: map[string]string{"dnf": "/usr/bin/dnf"}, euid: 1000}
	_, err := sys.installer().Install(context.Background())
	if !errors.Is(err, ErrNginxNotRoot) {
		t.Fatalf("error = %v, want ErrNginxNotRoot", err)
	}
	if sys.ran("dnf install") {
		t.Fatalf("an unprivileged install still ran the package manager: %v", sys.commands)
	}
}

// A host with no supported package manager must say so instead of running
// something arbitrary.
func TestInstallWithoutAPackageManager(t *testing.T) {
	sys := &fakeSystem{globs: map[string]string{}}
	_, err := sys.installer().Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "package manager") {
		t.Fatalf("error = %v, want a message naming the missing package manager", err)
	}
}

// A package manager that "succeeds" without producing a binary is a failure the
// operator must see, not a silent success.
func TestInstallFailsWhenTheBinaryNeverAppears(t *testing.T) {
	sys := &fakeSystem{globs: map[string]string{"pacman": "/usr/bin/pacman"}}
	report, err := sys.installer().Install(context.Background())
	if err == nil {
		t.Fatal("Install reported success although no binary appeared")
	}
	if report == nil || len(report.Steps) == 0 {
		t.Fatal("the failure carried no steps, so the operator cannot see what ran")
	}
}

// The packaged unit must be parked, otherwise its default site takes port 80 and
// collides with the panel's own instance.
func TestInstallParksThePackagedService(t *testing.T) {
	sys := &fakeSystem{globs: map[string]string{
		"dnf":       "/usr/bin/dnf",
		"systemctl": "/usr/bin/systemctl",
	}}
	sys.results = func(name string, args []string) (string, error) {
		if name == "dnf" && len(args) > 0 && args[0] == "install" {
			sys.globs["nginx"] = "/usr/sbin/nginx"
		}
		return "", nil
	}
	sys.installer().Install(context.Background())

	if !sys.ran("systemctl disable --now nginx") {
		t.Fatalf("the packaged unit was left enabled: %v", sys.commands)
	}
}

// CanInstall drives the UI button state, so a wrong answer is a dead button or a
// button that always fails.
func TestCanInstallReportsTheReason(t *testing.T) {
	cases := []struct {
		name string
		sys  *fakeSystem
		want bool
	}{
		{"present", &fakeSystem{globs: map[string]string{"nginx": "/usr/sbin/nginx"}}, false},
		{"container", &fakeSystem{globs: map[string]string{"apt-get": "/usr/bin/apt-get"}, container: true}, false},
		{"not root", &fakeSystem{globs: map[string]string{"apt-get": "/usr/bin/apt-get"}, euid: 1000}, false},
		{"no manager", &fakeSystem{globs: map[string]string{}}, false},
		{"installable", &fakeSystem{globs: map[string]string{"apt-get": "/usr/bin/apt-get"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := tc.sys.installer().CanInstall()
			if ok != tc.want {
				t.Fatalf("CanInstall = %v (%q), want %v", ok, reason, tc.want)
			}
			if !ok && reason == "" {
				t.Fatal("a refusal carried no reason, so the UI cannot explain the disabled button")
			}
		})
	}
}

// A configured binary path must win over PATH, or a custom build is ignored.
func TestPresentHonoursTheConfiguredBinary(t *testing.T) {
	dir := t.TempDir()
	custom := dir + "/nginx-custom"
	if err := writeExecutable(custom); err != nil {
		t.Fatalf("write: %v", err)
	}
	sys := &fakeSystem{globs: map[string]string{"nginx": "/usr/sbin/nginx"}}
	inst := sys.installer()
	inst.Bin = custom

	bin, ok := inst.Present()
	if !ok || bin != custom {
		t.Fatalf("Present = %q/%v, want the configured %q", bin, ok, custom)
	}
}

func TestFirstLineKeepsOnlyTheVersion(t *testing.T) {
	if got := firstLine("nginx version: 1.24.0\nextra\n"); got != "nginx version: 1.24.0" {
		t.Fatalf("firstLine = %q", got)
	}
	if got := firstLine(""); got != "" {
		t.Fatalf("firstLine(\"\") = %q", got)
	}
}

func TestFormatStepShowsTheCommandAndFailure(t *testing.T) {
	line := formatStep("apt-get", []string{"install", "-y", "nginx"}, "E: lock held", fmt.Errorf("exit status 100"))
	for _, want := range []string{"$ apt-get install -y nginx", "E: lock held", "exit status 100"} {
		if !strings.Contains(line, want) {
			t.Fatalf("step %q is missing %q", line, want)
		}
	}
}

// writeExecutable creates a stand-in binary for the configured-path case.
func writeExecutable(path string) error {
	return os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755)
}
