import { z } from 'zod';

import { HttpUtil } from '@/utils';

/* The panel log level is console/syslog verbosity only: the file backend always
   keeps DEBUG, so raising this never removes a line from a downloaded file. */
export const PANEL_LOG_LEVELS = ['debug', 'info', 'notice', 'warning', 'error'] as const;
export type PanelLogLevel = (typeof PANEL_LOG_LEVELS)[number];

export const PANEL_LOG_LEVEL_LABELS: Record<PanelLogLevel, string> = {
  debug: 'pages.index.logLevelDebug',
  info: 'pages.index.logLevelInfo',
  notice: 'pages.index.logLevelNotice',
  warning: 'pages.index.logLevelWarning',
  error: 'pages.index.logLevelError',
};

export const LOG_SOURCE_LABELS: Record<string, string> = {
  app: 'pages.index.logSourceApp',
  'edge-access': 'pages.index.logSourceEdgeAccess',
  'edge-error': 'pages.index.logSourceEdgeError',
};

export interface LogSource {
  name: string;
  path: string;
  exists: boolean;
  size: number;
}

export const LogSourceSchema = z
  .object({
    name: z.string().min(1),
    path: z.string().optional().default(''),
    exists: z.boolean().optional().default(false),
    size: z.number().optional().default(0),
  })
  .loose();

/* One unreadable row must degrade a list entry, never blank the section. */
export function parseLogSources(value: unknown): LogSource[] {
  const list: unknown[] = Array.isArray(value) ? value : [];
  return list.flatMap((row) => {
    const parsed = LogSourceSchema.safeParse(row);
    return parsed.success ? [parsed.data] : [];
  });
}

export const LOG_SOURCE_ORDER = ['app', 'edge-access', 'edge-error'] as const;

export const LOG_SOURCES_KEY = ['server', 'logSources'] as const;

/* Re-read on every open: a file that did not exist last time may exist now, and
   a failed request must surface rather than look like "no log files". */
export async function fetchLogSources(): Promise<LogSource[]> {
  const msg = await HttpUtil.get<unknown>('/panel/api/server/logs/sources', undefined, {
    silent: true,
  });
  if (!msg.success) throw new Error(msg.msg || 'log sources');
  return parseLogSources(msg.obj);
}

/* The tail length both the panel and this UI default to: enough for one incident
   without shipping a rotated multi-megabyte log to the browser. */
export const LOG_EXPORT_LINES = 5000;

export function logExportUrl(source: string, lines: number = LOG_EXPORT_LINES): string {
  // The panel injects a base path ending in "/" (at least "/"); the download URL
  // is absolute so it resolves the same from any SPA route.
  const base = window.X_UI_BASE_PATH || '/';
  return `${base}panel/api/server/logs/export?source=${encodeURIComponent(source)}&lines=${lines}`;
}

/* An anchor, not window.location: the panel stays on screen, and a vanished file
   downloads its JSON error envelope instead of replacing the page with it. */
export function downloadLogFile(source: string, lines: number = LOG_EXPORT_LINES): void {
  const link = window.document.createElement('a');
  link.href = logExportUrl(source, lines);
  link.download = `xui-${source}.log`;
  link.style.position = 'absolute';
  link.style.left = '-9999px';
  window.document.body.appendChild(link);
  link.click();
  link.remove();
}
