package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/firewall"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// The deployment's ports come from panel settings, with the environment as a
// fallback: guessing a port is what turns a firewall rule into a lockout.
const (
	envNFTBin     = "XUI_NFT_BIN"
	envSSHPort    = "XUI_SSH_PORT"
	envManageSSH  = "XUI_FIREWALL_MANAGE_SSH"
	envAllowPorts = "XUI_FIREWALL_ALLOW"

	candidateRulesetFile = "ruleset.nft"
	rollbackRulesetFile  = "rollback.nft"
	pendingRollbackFile  = "pending-rollback.json"

	nftTimeout = 30 * time.Second
)

// absentRuleset is the replayable form of "the panel's table did not exist", so
// a rollback returns the host to a ruleset the panel never installed.
const absentRuleset = "table inet xui\ndelete table inet xui\n"

// Errors a caller has to tell apart from "nft failed".
var (
	ErrConfirmWindowRequired = errors.New("firewall: a confirmation window is required; an unconfirmed apply would be permanent")
	ErrNoPendingRollback     = errors.New("firewall: no apply is waiting for confirmation")
	ErrUnknownRollback       = errors.New("firewall: unknown confirmation deadline")
)

// runCLI runs the nft command line; tests substitute it so the panel can be
// exercised without ever touching a live firewall.
var runCLI = func(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// FirewallService owns the panel's nftables ruleset: it renders it, checks it
// with nft, loads it, and always keeps a way back to the previous firewall.
type FirewallService struct {
	// Bin is the nft executable; "" means "nft" from PATH.
	Bin string
	// StateDir holds the candidate, the rollback copy and the pending marker.
	StateDir string
	// Config is the desired input policy, resolved from the deployment settings.
	Config firewall.Config

	mu      sync.Mutex
	pending *rollbackMarker
	timer   *time.Timer
}

// rollbackMarker is what tells a fresh process that an apply is still
// unconfirmed and must be undone.
type rollbackMarker struct {
	ID       string    `json:"id"`
	Deadline time.Time `json:"deadline"`
	Previous string    `json:"previous"`
}

// FirewallStatus reports the loaded firewall next to what the panel means to
// allow, so an operator can see drift between the two.
type FirewallStatus struct {
	Loaded           bool   `json:"loaded"`
	Table            string `json:"table"`
	SSHPort          int    `json:"sshPort"`
	ManageSSH        bool   `json:"manageSsh"`
	AllowedTCP       []int  `json:"allowedTcp"`
	AllowedUDP       []int  `json:"allowedUdp"`
	PendingRollback  bool   `json:"pendingRollback"`
	RollbackID       string `json:"rollbackId,omitempty"`
	RollbackDeadline string `json:"rollbackDeadline,omitempty"`
	Message          string `json:"message,omitempty"`
}

// NewFirewallService wires the service to the deployment's ports and to a state
// dir beside the database, so a backup carries the rollback copy.
func NewFirewallService() *FirewallService {
	settingService := &SettingService{}
	bin := settingService.GetFirewallBin()
	if bin == "" {
		bin = strings.TrimSpace(os.Getenv(envNFTBin))
	}
	cfg, err := settingService.FirewallConfig()
	if err != nil {
		// An unreadable setting must not silently widen the policy: an empty
		// config renders nothing, and Render refuses it.
		logger.Warning("firewall settings unreadable, the policy is left untouched:", err)
		cfg = firewall.Config{}
	}
	return &FirewallService{
		Bin:      bin,
		StateDir: filepath.Join(config.GetDBFolderPath(), "firewall"),
		Config:   cfg,
	}
}

func parseAllowPorts(spec string) []firewall.Rule {
	var rules []firewall.Rule
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		proto, portText, ok := strings.Cut(entry, "/")
		port, err := strconv.Atoi(strings.TrimSpace(portText))
		if !ok || err != nil {
			rules = append(rules, firewall.Rule{Proto: entry})
			continue
		}
		rules = append(rules, firewall.Rule{Proto: proto, Port: port})
	}
	return rules
}

// Preview returns the exact ruleset Apply would load.
func (s *FirewallService) Preview() (string, error) {
	return firewall.Render(s.Config)
}

