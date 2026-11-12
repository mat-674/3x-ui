import { useTranslation } from 'react-i18next';
import { Collapse, Typography } from 'antd';

import type { EdgeInstallReport } from '@/api/queries/useEdgeQueries';

interface InstallStepsProps {
  report: EdgeInstallReport;
}

/* The distribution's own commands ran as root on the operator's machine, so they
   stay visible: a silent "installed" is not auditable. */
export default function InstallSteps({ report }: InstallStepsProps) {
  const { t } = useTranslation();
  if (report.steps.length === 0) return null;

  return (
    <div data-testid="edge-install-steps" style={{ marginTop: 8 }}>
      <Collapse
        size="small"
        defaultActiveKey={['steps']}
        items={[
          {
            key: 'steps',
            label: t('pages.settings.edge.installStepsTitle'),
            forceRender: true,
            children: (
              <>
                {report.packageManager && (
                  <Typography.Paragraph type="secondary" style={{ marginBottom: 8 }}>
                    {t('pages.settings.edge.installStepsVia', { pm: report.packageManager })}
                  </Typography.Paragraph>
                )}
                {report.steps.map((step, index) => (
                  <pre key={index} className="edge-install-step" data-testid="edge-install-step">
                    {step}
                  </pre>
                ))}
              </>
            ),
          },
        ]}
      />
    </div>
  );
}
