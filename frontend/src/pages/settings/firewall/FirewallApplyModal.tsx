import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Form, InputNumber, Modal } from 'antd';
import { FormProvider, useForm } from 'react-hook-form';

import { FormField, rhfZodValidate } from '@/components/form/rhf';
import {
  applyDraftOf,
  FirewallApplySchema,
  type FirewallApplyDraft,
  type FirewallApplyValues,
} from './firewallApplyForm';

interface FirewallApplyModalProps {
  open: boolean;
  sshPort: number;
  error: string;
  pending: boolean;
  onCancel: () => void;
  onApply: (values: FirewallApplyValues) => void;
}

export default function FirewallApplyModal({
  open,
  sshPort,
  error,
  pending,
  onCancel,
  onApply,
}: FirewallApplyModalProps) {
  const { t } = useTranslation();
  const methods = useForm<FirewallApplyDraft>({ defaultValues: applyDraftOf(sshPort) });

  // Every open starts from the port this tab holds now, never from the last apply.
  useEffect(() => {
    if (open) methods.reset(applyDraftOf(sshPort));
  }, [open, sshPort, methods]);

  function submit(values: FirewallApplyDraft) {
    const parsed = FirewallApplySchema.safeParse(values);
    if (!parsed.success) return;
    onApply(parsed.data);
  }

  return (
    <Modal
      open={open}
      title={t('pages.settings.firewall.applyTitle')}
      okText={t('pages.settings.firewall.apply')}
      cancelText={t('cancel')}
      mask={{ closable: false }}
      okButtonProps={{ danger: true }}
      confirmLoading={pending}
      onOk={methods.handleSubmit(submit)}
      onCancel={onCancel}
    >
      <Alert
        type="warning"
        showIcon
        title={t('pages.settings.firewall.applyWarning')}
        style={{ marginBottom: 12 }}
      />

      <FormProvider {...methods}>
        <Form layout="vertical">
          <FormField
            name="sshPort"
            label={t('pages.settings.firewall.applySshPortLabel')}
            required
            rules={{ validate: rhfZodValidate(FirewallApplySchema.shape.sshPort) }}
          >
            <InputNumber min={1} max={65535} style={{ width: '100%' }} placeholder="22" />
          </FormField>
          <FormField
            name="confirmInSeconds"
            label={t('pages.settings.firewall.applyWindowLabel')}
            required
            extra={t('pages.settings.firewall.applyWindowHint')}
            rules={{ validate: rhfZodValidate(FirewallApplySchema.shape.confirmInSeconds) }}
          >
            <InputNumber min={1} max={3600} style={{ width: '100%' }} />
          </FormField>
        </Form>
      </FormProvider>

      {error && (
        <div data-testid="firewall-apply-error" style={{ marginTop: 12 }}>
          <Alert
            type="error"
            showIcon
            title={t('pages.settings.firewall.applyFailed')}
            description={<pre className="firewall-error-detail">{error}</pre>}
          />
        </div>
      )}
    </Modal>
  );
}
