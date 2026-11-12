import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Empty,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import type { TableColumnsType } from 'antd';
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { FormProvider, useForm, useWatch } from 'react-hook-form';

import { FormField, rhfZodValidate } from '@/components/form/rhf';
import {
  splitDomains,
  useDeleteFrontCert,
  useFrontCerts,
  useReloadFrontCerts,
  useSaveFrontCert,
  type FrontCert,
  type FrontCertInput,
} from './frontApi';
import { certExpiryLevel, daysUntilExpiry } from './certExpiry';
import { EMPTY_CERT_FORM, FrontCertFormSchema, type FrontCertFormValues } from './certForm';

function CertExpiryCell({ cert }: { cert: FrontCert }) {
  const { t } = useTranslation();
  const days = daysUntilExpiry(cert.notAfter);
  const level = certExpiryLevel(days);
  const stamp = cert.notAfter ? new Date(cert.notAfter).toLocaleString() : '';
  const warnTestId = cert.id === undefined ? undefined : `front-cert-expiry-${cert.id}`;

  if (days === null) {
    return <span>{t('pages.settings.front.certNoExpiry')}</span>;
  }
  if (level === 'expired') {
    return (
      <span data-testid={warnTestId}>
        {stamp}
        {' · '}
        <Tag color="red">{t('pages.settings.front.certExpired', { days: Math.abs(days) })}</Tag>
      </span>
    );
  }
  if (level === 'expiring') {
    return (
      <span data-testid={warnTestId}>
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

export default function FrontCertsSection() {
  const { t } = useTranslation();
  const certs = useFrontCerts();
  const reload = useReloadFrontCerts();
  const remove = useDeleteFrontCert();
  const save = useSaveFrontCert();
  const [messageApi, messageContextHolder] = message.useMessage();
  const [modalOpen, setModalOpen] = useState(false);

  const methods = useForm<FrontCertFormValues>({ defaultValues: EMPTY_CERT_FORM });
  const issuer = useWatch({ control: methods.control, name: 'issuer' });

  const rows = useMemo(() => certs.data ?? [], [certs.data]);
  // Either signal is enough: an empty list, or a reload that loaded nothing.
  const noCert = (certs.isSuccess && rows.length === 0) || reload.data?.hasCertificate === false;

  const columns: TableColumnsType<FrontCert> = [
    {
      title: t('pages.settings.front.certDomains'),
      dataIndex: 'domains',
      key: 'domains',
      render: (_: unknown, row) => {
        const domains = splitDomains(row.domains);
        return domains.length === 0 ? (
          <span>{t('pages.settings.front.certNoDomains')}</span>
        ) : (
          <Space size={4} wrap>
            {domains.map((domain) => (
              <Tag key={domain}>{domain}</Tag>
            ))}
          </Space>
        );
      },
    },
    {
      title: t('pages.settings.front.certIssuer'),
      dataIndex: 'issuer',
      key: 'issuer',
      render: (_: unknown, row) =>
        row.issuer === 'acme'
          ? t('pages.settings.front.certIssuerAcme')
          : t('pages.settings.front.certIssuerFile'),
    },
    {
      title: t('pages.settings.front.certExpires'),
      dataIndex: 'notAfter',
      key: 'notAfter',
      render: (_: unknown, row) => <CertExpiryCell cert={row} />,
    },
    {
      title: t('pages.settings.front.certLastError'),
      dataIndex: 'lastError',
      key: 'lastError',
      render: (_: unknown, row) => row.lastError || '—',
    },
    {
      title: '',
      key: 'actions',
      width: 96,
      render: (_: unknown, row) => {
        const id = row.id;
        if (id === undefined) return null;
        return (
          <Popconfirm
            title={t('pages.settings.front.certDeleteConfirm', {
              domains: splitDomains(row.domains).join(', '),
            })}
            okText={t('delete')}
            cancelText={t('cancel')}
            okButtonProps={{ danger: true }}
            onConfirm={() => remove.mutate(id)}
          >
            <Button size="small" danger>
              {t('delete')}
            </Button>
          </Popconfirm>
        );
      },
    },
  ];

  function submitCert(values: FrontCertFormValues) {
    const input: FrontCertInput = {
      domains: values.domains,
      certPath: values.certPath,
      keyPath: values.keyPath,
      issuer: values.issuer,
      acmeEmail: values.issuer === 'acme' ? (values.acmeEmail ?? '') : '',
    };
    save.mutate(input, {
      onSuccess: () => {
        setModalOpen(false);
        methods.reset(EMPTY_CERT_FORM);
      },
    });
  }

  return (
    <div className="front-section">
      {messageContextHolder}
      <Typography.Title level={5}>{t('pages.settings.front.certsTitle')}</Typography.Title>
      <Typography.Paragraph type="secondary">
        {t('pages.settings.front.certsDesc')}
      </Typography.Paragraph>

      {certs.isError && (
        <Alert type="error" showIcon title={t('pages.settings.front.certsLoadFailed')} />
      )}

      {/* A front without material terminates no handshake: this cannot be a hint. */}
      {noCert && (
        <div data-testid="front-no-cert">
          <Alert type="error" showIcon title={t('pages.settings.front.noCert')} />
        </div>
      )}

      <Space style={{ marginBottom: 12 }} wrap>
        <Button icon={<PlusOutlined aria-hidden />} onClick={() => setModalOpen(true)}>
          {t('pages.settings.front.addCert')}
        </Button>
        <Button
          icon={<ReloadOutlined aria-hidden />}
          loading={reload.isPending}
          onClick={() =>
            reload.mutate(undefined, {
              onSuccess: (report) => {
                if (report.hasCertificate === false) {
                  messageApi.error(t('pages.settings.front.noCert'));
                  return;
                }
                messageApi.success(t('pages.settings.front.certReloaded'));
              },
              onError: () => messageApi.error(t('pages.settings.front.certsLoadFailed')),
            })
          }
        >
          {t('pages.settings.front.certReload')}
        </Button>
      </Space>

      <Table<FrontCert>
        size="small"
        rowKey={(row) => String(row.id ?? row.domains)}
        loading={certs.isPending}
        columns={columns}
        dataSource={rows}
        pagination={false}
        rowClassName={(row) => {
          const level = certExpiryLevel(daysUntilExpiry(row.notAfter));
          if (level === 'expired') return 'front-cert-row-expired';
          if (level === 'expiring') return 'front-cert-row-warn';
          return '';
        }}
        locale={{ emptyText: <Empty description={t('pages.settings.front.certsEmpty')} /> }}
      />

      <Modal
        open={modalOpen}
        title={`+ ${t('pages.settings.front.addCert')}`}
        okText={t('save')}
        cancelText={t('cancel')}
        mask={{ closable: false }}
        confirmLoading={save.isPending}
        onOk={methods.handleSubmit(submitCert)}
        onCancel={() => setModalOpen(false)}
      >
        <FormProvider {...methods}>
          <Form layout="vertical">
            <FormField
              label={t('pages.settings.front.certDomains')}
              name="domains"
              required
              extra={t('pages.settings.front.certDomainsHint')}
              rules={{ validate: rhfZodValidate(FrontCertFormSchema.shape.domains) }}
            >
              <Input placeholder="panel.example.com, *.example.com" />
            </FormField>
            <FormField
              label={t('pages.settings.front.certPath')}
              name="certPath"
              required
              rules={{ validate: rhfZodValidate(FrontCertFormSchema.shape.certPath) }}
            >
              <Input placeholder="/etc/x-ui/front/certs/panel.crt" />
            </FormField>
            <FormField
              label={t('pages.settings.front.certKeyPath')}
              name="keyPath"
              required
              rules={{ validate: rhfZodValidate(FrontCertFormSchema.shape.keyPath) }}
            >
              <Input placeholder="/etc/x-ui/front/certs/panel.key" />
            </FormField>
            <FormField label={t('pages.settings.front.certIssuer')} name="issuer" required>
              <Select
                options={[
                  { value: 'file', label: t('pages.settings.front.certIssuerFile') },
                  { value: 'acme', label: t('pages.settings.front.certIssuerAcme') },
                ]}
              />
            </FormField>
            {issuer === 'acme' && (
              <FormField label={t('pages.settings.front.certAcmeEmail')} name="acmeEmail">
                <Input placeholder="admin@example.com" />
              </FormField>
            )}
          </Form>
        </FormProvider>
      </Modal>
    </div>
  );
}
