import { useTranslation } from 'react-i18next';
import { Alert, Divider, Input, Typography } from 'antd';

import { SettingListItem } from '@/components/ui';
import type { AllSetting } from '@/models/setting';
import FrontSitesSection from './front/FrontSitesSection';
import FrontCertsSection from './front/FrontCertsSection';
import './front/frontTab.css';

interface FrontTabProps {
  allSetting: AllSetting;
  updateSetting: (patch: Partial<AllSetting>) => void;
}

export default function FrontTab({ allSetting, updateSetting }: FrontTabProps) {
  const { t } = useTranslation();

  return (
    <>
      <Alert
        type="info"
        showIcon
        title={t('pages.settings.front.intro')}
        style={{ marginBottom: 8 }}
      />

      <SettingListItem
        paddings="small"
        title={t('pages.settings.front.cookieName')}
        description={t('pages.settings.front.cookieNameDesc')}
      >
        <Input
          value={allSetting.panelCookieName}
          placeholder="sess"
          onChange={(e) => updateSetting({ panelCookieName: e.target.value })}
        />
      </SettingListItem>

      <Typography.Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0 }}>
        {t('pages.settings.front.saveHint')}
      </Typography.Paragraph>

      <Divider />
      <FrontSitesSection
        frontSiteId={allSetting.frontSiteId}
        onSelect={(id) => updateSetting({ frontSiteId: id })}
      />

      <Divider />
      <FrontCertsSection />
    </>
  );
}
