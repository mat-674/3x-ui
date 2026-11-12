import { useTranslation } from 'react-i18next';
import { Alert, Descriptions, Space, Tag, Typography } from 'antd';
import type { DescriptionsProps } from 'antd';

import { useFirewallStatus } from '@/api/queries/useFirewallQueries';

function PortTags({ ports, testId }: { ports: number[]; testId: string }) {
  if (ports.length === 0) return <span>—</span>;
  return (
    <Space size={4} wrap data-testid={testId}>
      {ports.map((port) => (
        <Tag key={port}>{String(port)}</Tag>
      ))}
    </Space>
  );
}

function deadlineStamp(deadline: string | null | undefined): string {
  if (!deadline) return '—';
  const stamp = new Date(deadline);
  return Number.isNaN(stamp.getTime()) ? deadline : stamp.toLocaleString();
}

function StatusBody() {
  const { t } = useTranslation();
  const { data, isPending, isError } = useFirewallStatus();

  if (isPending && !data) {
    return <Typography.Text type="secondary">{t('loading')}</Typography.Text>;
  }
  if (isError || !data) {
    return (
      <div data-testid="firewall-status-error">
        <Alert type="error" showIcon title={t('pages.settings.firewall.statusLoadFailed')} />
      </div>
    );
  }

  const items: DescriptionsProps['items'] = [
    {
      key: 'table',
      label: t('pages.settings.firewall.table'),
      children: <span data-testid="firewall-table">{data.table || '—'}</span>,
    },
    {
      key: 'sshPort',
      label: t('pages.settings.firewall.sshPortLabel'),
      children: <span>{data.sshPort > 0 ? String(data.sshPort) : '—'}</span>,
    },
    {
      key: 'manageSsh',
      label: t('pages.settings.firewall.manageSshLabel'),
      children: <span>{data.manageSsh ? t('enabled') : t('disabled')}</span>,
    },
    {
      key: 'allowedTcp',
      label: t('pages.settings.firewall.allowedTcp'),
      children: <PortTags ports={data.allowedTcp} testId="firewall-allowed-tcp" />,
    },
    {
      key: 'allowedUdp',
      label: t('pages.settings.firewall.allowedUdp'),
      children: <PortTags ports={data.allowedUdp} testId="firewall-allowed-udp" />,
    },
  ];
  if (data.pendingRollback) {
    items.push({
      key: 'rollbackDeadline',
      label: t('pages.settings.firewall.rollbackDeadline'),
      children: (
        <span data-testid="firewall-rollback-deadline">{deadlineStamp(data.rollbackDeadline)}</span>
      ),
    });
  }

  return (
    <>
      <Space size={8} wrap style={{ marginBottom: 12 }}>
        <Tag color={data.loaded ? 'green' : 'red'} data-testid="firewall-loaded">
          {data.loaded
            ? t('pages.settings.firewall.loaded')
            : t('pages.settings.firewall.notLoaded')}
        </Tag>
        {!data.manageSsh && <Tag>{t('pages.settings.firewall.manageSshOffTag')}</Tag>}
        {data.pendingRollback && (
          <Tag color="orange" data-testid="firewall-pending-rollback">
            {t('pages.settings.firewall.pendingRollbackTag')}
          </Tag>
        )}
      </Space>

      {/* The backend's own words: a ruleset that is not loaded, or nft that is
          missing, is exactly the state the operator has to act on. */}
      {data.message && (
        <div data-testid="firewall-status-message" style={{ marginBottom: 12 }}>
          <Alert
            type={data.loaded ? 'warning' : 'error'}
            showIcon
            title={t('pages.settings.firewall.statusMessage')}
            description={<pre className="firewall-error-detail">{data.message}</pre>}
          />
        </div>
      )}

      <Descriptions size="small" bordered column={1} items={items} />
    </>
  );
}

export default function FirewallStatusCard() {
  const { t } = useTranslation();

  return (
    <div className="firewall-status">
      <Typography.Title level={5}>{t('pages.settings.firewall.statusTitle')}</Typography.Title>
      <Typography.Paragraph type="secondary">
        {t('pages.settings.firewall.statusDesc')}
      </Typography.Paragraph>
      <StatusBody />
    </div>
  );
}
