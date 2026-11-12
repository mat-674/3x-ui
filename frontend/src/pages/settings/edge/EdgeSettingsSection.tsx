import { useTranslation } from 'react-i18next';
import { Alert, Input, InputNumber, Switch } from 'antd';

import { SettingListItem } from '@/components/ui';
import { onNumber } from '@/utils/onNumber';
import type { AllSetting } from '@/models/setting';

interface EdgeSettingsSectionProps {
  allSetting: AllSetting;
  updateSetting: (patch: Partial<AllSetting>) => void;
}

export default function EdgeSettingsSection({
  allSetting,
  updateSetting,
}: EdgeSettingsSectionProps) {
  const { t } = useTranslation();

  return (
    <div className="edge-section">
      <SettingListItem
        paddings="small"
        title={t('pages.settings.edge.enable')}
        description={t('pages.settings.edge.enableDesc')}
      >
        <Switch
          checked={allSetting.edgeEnable}
          onChange={(checked) => updateSetting({ edgeEnable: checked })}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.edge.listen')}
        description={t('pages.settings.edge.listenDesc')}
      >
        <Input
          value={allSetting.edgeListen}
          placeholder="127.0.0.1"
          onChange={(e) => updateSetting({ edgeListen: e.target.value })}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.edge.port')}
        description={t('pages.settings.edge.portDesc')}
      >
        <InputNumber
          value={allSetting.edgePort}
          min={1}
          max={65535}
          style={{ width: '100%' }}
          onChange={onNumber((v) => updateSetting({ edgePort: v }))}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.edge.serverNames')}
        description={t('pages.settings.edge.serverNamesDesc')}
      >
        <Input
          value={allSetting.edgeServerNames}
          placeholder="panel.example.com, www.example.com"
          onChange={(e) => updateSetting({ edgeServerNames: e.target.value })}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.edge.certFile')}
        description={t('pages.settings.edge.certFileDesc')}
      >
        <Input
          value={allSetting.edgeCertFile}
          placeholder="/etc/x-ui/cert/fullchain.pem"
          onChange={(e) => updateSetting({ edgeCertFile: e.target.value })}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.edge.keyFile')}
        description={t('pages.settings.edge.keyFileDesc')}
      >
        <Input
          value={allSetting.edgeKeyFile}
          placeholder="/etc/x-ui/cert/privkey.pem"
          onChange={(e) => updateSetting({ edgeKeyFile: e.target.value })}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.edge.nginxBin')}
        description={t('pages.settings.edge.nginxBinDesc')}
      >
        <Input
          value={allSetting.edgeNginxBin}
          placeholder="nginx"
          onChange={(e) => updateSetting({ edgeNginxBin: e.target.value })}
        />
      </SettingListItem>

      <SettingListItem
        paddings="small"
        title={t('pages.settings.edge.proxyProtocol')}
        description={t('pages.settings.edge.proxyProtocolDesc')}
      >
        <Switch
          checked={allSetting.edgeProxyProtocol}
          onChange={(checked) => updateSetting({ edgeProxyProtocol: checked })}
        />
      </SettingListItem>

      {/* Both sides of this pair must agree, so the risk is stated for whichever
          setting the operator has to change. */}
      {allSetting.edgeProxyProtocol ? (
        <div data-testid="edge-xver-note">
          <Alert type="info" showIcon title={t('pages.settings.edge.proxyProtocolOnNote')} />
        </div>
      ) : (
        <div data-testid="edge-xver-warning">
          <Alert type="warning" showIcon title={t('pages.settings.edge.proxyProtocolOffWarn')} />
        </div>
      )}
    </div>
  );
}
