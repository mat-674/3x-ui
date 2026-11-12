import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Space, Typography } from 'antd';
import { CheckCircleOutlined } from '@ant-design/icons';

import { FIREWALL_STATUS_KEY, useConfirmFirewall } from '@/api/queries/useFirewallQueries';

interface FirewallRollbackBannerProps {
  deadlineId: string;
  /* Epoch ms, not a remaining count: the window belongs to the backend, so a
     refetch or a re-render must not restart the countdown. */
  deadlineAt: number;
  onKept: () => void;
}

export default function FirewallRollbackBanner({
  deadlineId,
  deadlineAt,
  onKept,
}: FirewallRollbackBannerProps) {
  const { t } = useTranslation();
  const [now, setNow] = useState(() => Date.now());
  const [error, setError] = useState('');
  const queryClient = useQueryClient();
  const confirm = useConfirmFirewall();
  const refreshed = useRef(false);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);

  // An unreadable deadline keeps the button usable: confirming a stale id is
  // refused by the backend, while a disabled button strands the operator.
  const known = Number.isFinite(deadlineAt);
  const remaining = known ? Math.max(0, Math.ceil((deadlineAt - now) / 1000)) : null;
  const expired = remaining === 0;

  useEffect(() => {
    if (!expired || refreshed.current) return;
    refreshed.current = true;
    void queryClient.invalidateQueries({ queryKey: FIREWALL_STATUS_KEY });
  }, [expired, queryClient]);

  function keepRules() {
    setError('');
    confirm.mutate(
      { deadlineId },
      {
        onSuccess: onKept,
        onError: (failure) => setError((failure as Error).message),
      },
    );
  }

  const countdown = expired
    ? t('pages.settings.firewall.rollbackExpired')
    : remaining === null
      ? t('pages.settings.firewall.pendingRollbackTag')
      : t('pages.settings.firewall.rollbackTitle', { seconds: remaining });

  return (
    <div className="firewall-rollback" data-testid="firewall-rollback">
      <Alert
        type={expired ? 'error' : 'warning'}
        showIcon
        title={<span data-testid="firewall-countdown">{countdown}</span>}
        description={expired ? undefined : <span>{t('pages.settings.firewall.rollbackDesc')}</span>}
      />

      <Space size={12} wrap style={{ marginTop: 12 }}>
        <Button
          type="primary"
          size="large"
          icon={<CheckCircleOutlined aria-hidden />}
          loading={confirm.isPending}
          onClick={keepRules}
          data-testid="firewall-keep-rules"
        >
          {t('pages.settings.firewall.keepRules')}
        </Button>
        <Typography.Text type="secondary">
          {t('pages.settings.firewall.deadlineId')}:{' '}
          <Typography.Text code>{deadlineId || '—'}</Typography.Text>
        </Typography.Text>
      </Space>

      {error && (
        <div data-testid="firewall-keep-error" style={{ marginTop: 12 }}>
          <Alert
            type="error"
            showIcon
            title={t('pages.settings.firewall.keepFailed')}
            description={<pre className="firewall-error-detail">{error}</pre>}
          />
        </div>
      )}
    </div>
  );
}
