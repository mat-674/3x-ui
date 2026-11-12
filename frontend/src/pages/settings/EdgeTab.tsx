import { useTranslation } from 'react-i18next';
import { Alert, Divider, Typography } from 'antd';

import type { AllSetting } from '@/models/setting';
import EdgeNginxSection from './edge/EdgeNginxSection';
import EdgeSettingsSection from './edge/EdgeSettingsSection';
import EdgeStatusCard from './edge/EdgeStatusCard';
import EdgeActions from './edge/EdgeActions';
import { edgePatchOf } from './edge/edgePatch';
import './edge/edgeTab.css';

interface EdgeTabProps {
  allSetting: AllSetting;
  updateSetting: (patch: Partial<AllSetting>) => void;
  saveSetting: (payload: Partial<AllSetting> & Record<string, unknown>) => Promise<unknown>;
}

export default function EdgeTab({ allSetting, updateSetting, saveSetting }: EdgeTabProps) {
  const { t } = useTranslation();

  return (
    <>
      <Alert
        type="info"
        showIcon
        title={t('pages.settings.edge.intro')}
        style={{ marginBottom: 8 }}
      />

      <EdgeNginxSection />

      <EdgeSettingsSection allSetting={allSetting} updateSetting={updateSetting} />

      <Typography.Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0 }}>
        {t('pages.settings.edge.saveHint')}
      </Typography.Paragraph>

      <Divider />
      <EdgeStatusCard />

      <Divider />
      <EdgeActions patch={edgePatchOf(allSetting)} saveSetting={saveSetting} />
    </>
  );
}