// Apply validates the ruleset with `nft -c -f`, loads it only if nft accepts it,
// then arms a rollback unless Confirm lands within confirmIn; no window, no apply.
func (s *FirewallService) Apply(ctx context.Context, confirmIn time.Duration) (string, error) {
	if confirmIn <= 0 {
		return "", ErrConfirmWindowRequired
	}
	rendered, err := s.Preview()
	if err != nil {
		return "", err
	}
	logger.Infof("firewall: applying policy ssh_port=%d manage_ssh=%v allow=%v nft=%q window=%s",
		s.Config.SSHPort, s.Config.ManageSSH, s.Config.Allow, s.Bin, confirmIn)
	if err := os.MkdirAll(s.stateDir(), 0o750); err != nil {
		return "", fmt.Errorf("firewall: state dir: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Snapshot before the first nft write, so every failure below leaves the
	// host's firewall exactly as it was.
	baseline, err := s.baseline(ctx)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(s.path(rollbackRulesetFile), []byte(baseline), 0o600); err != nil {
		return "", fmt.Errorf("firewall: write rollback copy: %w", err)
	}
	candidate := s.path(candidateRulesetFile)
	if err := writeFileAtomic(candidate, []byte(rendered), 0o600); err != nil {
		return "", fmt.Errorf("firewall: write candidate ruleset: %w", err)
	}
	if out, err := s.run(ctx, "-c", "-f", candidate); err != nil {
		logger.Errorf("firewall: nft rejected the candidate ruleset, live firewall untouched: %v\n%s", err, out)
		return "", fmt.Errorf("firewall: nft rejected the ruleset; the live firewall is untouched: %w: %s", err, out)
	}
	if out, err := s.run(ctx, "-f", candidate); err != nil {
		logger.Errorf("firewall: loading the ruleset failed: %v\n%s", err, out)
		return "", fmt.Errorf("firewall: load ruleset: %w: %s", err, out)
	}
	logger.Infof("firewall: ruleset loaded, rollback armed for %s unless confirmed", time.Now().Add(confirmIn).Format(time.RFC3339))

	s.stopTimer()
	marker := rollbackMarker{
		ID:       newRollbackID(),
		Deadline: time.Now().Add(confirmIn).UTC(),
		Previous: rollbackRulesetFile,
	}
	if err := s.writeMarker(marker); err != nil {
		// Without a persisted way back the timer would die with the process, so
		// undo the apply now rather than leave a ruleset nobody can confirm.
		if undoErr := s.restore(ctx, baseline); undoErr != nil {
			return "", fmt.Errorf("firewall: persist rollback marker and the undo of the load both failed, so the rendered ruleset is live with no rollback armed: %w", errors.Join(err, undoErr))
		}
		return "", fmt.Errorf("firewall: persist rollback marker, previous firewall restored: %w", err)
	}
	s.pending = &marker
	id := marker.ID
	s.timer = time.AfterFunc(confirmIn, func() { s.rollback(id) })
	return id, nil
}

// Confirm retires the pending rollback: the operator has verified that the
// panel and SSH still answer on the new ruleset.
func (s *FirewallService) Confirm(deadlineID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return ErrNoPendingRollback
	}
	if deadlineID != s.pending.ID {
		return ErrUnknownRollback
	}
	// Drop the marker first: if that fails the timer must keep running, or a
	// restart would still restore while nothing is armed to do it now.
	if err := os.Remove(s.path(pendingRollbackFile)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("firewall: clear rollback marker: %w", err)
	}
	s.stopTimer()
	s.pending = nil
	return nil
}

// RestorePending restores the previous firewall for a rollback armed by an
// earlier process: a restart is not a confirmation. Call it at startup.
func (s *FirewallService) RestorePending(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	marker := s.readMarker()
	if marker == nil {
		return nil
	}
	if out, err := s.run(ctx, "-f", s.path(filepath.Base(marker.Previous))); err != nil {
		return fmt.Errorf("firewall: restore the previous firewall after a restart: %w: %s", err, out)
	}
	if err := os.Remove(s.path(pendingRollbackFile)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("firewall: clear rollback marker: %w", err)
	}
	logger.Infof("firewall: restored the previous firewall for an apply that was never confirmed")
	return nil
}

// Status reports whether the panel's table is loaded and which ports it opens.
// It runs `nft list ruleset`, because the loaded firewall is the only truth.
func (s *FirewallService) Status(ctx context.Context) (FirewallStatus, error) {
	st := FirewallStatus{
		Table:      firewall.TableRef,
		SSHPort:    s.Config.SSHPort,
		ManageSSH:  s.Config.ManageSSH,
		AllowedTCP: s.allowedPorts("tcp"),
		AllowedUDP: s.allowedPorts("udp"),
	}
	if marker := s.readMarker(); marker != nil {
		st.PendingRollback = true
		st.RollbackID = marker.ID
		st.RollbackDeadline = marker.Deadline.UTC().Format(time.RFC3339)
	}
	out, err := s.run(ctx, "list", "ruleset")
	if err != nil {
		st.Message = "nft is unavailable: " + err.Error()
		return st, fmt.Errorf("firewall: list ruleset: %w", err)
	}
	st.Loaded = strings.Contains(out, "table "+firewall.TableRef)
	if !st.Loaded {
		st.Message = "the panel's ruleset is not loaded: the host firewall is whatever the distribution, Docker or another tool installed"
	}
	return st, nil
}

// baseline is the ruleset a rollback returns to. With an apply still
// unconfirmed that is its snapshot, never the ruleset nobody verified yet.
func (s *FirewallService) baseline(ctx context.Context) (string, error) {
	if s.pending != nil || s.readMarker() != nil {
		if data, err := os.ReadFile(s.path(rollbackRulesetFile)); err == nil {
			return string(data), nil
		}
	}
	return s.snapshot(ctx)
}

// snapshot captures the panel's table in a form that `nft -f` replays over the
// table it replaced. A missing table means "restore it to missing".
func (s *FirewallService) snapshot(ctx context.Context) (string, error) {
	out, err := s.run(ctx, "list", "table", firewall.TableFamily, firewall.TableName)
	if err != nil {
		return absentRuleset, nil
	}
	return "table " + firewall.TableRef + "\ndelete table " + firewall.TableRef + "\n" + out, nil
}

// rollback is the timer callback for an apply nobody confirmed.
func (s *FirewallService) rollback(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil || s.pending.ID != id {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), nftTimeout)
	defer cancel()
	data, err := os.ReadFile(s.path(rollbackRulesetFile))
	if err == nil {
		err = s.restore(ctx, string(data))
	}
	if err != nil {
		// Keep the marker: the next start retries the restore.
		logger.Warningf("firewall: could not restore the previous firewall after an unconfirmed apply: %v", err)
		return
	}
	s.pending, s.timer = nil, nil
	if err := os.Remove(s.path(pendingRollbackFile)); err != nil && !os.IsNotExist(err) {
		logger.Warningf("firewall: could not clear the rollback marker: %v", err)
	}
	logger.Infof("firewall: rolled back an apply that was never confirmed")
}

