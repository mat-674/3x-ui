import type { AllSetting } from '@/models/setting';

export type FirewallPatch = Pick<
  AllSetting,
  'firewallSSHPort' | 'firewallManageSSH' | 'firewallAllow' | 'firewallNftBin'
>;

/* Apply reads the policy from the DB, so these four keys are persisted first —
   otherwise a confirmed apply loads the previous SSH port and cuts SSH off. */
export function firewallPatchOf(setting: AllSetting, sshPort: number): FirewallPatch {
  return {
    firewallSSHPort: sshPort,
    firewallManageSSH: setting.firewallManageSSH,
    firewallAllow: setting.firewallAllow,
    firewallNftBin: setting.firewallNftBin,
  };
}
