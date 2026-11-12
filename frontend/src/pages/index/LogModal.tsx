import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Checkbox,
  Divider,
  Form,
  Modal,
  Select,
  Tag,
  Tooltip,
  Typography,
  message,
} from 'antd';
import { DownloadOutlined, SyncOutlined } from '@ant-design/icons';

import { HttpUtil, FileManager, PromiseUtil, SizeFormatter } from '@/utils';
import { activateOnKey } from '@/utils/a11y';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { parseLogLine } from './logParse';
import {
  LOG_EXPORT_LINES,
  LOG_SOURCES_KEY,
  LOG_SOURCE_LABELS,
  LOG_SOURCE_ORDER,
  PANEL_LOG_LEVELS,
  PANEL_LOG_LEVEL_LABELS,
  downloadLogFile,
  fetchLogSources,
  type PanelLogLevel,
} from './logDiagnostics';
import './LogModal.css';

interface LogModalProps {
  open: boolean;
  onClose: () => void;
}

const AUTO_UPDATE_INTERVAL = 5000;

function isPanelLogLevel(value: unknown): value is PanelLogLevel {
  return typeof value === 'string' && (PANEL_LOG_LEVELS as readonly string[]).includes(value);
}

export default function LogModal({ open, onClose }: LogModalProps) {
  const { t } = useTranslation();
  const { isMobile } = useMediaQuery();
  const [rows, setRows] = useState('20');
  const [level, setLevel] = useState('info');
  const [syslog, setSyslog] = useState(false);
  const [autoUpdate, setAutoUpdate] = useState(false);
  const [loading, setLoading] = useState(false);
  const [logs, setLogs] = useState<string[]>([]);
  const [panelLevel, setPanelLevel] = useState<PanelLogLevel>('info');
  const [levelSaving, setLevelSaving] = useState(false);
  const [levelError, setLevelError] = useState('');
  const [messageApi, messageContextHolder] = message.useMessage();
  const sourcesQuery = useQuery({
    queryKey: LOG_SOURCES_KEY,
    queryFn: fetchLogSources,
    enabled: open,
    retry: false,
  });

  const runRefresh = useCallback(async () => {
    try {
      const msg = await HttpUtil.post<string[]>(`/panel/api/server/logs/${rows}`, {
        level,
        syslog,
      });
      if (msg?.success) {
        setLogs(msg.obj || []);
      }
      await PromiseUtil.sleep(300);
    } finally {
      setLoading(false);
    }
  }, [rows, level, syslog]);

  const refresh = useCallback(() => {
    setLoading(true);
    void runRefresh();
  }, [runRefresh]);

  const refreshRef = useRef(refresh);
  useEffect(() => {
    refreshRef.current = refresh;
  });

  // The spinner is raised during render so the fetch effect stays side-effect
  // free until its response lands.
  const refreshKey = open ? `${rows}\u0000${level}\u0000${syslog}` : null;
  const [loadingKey, setLoadingKey] = useState<string | null>(null);
  if (refreshKey !== loadingKey) {
    setLoadingKey(refreshKey);
    if (refreshKey) setLoading(true);
  }

  useEffect(() => {
    if (open) void runRefresh();
  }, [open, runRefresh]);

  useEffect(() => {
    if (!open || !autoUpdate) return;
    const id = setInterval(() => refreshRef.current(), AUTO_UPDATE_INTERVAL);
    return () => clearInterval(id);
  }, [open, autoUpdate]);

  /* No endpoint reports the current level, so the select starts on the backend
     default and adopts the level the POST answers with — the panel's real one. */
  async function changePanelLevel(next: PanelLogLevel): Promise<void> {
    const previous = panelLevel;
    setLevelError('');
    setLevelSaving(true);
    setPanelLevel(next);
    try {
      const msg = await HttpUtil.post<{ level?: string }>(
        '/panel/api/server/logLevel',
        { level: next },
        { silent: true, headers: { 'Content-Type': 'application/json' } },
      );
      if (!msg.success) {
        setPanelLevel(previous);
        setLevelError(msg.msg || t('pages.index.logLevelChangeFailed'));
        return;
      }
      const applied = isPanelLogLevel(msg.obj?.level) ? msg.obj.level : next;
      setPanelLevel(applied);
      messageApi.success(
        t('pages.index.logLevelChanged', { level: t(PANEL_LOG_LEVEL_LABELS[applied]) }),
      );
    } catch (error) {
      setPanelLevel(previous);
      setLevelError((error as Error).message || t('pages.index.logLevelChangeFailed'));
    } finally {
      setLevelSaving(false);
    }
  }

  const parsedLogs = useMemo(() => logs.map(parseLogLine), [logs]);

  const orderedSources = useMemo(() => {
    const rank = (name: string) => {
      const index = (LOG_SOURCE_ORDER as readonly string[]).indexOf(name);
      return index < 0 ? LOG_SOURCE_ORDER.length : index;
    };
    return [...(sourcesQuery.data ?? [])].sort((a, b) => rank(a.name) - rank(b.name));
  }, [sourcesQuery.data]);

  function sourceLabel(name: string): string {
    const key = LOG_SOURCE_LABELS[name];
    return key ? t(key) : name;
  }

  function download() {
    FileManager.downloadTextFile(logs.join('\n'), 'x-ui.log');
  }

  const titleNode = (
    <>
      {t('pages.index.logs')}
      <SyncOutlined
        spin={loading}
        className="reload-icon"
        role="button"
        tabIndex={0}
        aria-label={t('refresh')}
        onClick={refresh}
        onKeyDown={activateOnKey(refresh)}
      />
    </>
  );

  return (
    <Modal
      open={open}
      footer={null}
      width={isMobile ? '100vw' : 800}
      style={isMobile ? { top: 0, paddingBottom: 0, maxWidth: '100vw' } : undefined}
      className={isMobile ? 'logmodal-mobile' : undefined}
      onCancel={onClose}
      title={titleNode}
    >
      {messageContextHolder}
      <Form layout="inline" className="log-toolbar">
        <Form.Item label={t('pages.index.logLines')}>
          <Select
            aria-label={t('pages.index.logLines')}
            value={rows}
            size="small"
            style={{ width: 100 }}
            onChange={setRows}
            options={[
              { value: '20', label: '20' },
              { value: '50', label: '50' },
              { value: '100', label: '100' },
              { value: '500', label: '500' },
              { value: '1000', label: '1000' },
            ]}
          />
        </Form.Item>
        {/* Two level controls sit side by side, so each carries its own name:
            one narrows the lines fetched, the other changes the running panel. */}
        <Form.Item label={t('pages.index.viewLevel')}>
          <Select
            aria-label={t('pages.index.viewLevel')}
            value={level}
            size="small"
            style={{ minWidth: 95 }}
            onChange={setLevel}
            options={[
              { value: 'debug', label: t('pages.index.logLevelDebug') },
              { value: 'info', label: t('pages.index.logLevelInfo') },
              { value: 'notice', label: t('pages.index.logLevelNotice') },
              { value: 'warning', label: t('pages.index.logLevelWarning') },
              { value: 'err', label: t('pages.index.logLevelError') },
            ]}
          />
        </Form.Item>
        <Form.Item>
          <Checkbox checked={syslog} onChange={(e) => setSyslog(e.target.checked)}>
            SysLog
          </Checkbox>
          <Checkbox checked={autoUpdate} onChange={(e) => setAutoUpdate(e.target.checked)}>
            {t('pages.index.autoUpdate')}
          </Checkbox>
        </Form.Item>
        <Form.Item className="download-item">
          <Button
            type="primary"
            onClick={download}
            icon={<DownloadOutlined aria-hidden />}
            aria-label={t('pages.index.saveVisibleLogs')}
          />
        </Form.Item>
      </Form>

      <Form layout="vertical" className="log-level-form">
        <Form.Item
          label={t('pages.index.panelLogLevel')}
          extra={t('pages.index.panelLogLevelHint')}
        >
          <Select
            aria-label={t('pages.index.panelLogLevel')}
            value={panelLevel}
            size="small"
            loading={levelSaving}
            style={{ maxWidth: 200 }}
            onChange={(value: PanelLogLevel) => void changePanelLevel(value)}
            options={PANEL_LOG_LEVELS.map((value) => ({
              value,
              label: t(PANEL_LOG_LEVEL_LABELS[value]),
            }))}
          />
        </Form.Item>
      </Form>

      {levelError && (
        <div data-testid="log-level-error" className="log-level-error">
          <Alert
            type="error"
            showIcon
            title={t('pages.index.logLevelChangeFailed')}
            description={<pre className="log-error-detail">{levelError}</pre>}
          />
        </div>
      )}

      <Divider className="log-sources-divider" />

      <div className="log-sources" data-testid="log-sources">
        <Typography.Text strong>{t('pages.index.downloadLogs')}</Typography.Text>
        <Typography.Paragraph type="secondary" className="log-sources-hint">
          {t('pages.index.downloadLogsHint', { lines: LOG_EXPORT_LINES })}
        </Typography.Paragraph>

        {sourcesQuery.isError && (
          <div data-testid="log-sources-error">
            <Alert type="error" showIcon title={t('pages.index.logSourceLoadFailed')} />
          </div>
        )}

        {sourcesQuery.isPending && (
          <Typography.Text type="secondary">{t('loading')}</Typography.Text>
        )}
        {sourcesQuery.isSuccess && orderedSources.length === 0 && (
          <Typography.Text type="secondary">{t('noData')}</Typography.Text>
        )}

        {orderedSources.map((source) => (
          <div
            className="log-source-row"
            key={source.name}
            data-testid={`log-source-${source.name}`}
          >
            <div className="log-source-meta">
              <span className="log-source-name">{sourceLabel(source.name)}</span>
              <span className="log-source-path">{source.path}</span>
            </div>
            {source.exists ? (
              <span className="log-source-size">{SizeFormatter.sizeFormat(source.size)}</span>
            ) : (
              <Tag data-testid={`log-source-missing-${source.name}`}>
                {t('pages.index.logSourceMissingTag')}
              </Tag>
            )}
            <Tooltip
              title={source.exists ? undefined : t('pages.index.logSourceMissingHint')}
              placement="topRight"
            >
              <span>
                <Button
                  size="small"
                  icon={<DownloadOutlined aria-hidden />}
                  disabled={!source.exists}
                  aria-label={t('pages.index.downloadSource', {
                    source: sourceLabel(source.name),
                  })}
                  onClick={() => downloadLogFile(source.name)}
                />
              </span>
            </Tooltip>
          </div>
        ))}
      </div>

      <div className={`log-container ${isMobile ? 'log-container-mobile' : ''}`}>
        {parsedLogs.length === 0 ? (
          <div className="log-empty">No Record...</div>
        ) : isMobile ? (
          parsedLogs.map((log, idx) => (
            <div key={idx} className="log-card">
              <div className="log-card-head">
                {log.stamp && (
                  <span className="log-time">
                    {log.time && <span>{log.time}</span>}
                    {log.time && log.date ? ' ' : ''}
                    {log.date && <span className="log-date">{log.date}</span>}
                  </span>
                )}
                {log.levelText && (
                  <span className={`log-level-badge ${log.levelClass}`}>{log.levelText}</span>
                )}
              </div>
              {(log.body || log.service) && (
                <div className="log-body">
                  {log.service && <b>{log.service}</b>}
                  {log.service && log.body ? ' ' : ''}
                  {log.body && <span className="log-body-text">{log.body}</span>}
                </div>
              )}
            </div>
          ))
        ) : (
          parsedLogs.map((log, idx) => (
            <div key={idx} className="log-line">
              {log.stamp && <span className="log-stamp">{log.stamp}</span>}
              {log.stamp && log.levelText ? ' ' : ''}
              {log.levelText && (
                <span className={`log-level ${log.levelClass}`}>{log.levelText}</span>
              )}
              {(log.body || log.service) && (
                <>
                  {(log.stamp || log.levelText) && <span> - </span>}
                  {log.service && <b>{log.service}</b>}
                  {log.service && log.body ? ' ' : ''}
                  <span>{log.body}</span>
                </>
              )}
            </div>
          ))
        )}
      </div>
    </Modal>
  );
}
