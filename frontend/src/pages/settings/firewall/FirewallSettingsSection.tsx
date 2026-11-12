import { useTranslation } from 'react-i18next';
import { Alert, Input, InputNumber, Switch } from 'antd';

import { SettingListItem } from '@/components/ui';
import { onNumber } from '@/utils/onNumber';
import type { AllSetting } from '@/models/setting';

interface FirewallSettingsSectionProps {
  allSetting: AllSetting;
  updateSetting: (patch: Partial<AllSetting>) => void;
}

export default function FirewallSettingsSection({
  allSetting,
  updateSetting,
}: FirewallSettingsSectionProps) {
  const { t } = useTranslation();

  return (
    <div className="firewall-section">
      <SettingListItem
        paddings="small"
        title={t('pages.settings.firewall.manageSsh')}
        description={t('pages.settings.firewall.manageSshDesc')}
      >
        <Switch
          checked={allSetting.firewallManageSSH}
          onChange={(checked) => updateSetting({ firewallManageSSH: checked })}
        />
      </SettingListItem>

      {/* With SSH unmanaged nothing can be loaded at all, so the refusal is
          stated where the switch is, not only after an apply is attempted. */}
      {!allSetting.firewallManageSSH && (
        <div data-testid="firewall-manage-ssh-off">
          <Alert type="warning" showIcon title={t('pages.settings.firewall.manageSshOffWarn')} />
        </div>
      )}

      <SettingListItem
        paddings="small"
        title={t('pages.settings.firewall.sshPort')}
        description={t('pages.settings.firewall.sshPortDesc')}
      >
        <InputNumber
          value={allSetting.firewallSSHPort}
          min={0}
          max={65535}
          style={{ width: '100%' }}
          onChange={onNumber((v) => updateSetting({ firewallSSHPort: v }))}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.firewall.allow')}
        description={t('pages.settings.firewall.allowDesc')}
      >
        <Input
          value={allSetting.firewallAllow}
          placeholder="tcp/8080,udp/51820"
          onChange={(e) => updateSetting({ firewallAllow: e.target.value })}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.firewall.nftBin')}
        description={t('pages.settings.firewall.nftBinDesc')}
      >
        <Input
          value={allSetting.firewallNftBin}
          placeholder="nft"
          onChange={(e) => updateSetting({ firewallNftBin: e.target.value })}
        />
      </SettingListItem>
    </div>
  );
}
