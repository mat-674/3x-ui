import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Empty,
  Input,
  Popconfirm,
  Space,
  Table,
  Tag,
  Typography,
  Upload,
  message,
} from 'antd';
import type { TableColumnsType } from 'antd';
import { InboxOutlined, ThunderboltOutlined } from '@ant-design/icons';

import { SizeFormatter } from '@/utils';
import {
  useActivateFrontSite,
  useDeleteFrontSite,
  useFrontSites,
  useUploadFrontSite,
  type FrontSite,
} from './frontApi';

interface FrontSitesSectionProps {
  /** frontSiteId: the front honours this selection even before the row is active. */
  frontSiteId: number;
  onSelect: (id: number) => void;
}

export default function FrontSitesSection({ frontSiteId, onSelect }: FrontSitesSectionProps) {
  const { t } = useTranslation();
  const sites = useFrontSites();
  const upload = useUploadFrontSite();
  const activate = useActivateFrontSite();
  const remove = useDeleteFrontSite();
  const [messageApi, messageContextHolder] = message.useMessage();
  const [siteName, setSiteName] = useState('');

  const rows = useMemo(() => sites.data ?? [], [sites.data]);

  const columns: TableColumnsType<FrontSite> = [
    {
      title: t('pages.settings.front.siteName'),
      dataIndex: 'name',
      key: 'name',
      render: (_: unknown, row) => (
        <Space size={6}>
          <span>{row.name}</span>
          {row.active && <Tag color="green">{t('pages.settings.front.siteActiveTag')}</Tag>}
          {/* frontSiteId is honoured even before the row is activated. */}
          {!row.active && row.id === frontSiteId && (
            <Tag color="blue">{t('pages.settings.front.siteSelectedTag')}</Tag>
          )}
        </Space>
      ),
    },
    {
      title: t('pages.settings.front.siteEntry'),
      dataIndex: 'entryFile',
      key: 'entryFile',
      render: (_: unknown, row) => row.entryFile || '—',
    },
    {
      title: t('pages.settings.front.siteFiles'),
      dataIndex: 'fileCount',
      key: 'fileCount',
      width: 100,
    },
    {
      title: t('pages.settings.front.siteSize'),
      dataIndex: 'bytes',
      key: 'bytes',
      width: 120,
      render: (_: unknown, row) => SizeFormatter.sizeFormat(row.bytes),
    },
    {
      title: '',
      key: 'actions',
      width: 200,
      render: (_: unknown, row) => {
        const id = row.id;
        return (
          <Space size={6}>
            <Button
              size="small"
              icon={<ThunderboltOutlined aria-hidden />}
              disabled={row.active}
              loading={activate.isPending && activate.variables === id}
              onClick={() =>
                activate.mutate(id, {
                  // Keep the saved setting in step with the flag the backend just set.
                  onSuccess: () => onSelect(id),
                })
              }
            >
              {t('pages.settings.front.siteActivate')}
            </Button>
            <Popconfirm
              title={t('pages.settings.front.siteDeleteConfirm', { name: row.name })}
              okText={t('delete')}
              cancelText={t('cancel')}
              okButtonProps={{ danger: true }}
              // The backend refuses to delete the active site, so do not offer it.
              disabled={row.active}
              onConfirm={() => remove.mutate(id)}
            >
              <Button
                size="small"
                danger
                disabled={row.active}
                loading={remove.isPending && remove.variables === id}
              >
                {t('delete')}
              </Button>
            </Popconfirm>
          </Space>
        );
      },
    },
  ];

  return (
    <div className="front-section">
      {messageContextHolder}
      <Typography.Title level={5}>{t('pages.settings.front.sitesTitle')}</Typography.Title>
      <Typography.Paragraph type="secondary">
        {t('pages.settings.front.sitesDesc')}
      </Typography.Paragraph>

      {sites.isError && (
        <Alert type="error" showIcon title={t('pages.settings.front.sitesLoadFailed')} />
      )}

      <Table<FrontSite>
        size="small"
        rowKey={(row) => String(row.id)}
        loading={sites.isPending}
        columns={columns}
        dataSource={rows}
        pagination={false}
        rowClassName={(row) => (row.active ? 'front-site-row-active' : '')}
        locale={{ emptyText: <Empty description={t('pages.settings.front.sitesEmpty')} /> }}
      />

      <Space orientation="vertical" size={8} style={{ width: '100%', marginTop: 16 }}>
        <Typography.Text strong>{t('pages.settings.front.siteUpload')}</Typography.Text>
        <Input
          value={siteName}
          placeholder={t('pages.settings.front.siteUploadNamePlaceholder')}
          aria-label={t('pages.settings.front.siteUploadName')}
          onChange={(e) => setSiteName(e.target.value)}
          style={{ maxWidth: 320 }}
        />
        <Upload.Dragger
          accept=".zip"
          maxCount={1}
          showUploadList={false}
          disabled={upload.isPending}
          beforeUpload={(file) => {
            if (!file.name.toLowerCase().endsWith('.zip')) {
              messageApi.error(t('pages.settings.front.siteUploadOnlyZip'));
              return Upload.LIST_IGNORE;
            }
            if (siteName.trim() === '') {
              messageApi.error(t('pages.settings.front.siteUploadNoName'));
              return Upload.LIST_IGNORE;
            }
            return true;
          }}
          customRequest={(options) => {
            const dropped = options.file;
            if (!(dropped instanceof File)) {
              options.onError?.(new Error('front site upload needs a file'));
              return;
            }
            upload.mutate(
              { name: siteName.trim(), file: dropped },
              {
                onSuccess: () => {
                  setSiteName('');
                  options.onSuccess?.({});
                },
                onError: (error) => options.onError?.(error),
              },
            );
          }}
        >
          <p className="ant-upload-drag-icon">
            <InboxOutlined aria-hidden />
          </p>
          <p className="ant-upload-text">{t('pages.settings.front.siteUploadHint')}</p>
          <p className="ant-upload-hint">{t('pages.settings.front.siteUploadNameHint')}</p>
        </Upload.Dragger>
      </Space>
    </div>
  );
}
