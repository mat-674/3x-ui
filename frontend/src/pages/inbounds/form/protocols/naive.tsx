import { useTranslation } from 'react-i18next';
import { Input, Switch } from 'antd';

import { FormField } from '@/components/form/rhf';

export default function NaiveFields() {
  const { t } = useTranslation();
  return (
    <>
      <FormField
        name={['settings', 'domain']}
        label={t('pages.inbounds.naive.domain')}
        tooltip={t('pages.inbounds.naive.domainHint')}
        rules={{ required: t('pages.inbounds.naive.domainRequired') }}
      >
        <Input placeholder="proxy.example.com" />
      </FormField>
      <FormField
        name={['settings', 'fallbackRoot']}
        label={t('pages.inbounds.naive.fallbackRoot')}
        tooltip={t('pages.inbounds.naive.fallbackRootHint')}
      >
        <Input placeholder="/var/www/html" />
      </FormField>
      <div style={{ color: 'var(--ant-color-text-secondary)', fontSize: 12 }}>
        {t('pages.inbounds.naive.tlsHint')}
      </div>
      <FormField
        name={['settings', 'probeResistance']}
        label={t('pages.inbounds.naive.probeResistance')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
      <FormField
        name={['settings', 'hideIp']}
        label={t('pages.inbounds.naive.hideIp')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
      <FormField
        name={['settings', 'hideVia']}
        label={t('pages.inbounds.naive.hideVia')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
      <FormField
        name={['settings', 'encode']}
        label={t('pages.inbounds.naive.encode')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
    </>
  );
}
