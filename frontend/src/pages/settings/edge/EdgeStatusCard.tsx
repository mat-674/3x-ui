import { useTranslation } from 'react-i18next';
import { Alert, Descriptions, Space, Tag, Typography } from 'antd';
import type { DescriptionsProps } from 'antd';

import { useEdgeStatus } from '@/api/queries/useEdgeQueries';
import { certExpiryLevel, daysUntilExpiry } from '../front/certExpiry';

/* The perimeter presents the same certificate the certificates tab tracks, so the
   14-day warning comes from the same helper rather than a second threshold. */
function CertExpiryCell({ notAfter }: { notAfter: string | null | undefined }) {
  const { t } = useTranslation();
  const days = daysUntilExpiry(notAfter);

  if (!notAfter || days === null) {
    return <span data-testid="edge-cert-missing">{t('pages.settings.edge.certMissing')}</span>;
  }

  const stamp = new Date(notAfter).toLocaleString();
  const level = certExpiryLevel(days);
  if (level === 'expired') {
    return (
      <span data-testid="edge-cert-expiry">
        {stamp}
        {' · '}
        <Tag color="red">{t('pages.settings.front.certExpired', { days: Math.abs(days) })}</Tag>
      </span>
    );
  }
  if (level === 'expiring') {
    return (
      <span data-testid="edge-cert-expiry">
        {stamp}
        {' · '}
        <Tag color="orange">
          {days === 0
            ? t('pages.settings.front.certExpiresToday')
            : t('pages.settings.front.certRenewNow', { days })}
        </Tag>
      </span>
    );
  }
  return <span>{`${stamp} · ${t('pages.settings.front.certDaysLeft', { days })}`}</span>;
}

function TagList({ values, testId }: { values: string[]; testId: string }) {
  if (values.length === 0) return <span>—</span>;
  return (
    <Space size={4} wrap data-testid={testId}>
      {values.map((value) => (
        <Tag key={value}>{value}</Tag>
      ))}
    </Space>
  );
}

function StatusBody() {
  const { t } = useTranslation();
  const { data, isPending, isError } = useEdgeStatus();

  if (isPending && !data) {
    return <Typography.Text type="secondary">{t('loading')}</Typography.Text>;
  }
  if (isError || !data) {
    return (
      <div data-testid="edge-status-error">
        <Alert type="error" showIcon title={t('pages.settings.edge.statusLoadFailed')} />
      </div>
    );
  }

  const items: DescriptionsProps['items'] = [
    {
      key: 'listen',
      label: t('pages.settings.edge.listenLabel'),
      children: <span>{`${data.listen || '—'}:${data.port || '—'}`}</span>,
    },
    {
      key: 'serverNames',
      label: t('pages.settings.edge.serverNamesLabel'),
      children: <TagList values={data.serverNames} testId="edge-server-names" />,
    },
    {
      key: 'panelUpstream',
      label: t('pages.settings.edge.panelUpstream'),
      children: <span>{data.panelUpstream || '—'}</span>,
    },
    {
      key: 'subPrefixes',
      label: t('pages.settings.edge.subPrefixes'),
      children: <span data-testid="edge-sub-prefixes">{String(data.subPrefixes)}</span>,
    },
    {
      key: 'certDomains',
      label: t('pages.settings.edge.certDomains'),
      children: <TagList values={data.certDomains} testId="edge-cert-domains" />,
    },
    {
      key: 'certExpiry',
      label: t('pages.settings.edge.certExpiry'),
      children: <CertExpiryCell notAfter={data.certNotAfter} />,
    },
  ];

  // The binary is the one prerequisite the perimeter cannot work around, so its
  // version and path are reported here whenever it is present.
  if (data.nginxInstalled) {
    items.push(
      {
        key: 'nginxVersion',
        label: t('pages.settings.edge.nginxVersionLabel'),
        children: <span data-testid="edge-nginx-version">{data.nginxVersion || '—'}</span>,
      },
      {
        key: 'nginxBin',
        label: t('pages.settings.edge.nginxBinLabel'),
        children: <span data-testid="edge-nginx-bin">{data.nginxBin || '—'}</span>,
      },
    );
  }

  return (
    <>
      <Space size={8} wrap style={{ marginBottom: 12 }}>
        <Tag color={data.running ? 'green' : 'red'} data-testid="edge-running">
          {data.running ? t('pages.settings.edge.running') : t('pages.settings.edge.notRunning')}
        </Tag>
        <Tag color={data.configValid ? 'green' : 'red'} data-testid="edge-config-valid">
          {data.configValid
            ? t('pages.settings.edge.configValid')
            : t('pages.settings.edge.configInvalid')}
        </Tag>
        {!data.enabled && <Tag>{t('pages.settings.edge.disabledTag')}</Tag>}
      </Space>

      {/* An invalid config blocks everything else, so the backend's own reason is
          quoted instead of a generic failure message. */}
      {data.message && (
        <div
          data-testid={data.configValid ? 'edge-status-message' : 'edge-config-invalid'}
          style={{ marginBottom: 12 }}
        >
          <Alert
            type={data.configValid ? 'warning' : 'error'}
            showIcon
            title={
              data.configValid
                ? t('pages.settings.edge.statusMessage')
                : t('pages.settings.edge.configInvalid')
            }
            description={<pre className="edge-error-detail">{data.message}</pre>}
          />
        </div>
      )}

      <Descriptions size="small" bordered column={1} items={items} />
    </>
  );
}

export default function EdgeStatusCard() {
  const { t } = useTranslation();

  return (
    <div className="edge-status">
      <Typography.Title level={5}>{t('pages.settings.edge.statusTitle')}</Typography.Title>
      <Typography.Paragraph type="secondary">
        {t('pages.settings.edge.statusDesc')}
      </Typography.Paragraph>
      <StatusBody />
    </div>
  );
}
