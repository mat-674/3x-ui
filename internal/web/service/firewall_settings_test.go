package service

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
)

func firewallTestDB(t *testing.T) *SettingService {
	t.Helper()
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	return &SettingService{}
}

// A panel-managed policy must beat the deployment's environment, or an operator
// who edits the firewall in the UI would silently keep the old port.
func TestFirewallConfigPrefersSettingsOverEnv(t *testing.T) {
	svc := firewallTestDB(t)
	t.Setenv(envSSHPort, "22")
	t.Setenv(envManageSSH, "true")
	t.Setenv(envAllowPorts, "tcp/8080")

	if err := svc.setInt("firewallSSHPort", 2222); err != nil {
		t.Fatalf("seed ssh port: %v", err)
	}
	if err := svc.setBool("firewallManageSSH", false); err != nil {
		t.Fatalf("seed manage flag: %v", err)
	}
	if err := svc.setString("firewallAllow", "udp/51820"); err != nil {
		t.Fatalf("seed allow list: %v", err)
	}

	cfg, err := svc.FirewallConfig()
	if err != nil {
		t.Fatalf("FirewallConfig: %v", err)
	}
	if cfg.SSHPort != 2222 {
		t.Fatalf("ssh port = %d, want the panel's 2222 rather than the environment's 22", cfg.SSHPort)
	}
	if len(cfg.Allow) != 1 || cfg.Allow[0].Proto != "udp" || cfg.Allow[0].Port != 51820 {
		t.Fatalf("allow = %+v, want the panel's udp/51820 rather than the environment's tcp/8080", cfg.Allow)
	}
}

// A fresh install has no rows, so the environment is the only source there; an
// unset SSH port must not fall back to a guess.
func TestFirewallConfigUsesEnvWhenThePanelHasNoOpinion(t *testing.T) {
	svc := firewallTestDB(t)
	t.Setenv(envSSHPort, "2222")
	t.Setenv(envManageSSH, "true")
	t.Setenv(envAllowPorts, "tcp/8080,udp/51820")

	cfg, err := svc.FirewallConfig()
	if err != nil {
		t.Fatalf("FirewallConfig: %v", err)
	}
	if cfg.SSHPort != 2222 {
		t.Fatalf("ssh port = %d, want the environment's 2222", cfg.SSHPort)
	}
	if !cfg.ManageSSH {
		t.Fatal("manage flag came out false, so a deployment that opted in would leave the firewall untouched")
	}
	if len(cfg.Allow) != 2 {
		t.Fatalf("allow = %+v, want both environment rules", cfg.Allow)
	}
}

// With nothing set at all the policy must stay inert: SSHPort 0 and ManageSSH
// false is what makes Render refuse instead of dropping the operator's port.
func TestFirewallConfigIsInertByDefault(t *testing.T) {
	svc := firewallTestDB(t)
	t.Setenv(envSSHPort, "")
	t.Setenv(envManageSSH, "")
	t.Setenv(envAllowPorts, "")

	cfg, err := svc.FirewallConfig()
	if err != nil {
		t.Fatalf("FirewallConfig: %v", err)
	}
	if cfg.SSHPort != 0 || cfg.ManageSSH {
		t.Fatalf("default policy = %+v, want an inert one that cannot drop SSH", cfg)
	}
}

func TestGetFirewallBinPrefersTheSetting(t *testing.T) {
	svc := firewallTestDB(t)
	t.Setenv(envNFTBin, "/usr/sbin/nft")
	if got := svc.GetFirewallBin(); got != "/usr/sbin/nft" {
		t.Fatalf("bin = %q, want the environment's path when the setting is empty", got)
	}
	if err := svc.setString("firewallNftBin", "/opt/nft"); err != nil {
		t.Fatalf("seed bin: %v", err)
	}
	if got := svc.GetFirewallBin(); got != "/opt/nft" {
		t.Fatalf("bin = %q, want the setting's path", got)
	}
}
