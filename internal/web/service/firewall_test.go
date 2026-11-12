package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/firewall"
)

// fakeNft records every nft invocation and answers with scripted results, so
// these tests prove the argv the service builds without a live firewall.
type fakeNft struct {
	mu     sync.Mutex
	calls  [][]string
	result func(args []string) (string, error)
}

func (f *fakeNft) install(t *testing.T) {
	t.Helper()
	previous := runCLI
	runCLI = func(_ context.Context, bin string, args ...string) (string, error) {
		f.mu.Lock()
		f.calls = append(f.calls, append([]string{bin}, args...))
		f.mu.Unlock()
		if f.result != nil {
			return f.result(args)
		}
		return "", nil
	}
	t.Cleanup(func() { runCLI = previous })
}

// snapshotCalls returns a copy of the recorded invocations.
func (f *fakeNft) snapshotCalls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// loaded reports whether the service ever ran `nft -f <name>`.
func (f *fakeNft) loaded(name string) bool {
	for _, call := range f.snapshotCalls() {
		if len(call) >= 3 && call[1] == "-f" && filepath.Base(call[2]) == name {
			return true
		}
	}
	return false
}

func (f *fakeNft) ranAny() bool {
	return len(f.snapshotCalls()) > 0
}

func newTestFirewall(t *testing.T, cli *fakeNft) *FirewallService {
	t.Helper()
	cli.install(t)
	return &FirewallService{
		Bin:      "nft-fake",
		StateDir: t.TempDir(),
		Config:   firewall.Config{SSHPort: 2222, ManageSSH: true},
	}
}

// waitForRollback blocks until cond holds; the rollback runs on a timer, so a
// fixed sleep would either be slow or flaky.
func waitForRollback(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the rollback never ran; an unconfirmed lockout would have been permanent")
}

func markerGone(dir string) func() bool {
	return func() bool {
		_, err := os.Stat(filepath.Join(dir, pendingRollbackFile))
		return os.IsNotExist(err)
	}
}

// TestApplyRefusesToLoadARulesetNftRejects catches a broken ruleset reaching the
// live firewall, which would replace a working one with what nft refuses.
func TestApplyRefusesToLoadARulesetNftRejects(t *testing.T) {
	cli := &fakeNft{result: func(args []string) (string, error) {
		if len(args) > 0 && args[0] == "-c" {
			return "Error: syntax error, unexpected newline", os.ErrInvalid
		}
		return "", nil
	}}
	svc := newTestFirewall(t, cli)

	id, err := svc.Apply(context.Background(), time.Minute)
	if err == nil {
		t.Fatalf("Apply accepted a ruleset nft rejected and armed rollback %q", id)
	}
	if cli.loaded(candidateRulesetFile) {
		t.Fatalf("Apply loaded a ruleset `nft -c` rejected: %v", cli.snapshotCalls())
	}
}

// TestApplyRequiresAConfirmationWindow catches Apply with no way back: no window
// means no timer, so a wrong rule stays forever.
func TestApplyRequiresAConfirmationWindow(t *testing.T) {
	cli := &fakeNft{}
	svc := newTestFirewall(t, cli)

	for _, window := range []time.Duration{0, -time.Second} {
		if _, err := svc.Apply(context.Background(), window); err == nil {
			t.Fatalf("Apply accepted confirmIn=%v and armed no rollback", window)
		}
	}
	if cli.ranAny() {
		t.Fatalf("Apply touched the firewall although it refused the request: %v", cli.snapshotCalls())
	}
}

// TestApplyRefusesToLoadARulesetWithoutAnSSHAccept catches the safe default being
// bypassed: an unmanaged SSH port would be dropped by the rendered policy.
func TestApplyRefusesToLoadARulesetWithoutAnSSHAccept(t *testing.T) {
	cli := &fakeNft{}
	svc := newTestFirewall(t, cli)
	svc.Config.ManageSSH = false

	if _, err := svc.Apply(context.Background(), time.Minute); err == nil {
		t.Fatal("Apply loaded a default-drop ruleset that does not accept SSH")
	}
	if cli.ranAny() {
		t.Fatalf("Apply ran nft although the ruleset was refused: %v", cli.snapshotCalls())
	}
}

