import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Input, Space, Typography, message } from 'antd';
import { CodeOutlined, ThunderboltOutlined } from '@ant-design/icons';

import {
  useApplyFirewall,
  useFirewallConfig,
  useFirewallStatus,
  type FirewallStatus,
} from '@/api/queries/useFirewallQueries';
import type { AllSetting } from '@/models/setting';
import { firewallPatchOf } from './firewallPatch';
import FirewallApplyModal from './FirewallApplyModal';
import FirewallRollbackBanner from './FirewallRollbackBanner';
import type { FirewallApplyValues } from './firewallApplyForm';

interface FirewallActionsProps {
  allSetting: AllSetting;
  saveSetting: (payload: Partial<AllSetting> & Record<string, unknown>) => Promise<unknown>;
}

interface PendingWindow {
  deadlineId: string;
  deadlineAt: number;
}

/* A rollback armed by an earlier apply survives a page reload, so the keep
   button has to come from the status too, not only from this session. */
function pendingWindowOf(status: FirewallStatus | undefined): PendingWindow | null {
  if (!status?.pendingRollback || !status.rollbackId) return null;
  const parsed = status.rollbackDeadline ? Date.parse(status.rollbackDeadline) : Number.NaN;
  return { deadlineId: status.rollbackId, deadlineAt: parsed };
}

export default function FirewallActions({ allSetting, saveSetting }: FirewallActionsProps) {
  const { t } = useTranslation();
  const [messageApi, messageContextHolder] = message.useMessage();
  const apply = useApplyFirewall();
  const status = useFirewallStatus();
  const [applyOpen, setApplyOpen] = useState(false);
  const [applyError, setApplyError] = useState('');
  const [applied, setApplied] = useState<PendingWindow | null>(null);
  const [keptId, setKeptId] = useState('');
  const [configOpen, setConfigOpen] = useState(false);
  const config = useFirewallConfig(configOpen);

  const fromStatus = pendingWindowOf(status.data);
  const rollback = applied ?? (fromStatus && fromStatus.deadlineId !== keptId ? fromStatus : null);

  async function runApply(values: FirewallApplyValues): Promise<void> {
    setApplyError('');
    try {
      // Apply reads the policy from the DB, so the port it must accept is saved first.
      const saved = (await saveSetting(firewallPatchOf(allSetting, values.sshPort))) as
        | { success?: boolean; msg?: string }
        | undefined;
      if (saved && saved.success === false) {
        setApplyError(saved.msg || t('pages.settings.firewall.applyFailed'));
        return;
      }
      const window = await apply.mutateAsync({ confirmInSeconds: values.confirmInSeconds });
      setApplyOpen(false);
      setApplied({
        deadlineId: window.deadlineId,
        deadlineAt: Date.now() + window.confirmInSeconds * 1000,
      });
      messageApi.success(t('pages.settings.firewall.applied'));
    } catch (error) {
      setApplyError((error as Error).message || t('pages.settings.firewall.applyFailed'));
    }
  }

  function keepRules(): void {
    if (rollback) setKeptId(rollback.deadlineId);
    setApplied(null);
    messageApi.success(t('pages.settings.firewall.kept'));
  }

  return (
    <div className="firewall-actions">
      {messageContextHolder}
      <Typography.Title level={5}>{t('pages.settings.firewall.actionsTitle')}</Typography.Title>
      <Typography.Paragraph type="secondary">
        {t('pages.settings.firewall.actionsDesc')}
      </Typography.Paragraph>

      {rollback && (
        <FirewallRollbackBanner
          deadlineId={rollback.deadlineId}
          deadlineAt={rollback.deadlineAt}
          onKept={keepRules}
        />
      )}

      <Space size={8} wrap>
        <Button
          type="primary"
          danger
          icon={<ThunderboltOutlined aria-hidden />}
          loading={apply.isPending}
          onClick={() => {
            setApplyError('');
            setApplyOpen(true);
          }}
        >
          {t('pages.settings.firewall.apply')}
        </Button>
        <Button
          icon={<CodeOutlined aria-hidden />}
          aria-expanded={configOpen}
          onClick={() => setConfigOpen((open) => !open)}
        >
          {configOpen
            ? t('pages.settings.firewall.hideConfig')
            : t('pages.settings.firewall.showConfig')}
        </Button>
      </Space>

      {configOpen && (
        <div className="firewall-config" style={{ marginTop: 12 }}>
          <Typography.Text strong>{t('pages.settings.firewall.configTitle')}</Typography.Text>
          <Typography.Paragraph type="secondary" style={{ marginBottom: 8 }}>
            {t('pages.settings.firewall.configDesc')}
          </Typography.Paragraph>
          {config.isError && (
            <div data-testid="firewall-config-error" style={{ marginBottom: 8 }}>
              <Alert type="error" showIcon title={t('pages.settings.firewall.configLoadFailed')} />
            </div>
          )}
          <Input.TextArea
            readOnly
            value={config.data ?? ''}
            aria-label={t('pages.settings.firewall.configTitle')}
            autoSize={{ minRows: 8, maxRows: 24 }}
            className="firewall-config-text"
          />
        </div>
      )}

      <FirewallApplyModal
        open={applyOpen}
        sshPort={allSetting.firewallSSHPort}
        error={applyError}
        pending={apply.isPending}
        onCancel={() => setApplyOpen(false)}
        onApply={runApply}
      />
    </div>
  );
}