// restore loads ruleset, reporting nft's own message so a failed rollback is
// diagnosable instead of a generic error.
func (s *FirewallService) restore(ctx context.Context, ruleset string) error {
	path := s.path(rollbackRulesetFile)
	if err := writeFileAtomic(path, []byte(ruleset), 0o600); err != nil {
		return fmt.Errorf("firewall: write restore ruleset: %w", err)
	}
	if out, err := s.run(ctx, "-f", path); err != nil {
		return fmt.Errorf("firewall: load restore ruleset: %w: %s", err, out)
	}
	return nil
}

func (s *FirewallService) allowedPorts(proto string) []int {
	seen := map[int]struct{}{}
	ports := []int{}
	add := func(port int) {
		if port < 1 || port > 65535 {
			return
		}
		if _, dup := seen[port]; dup {
			return
		}
		seen[port] = struct{}{}
		ports = append(ports, port)
	}
	if proto == "tcp" {
		if s.Config.ManageSSH {
			add(s.Config.SSHPort)
		}
		add(firewall.HTTPSPort)
	}
	for _, r := range s.Config.Allow {
		if strings.EqualFold(strings.TrimSpace(r.Proto), proto) {
			add(r.Port)
		}
	}
	return ports
}

func (s *FirewallService) stopTimer() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

func (s *FirewallService) stateDir() string {
	if s.StateDir != "" {
		return s.StateDir
	}
	return filepath.Join(config.GetDBFolderPath(), "firewall")
}

func (s *FirewallService) path(name string) string {
	return filepath.Join(s.stateDir(), name)
}

func (s *FirewallService) bin() string {
	if bin := strings.TrimSpace(s.Bin); bin != "" {
		return bin
	}
	return "nft"
}

func (s *FirewallService) run(ctx context.Context, args ...string) (string, error) {
	return runCLI(ctx, s.bin(), args...)
}

func (s *FirewallService) readMarker() *rollbackMarker {
	data, err := os.ReadFile(s.path(pendingRollbackFile))
	if err != nil {
		return nil
	}
	var marker rollbackMarker
	if err := json.Unmarshal(data, &marker); err != nil || marker.ID == "" {
		logger.Warningf("firewall: pending rollback marker is unreadable, an unconfirmed apply cannot be restored: %v", err)
		return nil
	}
	return &marker
}

func (s *FirewallService) writeMarker(marker rollbackMarker) error {
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path(pendingRollbackFile), data, 0o600)
}

func newRollbackID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf[:])
}

// writeFileAtomic writes data via a same-directory temp file renamed into place,
// so a crash cannot leave a half-written ruleset that nft then refuses to load.
func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nft-*.tmp")
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