// TestApplyLoadsExactlyWhatPreviewShows catches preview drift: the operator
// approves one text while nft is handed another, so the review certifies nothing.
func TestApplyLoadsExactlyWhatPreviewShows(t *testing.T) {
	cli := &fakeNft{}
	svc := newTestFirewall(t, cli)
	svc.Config.Allow = []firewall.Rule{{Proto: "udp", Port: 51820, Comment: "wireguard"}}

	preview, err := svc.Preview()
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	id, err := svc.Apply(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Cleanup(func() { _ = svc.Confirm(id) })

	loaded, err := os.ReadFile(filepath.Join(svc.StateDir, candidateRulesetFile))
	if err != nil {
		t.Fatalf("read the loaded ruleset: %v", err)
	}
	if string(loaded) != preview {
		t.Fatalf("the loaded ruleset is not the previewed one:\n--- preview ---\n%s\n--- loaded ---\n%s", preview, loaded)
	}
}

// TestAStaleDeadlineCannotDisarmTheRollback catches a confirmation not tied to the
// apply it approves: a replayed or guessed value would retire the only way back.
func TestAStaleDeadlineCannotDisarmTheRollback(t *testing.T) {
	cli := &fakeNft{}
	svc := newTestFirewall(t, cli)

	if _, err := svc.Apply(context.Background(), 40*time.Millisecond); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := svc.Confirm("not-the-id-that-was-issued"); !errors.Is(err, ErrUnknownRollback) {
		t.Fatalf("Confirm with a foreign deadline returned %v, want ErrUnknownRollback", err)
	}
	waitForRollback(t, markerGone(svc.StateDir))
	if !cli.loaded(rollbackRulesetFile) {
		t.Fatalf("a stale confirmation disarmed the rollback: %v", cli.snapshotCalls())
	}
}

// TestConfirmRetiresTheRollbackOnce catches a half-confirmation: the panel would
// report a state it does not act on, and a second confirm would look valid.
func TestConfirmRetiresTheRollbackOnce(t *testing.T) {
	cli := &fakeNft{}
	svc := newTestFirewall(t, cli)

	id, err := svc.Apply(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := svc.Confirm(id); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if cli.loaded(rollbackRulesetFile) {
		t.Fatalf("a confirmed apply was rolled back anyway: %v", cli.snapshotCalls())
	}
	if _, err := os.Stat(filepath.Join(svc.StateDir, pendingRollbackFile)); !os.IsNotExist(err) {
		t.Fatalf("the marker survived a confirmation, so a restart would still restore: %v", err)
	}
	if err := svc.Confirm(id); !errors.Is(err, ErrNoPendingRollback) {
		t.Fatalf("confirming an already confirmed apply returned %v, want ErrNoPendingRollback", err)
	}
}

// TestRollbackRestoresThePreviousRulesetWhenNotConfirmed catches an armed rollback
// that restores the wrong text (or nothing) after an unconfirmed apply.
func TestRollbackRestoresThePreviousRulesetWhenNotConfirmed(t *testing.T) {
	const previousRuleset = "table inet xui {\n\tchain input {\n\t\ttcp dport 2222 accept\n\t}\n}\n"
	cli := &fakeNft{result: func(args []string) (string, error) {
		if len(args) >= 2 && args[0] == "list" && args[1] == "table" {
			return previousRuleset, nil
		}
		return "", nil
	}}
	svc := newTestFirewall(t, cli)

	id, err := svc.Apply(context.Background(), 30*time.Millisecond)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if id == "" {
		t.Fatal("Apply armed no confirmation deadline")
	}
	// The marker is removed only after the restore finished, so waiting for it
	// proves the rollback completed rather than merely started.
	waitForRollback(t, markerGone(svc.StateDir))
	if !cli.loaded(rollbackRulesetFile) {
		t.Fatalf("the timer did not restore the previous ruleset: %v", cli.snapshotCalls())
	}

	stored, err := os.ReadFile(filepath.Join(svc.StateDir, rollbackRulesetFile))
	if err != nil {
		t.Fatalf("the rollback copy is missing: %v", err)
	}
	if !strings.Contains(string(stored), "tcp dport 2222 accept") {
		t.Fatalf("the rollback copy is not the previous firewall:\n%s", stored)
	}
	if rulesetContainsNewPolicy(string(stored)) {
		t.Fatalf("the rollback copy is the new policy, not the previous one:\n%s", stored)
	}
}

// rulesetContainsNewPolicy reports whether text holds a rule only the rendered
// ruleset has (a drop policy), so the previous firewall is distinguishable.
func rulesetContainsNewPolicy(text string) bool {
	return strings.Contains(text, "policy drop")
}

// TestApplyKeepsTheLastConfirmedSnapshotWhileOneIsPending catches a second apply
// overwriting the snapshot, so the timer restores a state nobody verified.
func TestApplyKeepsTheLastConfirmedSnapshotWhileOneIsPending(t *testing.T) {
	const previousRuleset = "table inet xui {\n\tchain input {\n\t\ttcp dport 2222 accept\n\t}\n}\n"
	cli := &fakeNft{result: func(args []string) (string, error) {
		if len(args) >= 2 && args[0] == "list" && args[1] == "table" {
			return previousRuleset, nil
		}
		return "", nil
	}}
	svc := newTestFirewall(t, cli)

	first, err := svc.Apply(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	second, err := svc.Apply(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	t.Cleanup(func() { _ = svc.Confirm(second) })
	// The superseded deadline must not end the new window: confirming it would
	// leave the second ruleset applied with no rollback armed.
	if err := svc.Confirm(first); !errors.Is(err, ErrUnknownRollback) {
		t.Fatalf("Confirm of the superseded deadline returned %v, want ErrUnknownRollback", err)
	}

	stored, err := os.ReadFile(filepath.Join(svc.StateDir, rollbackRulesetFile))
	if err != nil {
		t.Fatalf("the rollback copy is missing: %v", err)
	}
	if rulesetContainsNewPolicy(string(stored)) {
		t.Fatalf("the second apply replaced the confirmed snapshot with its own ruleset:\n%s", stored)
	}
}

// TestARestartRestoresAPendingRollback catches the worst case: a restart kills the
// timer, and only the persisted marker tells a fresh process to undo the apply.
func TestARestartRestoresAPendingRollback(t *testing.T) {
	cli := &fakeNft{}
	svc := newTestFirewall(t, cli)
	id, err := svc.Apply(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Cleanup(func() { _ = svc.Confirm(id) })
	if _, err := os.Stat(filepath.Join(svc.StateDir, pendingRollbackFile)); err != nil {
		t.Fatalf("Apply did not persist a rollback marker: %v", err)
	}

	restarted := &FirewallService{Bin: "nft-fake", StateDir: svc.StateDir, Config: svc.Config}
	restartCLI := &fakeNft{}
	restartCLI.install(t)
	if err := restarted.RestorePending(context.Background()); err != nil {
		t.Fatalf("RestorePending: %v", err)
	}
	if !restartCLI.loaded(rollbackRulesetFile) {
		t.Fatalf("a fresh process did not restore the pending rollback: %v", restartCLI.snapshotCalls())
	}
	if _, err := os.Stat(filepath.Join(svc.StateDir, pendingRollbackFile)); !os.IsNotExist(err) {
		t.Fatalf("the marker survived the restore: %v", err)
	}
}

// TestStatusReportsTheLoadedRulesetFromNft catches a status reporting the desired
// state: an operator would think the host is closed while it is wide open.
func TestStatusReportsTheLoadedRulesetFromNft(t *testing.T) {
	cases := []struct {
		name     string
		ruleset  string
		wantLoad bool
	}{
		{"panel table present", "table inet xui {\n\tchain input {\n\t}\n}", true},
		{"another tool only", "table ip filter {\n\tchain INPUT {\n\t}\n}", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cli := &fakeNft{result: func(args []string) (string, error) {
				if len(args) >= 2 && args[0] == "list" && args[1] == "ruleset" {
					return tc.ruleset, nil
				}
				return "", nil
			}}
			svc := newTestFirewall(t, cli)
			svc.Config.Allow = []firewall.Rule{{Proto: "udp", Port: 51820}}

			st, err := svc.Status(context.Background())
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if st.Loaded != tc.wantLoad {
				t.Fatalf("Status.Loaded = %v, want %v for ruleset %q", st.Loaded, tc.wantLoad, tc.ruleset)
			}
			if want := []int{2222, firewall.HTTPSPort}; !reflect.DeepEqual(st.AllowedTCP, want) {
				t.Fatalf("Status.AllowedTCP = %v, want %v", st.AllowedTCP, want)
			}
			if want := []int{51820}; !reflect.DeepEqual(st.AllowedUDP, want) {
				t.Fatalf("Status.AllowedUDP = %v, want %v", st.AllowedUDP, want)
			}
		})
	}
}

// TestApplyUndoesTheLoadWhenItCannotArmARollback catches the half-applied state:
// a load with no marker and no timer can never be undone.
func TestApplyUndoesTheLoadWhenItCannotArmARollback(t *testing.T) {
	const previousRuleset = "table inet xui {\n\tchain input {\n\t\ttcp dport 2222 accept\n\t}\n}\n"
	cli := &fakeNft{result: func(args []string) (string, error) {
		if len(args) >= 2 && args[0] == "list" && args[1] == "table" {
			return previousRuleset, nil
		}
		return "", nil
	}}
	svc := newTestFirewall(t, cli)
	// A directory where the marker file must go makes persisting it fail, which
	// is also what a full or read-only state disk does in the field.
	if err := os.MkdirAll(filepath.Join(svc.StateDir, pendingRollbackFile), 0o750); err != nil {
		t.Fatalf("prepare the unpersistable marker: %v", err)
	}

	if _, err := svc.Apply(context.Background(), time.Minute); err == nil {
		t.Fatal("Apply reported success although no rollback could be persisted")
	}
	if !cli.loaded(rollbackRulesetFile) {
		t.Fatalf("Apply left the new ruleset live with no rollback armed: %v", cli.snapshotCalls())
	}
	last := cli.snapshotCalls()[len(cli.snapshotCalls())-1]
	if filepath.Base(last[len(last)-1]) != rollbackRulesetFile {
		t.Fatalf("the last nft call was not the undo: %v", last)
	}
	stored, err := os.ReadFile(filepath.Join(svc.StateDir, rollbackRulesetFile))
	if err != nil {
		t.Fatalf("read the restore copy: %v", err)
	}
	if rulesetContainsNewPolicy(string(stored)) {
		t.Fatalf("the undo loaded the new policy instead of the previous firewall:\n%s", stored)
	}
}
