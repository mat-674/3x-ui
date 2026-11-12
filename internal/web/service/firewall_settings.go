package service

import (
	"strconv"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/firewall"
)

// FirewallConfig reads the desired firewall policy from panel settings, falling
// back to the environment only when the panel has no opinion. Settings win so an
// operator can manage the firewall from the UI instead of editing a unit file.
func (s *SettingService) FirewallConfig() (firewall.Config, error) {
	var cfg firewall.Config
	sshPort, err := s.getInt("firewallSSHPort")
	if err != nil {
		return cfg, err
	}
	if sshPort == 0 {
		// An unset setting still honours the deployment's environment: a fresh
		// install legitimately comes from XUI_SSH_PORT.
		if fromEnv, convErr := strconv.Atoi(strings.TrimSpace(getEnv(envSSHPort, ""))); convErr == nil {
			sshPort = fromEnv
		}
	}
	manageSSH, err := s.getBool("firewallManageSSH")
	if err != nil {
		return cfg, err
	}
	if !manageSSH {
		if fromEnv, convErr := strconv.ParseBool(strings.TrimSpace(getEnv(envManageSSH, ""))); convErr == nil {
			manageSSH = fromEnv
		}
	}
	allow, err := s.getString("firewallAllow")
	if err != nil {
		return cfg, err
	}
	if strings.TrimSpace(allow) == "" {
		allow = getEnv(envAllowPorts, "")
	}

	cfg.SSHPort = sshPort
	cfg.ManageSSH = manageSSH
	cfg.Allow = parseAllowPorts(allow)
	return cfg, nil
}

// GetFirewallBin returns the nft executable path, preferring the setting.
func (s *SettingService) GetFirewallBin() string {
	if bin, err := s.getString("firewallNftBin"); err == nil && strings.TrimSpace(bin) != "" {
		return strings.TrimSpace(bin)
	}
	return strings.TrimSpace(getEnv(envNFTBin, ""))
}
