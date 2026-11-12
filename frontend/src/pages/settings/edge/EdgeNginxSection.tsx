import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, message } from 'antd';
import { DownloadOutlined, ReloadOutlined } from '@ant-design/icons';

import {
  useEdgeStatus,
  useInstallEdgeNginx,
  type EdgeInstallReport,
} from '@/api/queries/useEdgeQueries';
import InstallSteps from './InstallSteps';

/* The binary is a hard prerequisite, not a detail: without it Apply cannot load
   anything, so the tab states that before the operator reaches the buttons. */
export default function EdgeNginxSection() {
  const { t } = useTranslation();
  const status = useEdgeStatus();
  const install = useInstallEdgeNginx();
  const [messageApi, messageContextHolder] = message.useMessage();
  const [report, setReport] = useState<EdgeInstallReport | null>(null);
  const [error, setError] = useState('');

  async function installNow(): Promise<void> {
    setError('');
    try {
      const installed = await install.mutateAsync();
      setReport(installed);
      if (installed.alreadyInstalled) {
        messageApi.info(t('pages.settings.edge.nginxAlreadyInstalled'));
      } else {
        messageApi.success(
          t('pages.settings.edge.installSucceeded', { version: installed.version }),
        );
      }
    } catch (installError) {
      setError((installError as Error).message || t('pages.settings.edge.installFailed'));
    }
  }

  const data = status.data;

  // Nothing to say while the state is unknown, and nothing to add once it is fine:
  // the status card carries version and path.
  if (!data) return messageContextHolder;
  if (data.nginxInstalled) {
    return (
      <>
        {messageContextHolder}
        {report && <InstallSteps report={report} />}
      </>
    );
  }

  if (!data.nginxInstallable) {
    return (
      <div data-testid="edge-nginx-blocked" style={{ marginBottom: 8 }}>
        <Alert
          type="error"
          showIcon
          title={t('pages.settings.edge.nginxBlockedTitle')}
          description={
            <pre className="edge-error-detail">
              {data.nginxHint || t('pages.settings.edge.nginxBlockedDesc')}
            </pre>
          }
          action={
            <Button onClick={() => void status.refetch()} icon={<ReloadOutlined aria-hidden />}>
              {t('pages.settings.edge.nginxRecheck')}
            </Button>
          }
        />
      </div>
    );
  }

  return (
    <div style={{ marginBottom: 8 }}>
      {messageContextHolder}
      <div data-testid="edge-nginx-missing">
        <Alert
          type="warning"
          showIcon
          title={t('pages.settings.edge.nginxMissingTitle')}
          description={t('pages.settings.edge.nginxMissingDesc')}
          action={
            <Button
              type="primary"
              loading={install.isPending}
              onClick={() => void installNow()}
              icon={<DownloadOutlined aria-hidden />}
            >
              {t('pages.settings.edge.installNginx')}
            </Button>
          }
        />
      </div>

      {error && (
        <div data-testid="edge-nginx-install-error" style={{ marginTop: 8 }}>
          <Alert
            type="error"
            showIcon
            title={t('pages.settings.edge.installFailed')}
            description={<pre className="edge-error-detail">{error}</pre>}
          />
        </div>
      )}

      {report && <InstallSteps report={report} />}
    </div>
  );
}
