import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Input, Modal, Space, Typography, message } from 'antd';
import { CodeOutlined, StopOutlined, ThunderboltOutlined } from '@ant-design/icons';

import {
  useApplyEdge,
  useEdgeConfig,
  useEdgeStatus,
  useInstallEdgeNginx,
  useStopEdge,
  type EdgeInstallReport,
} from '@/api/queries/useEdgeQueries';
import type { AllSetting } from '@/models/setting';
import type { EdgePatch } from './edgePatch';
import InstallSteps from './InstallSteps';

interface EdgeActionsProps {
  patch: EdgePatch;
  saveSetting: (payload: Partial<AllSetting> & Record<string, unknown>) => Promise<unknown>;
}

const NGINX_MISSING = /nginx is not installed/i;

export default function EdgeActions({ patch, saveSetting }: EdgeActionsProps) {
  const { t } = useTranslation();
  const [modal, modalContextHolder] = Modal.useModal();
  const [messageApi, messageContextHolder] = message.useMessage();
  const status = useEdgeStatus();
  const apply = useApplyEdge();
  const install = useInstallEdgeNginx();
  const stop = useStopEdge();
  const [applyError, setApplyError] = useState('');
  const [installError, setInstallError] = useState('');
  const [installReport, setInstallReport] = useState<EdgeInstallReport | null>(null);
  const [stopError, setStopError] = useState('');
  const [configOpen, setConfigOpen] = useState(false);
  const config = useEdgeConfig(configOpen);

  const installed = status.data?.nginxInstalled ?? false;
  const installable = status.data?.nginxInstallable ?? false;
  // Before the status lands, do not guess: Apply itself reports the truth.
  const settled = status.data !== undefined;
  const needsInstall = settled && !installed && installable;
  const blocked = settled && !installed && !installable;

  /* Installing is not a step of its own in this flow: the operator asked for a
     working perimeter, so the binary is fetched first and applied straight after. */
  async function runApply(): Promise<void> {
    setApplyError('');
    setInstallError('');
    try {
      const saved = (await saveSetting({ ...patch })) as
        | { success?: boolean; msg?: string }
        | undefined;
      if (saved && saved.success === false) {
        setApplyError(saved.msg || t('pages.settings.edge.applyFailed'));
        return;
      }
    } catch (error) {
      setApplyError((error as Error).message || t('pages.settings.edge.applyFailed'));
      return;
    }

    if (needsInstall) {
      try {
        setInstallReport(await install.mutateAsync());
      } catch (error) {
        setInstallError((error as Error).message || t('pages.settings.edge.installFailed'));
        return;
      }
    }

    try {
      const applied = await apply.mutateAsync();
      messageApi.success(applied.msg || t('pages.settings.edge.applied'));
    } catch (error) {
      setApplyError((error as Error).message || t('pages.settings.edge.applyFailed'));
    }
  }

  function confirmApply() {
    modal.confirm({
      title: t('pages.settings.edge.applyConfirmTitle'),
      content: t('pages.settings.edge.applyConfirm'),
      okText: t('pages.settings.edge.apply'),
      cancelText: t('cancel'),
      okButtonProps: { danger: true },
      onOk: runApply,
    });
  }

  async function runStop(): Promise<void> {
    setStopError('');
    try {
      await stop.mutateAsync();
      messageApi.success(t('pages.settings.edge.stopped'));
    } catch (error) {
      setStopError((error as Error).message || t('pages.settings.edge.stopFailed'));
    }
  }

  function confirmStop() {
    modal.confirm({
      title: t('pages.settings.edge.stopConfirmTitle'),
      content: t('pages.settings.edge.stopConfirm'),
      okText: t('pages.settings.edge.stop'),
      cancelText: t('cancel'),
      okButtonProps: { danger: true },
      onOk: runStop,
    });
  }

  const nginxMissing = NGINX_MISSING.test(applyError);

  return (
    <div className="edge-actions">
      {messageContextHolder}
      {modalContextHolder}
      <Typography.Title level={5}>{t('pages.settings.edge.actionsTitle')}</Typography.Title>
      <Typography.Paragraph type="secondary">
        {t('pages.settings.edge.actionsDesc')}
      </Typography.Paragraph>

      {installError && (
        <div data-testid="edge-install-error" style={{ marginBottom: 12 }}>
          <Alert
            type="error"
            showIcon
            title={t('pages.settings.edge.installFailed')}
            description={<pre className="edge-error-detail">{installError}</pre>}
          />
        </div>
      )}
      {applyError && (
        <div data-testid="edge-apply-error" style={{ marginBottom: 12 }}>
          <Alert
            type="error"
            showIcon
            title={
              nginxMissing
                ? t('pages.settings.edge.nginxMissingApplyTitle')
                : t('pages.settings.edge.applyFailed')
            }
            description={<pre className="edge-error-detail">{applyError}</pre>}
          />
        </div>
      )}
      {stopError && (
        <div data-testid="edge-stop-error" style={{ marginBottom: 12 }}>
          <Alert
            type="error"
            showIcon
            title={t('pages.settings.edge.stopFailed')}
            description={<pre className="edge-error-detail">{stopError}</pre>}
          />
        </div>
      )}

      <Space size={8} wrap>
        <Button
          type="primary"
          icon={<ThunderboltOutlined aria-hidden />}
          loading={apply.isPending || install.isPending}
          disabled={blocked}
          onClick={confirmApply}
        >
          {needsInstall ? t('pages.settings.edge.installAndApply') : t('pages.settings.edge.apply')}
        </Button>
        <Button
          danger
          icon={<StopOutlined aria-hidden />}
          loading={stop.isPending}
          onClick={confirmStop}
        >
          {t('pages.settings.edge.stop')}
        </Button>
        <Button
          icon={<CodeOutlined aria-hidden />}
          aria-expanded={configOpen}
          onClick={() => setConfigOpen((open) => !open)}
        >
          {configOpen ? t('pages.settings.edge.hideConfig') : t('pages.settings.edge.showConfig')}
        </Button>
      </Space>

      {installReport && <InstallSteps report={installReport} />}

      {configOpen && (
        <div className="edge-config" style={{ marginTop: 12 }}>
          <Typography.Text strong>{t('pages.settings.edge.configTitle')}</Typography.Text>
          <Typography.Paragraph type="secondary" style={{ marginBottom: 8 }}>
            {t('pages.settings.edge.configDesc')}
          </Typography.Paragraph>
          {config.isError && (
            <div data-testid="edge-config-error" style={{ marginBottom: 8 }}>
              <Alert type="error" showIcon title={t('pages.settings.edge.configLoadFailed')} />
            </div>
          )}
          <Input.TextArea
            readOnly
            value={config.data ?? ''}
            aria-label={t('pages.settings.edge.configTitle')}
            autoSize={{ minRows: 8, maxRows: 24 }}
            className="edge-config-text"
          />
        </div>
      )}
    </div>
  );
}
