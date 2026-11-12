import { useTranslation } from 'react-i18next';
import { Alert, Divider, Typography } from 'antd';

import type { AllSetting } from '@/models/setting';
import FirewallSettingsSection from './firewall/FirewallSettingsSection';
import FirewallStatusCard from './firewall/FirewallStatusCard';
import FirewallActions from './firewall/FirewallActions';
import './firewall/firewallTab.css';

interface FirewallTabProps {
  allSetting: AllSetting;
  updateSetting: (patch: Partial<AllSetting>) => void;
  saveSetting: (payload: Partial<AllSetting> & Record<string, unknown>) => Promise<unknown>;
}

export default function FirewallTab({ allSetting, updateSetting, saveSetting }: FirewallTabProps) {
  const { t } = useTranslation();

  return (
    <>
      <Alert
        type="info"
        showIcon
        title={t('pages.settings.firewall.intro')}
        style={{ marginBottom: 8 }}
      />

      <FirewallSettingsSection allSetting={allSetting} updateSetting={updateSetting} />

      <Typography.Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0 }}>
        {t('pages.settings.firewall.saveHint')}
      </Typography.Paragraph>

      <Divider />
      <FirewallStatusCard />

      <Divider />
      <FirewallActions allSetting={allSetting} saveSetting={saveSetting} />
    </>
  );
}
