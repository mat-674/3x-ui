import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { z } from 'zod';

import { HttpUtil } from '@/utils';

/* Every field carries a fallback: the status card is read on page load, and one
   unexpected shape must degrade a row, never blank the tab. */
export const EdgeStatusSchema = z
  .object({
    enabled: z.boolean().optional().default(false),
    running: z.boolean().optional().default(false),
    configValid: z.boolean().optional().default(false),
    listen: z.string().optional().default(''),
    port: z.number().optional().default(0),
    serverNames: z.array(z.string()).catch([]),
    panelUpstream: z.string().optional().default(''),
    subPrefixes: z.number().optional().default(0),
    certDomains: z.array(z.string()).catch([]),
    certNotAfter: z.string().nullish(),
    message: z.string().optional().default(''),
    /* The binary is a prerequisite, not a detail: the tab refuses to pretend the
       perimeter can work without it, so these drive the install banner. */
    nginxInstalled: z.boolean().optional().default(false),
    nginxVersion: z.string().optional().default(''),
    nginxBin: z.string().optional().default(''),
    nginxInstallable: z.boolean().optional().default(false),
    nginxHint: z.string().optional().default(''),
  })
  .loose();
export type EdgeStatus = z.infer<typeof EdgeStatusSchema>;

/* The install answer keeps the package manager's own commands: an operator has to
   be able to see what ran as root on their server. */
export const EdgeInstallReportSchema = z
  .object({
    alreadyInstalled: z.boolean().optional().default(false),
    packageManager: z.string().optional().default(''),
    version: z.string().optional().default(''),
    bin: z.string().optional().default(''),
    steps: z.array(z.string()).catch([]),
  })
  .loose();
export type EdgeInstallReport = z.infer<typeof EdgeInstallReportSchema>;

const STATUS_KEY = ['edge', 'status'] as const;
const CONFIG_KEY = ['edge', 'config'] as const;

async function fetchEdgeStatus(): Promise<EdgeStatus> {
  const msg = await HttpUtil.get('/panel/api/edge/status', undefined, { silent: true });
  if (!msg.success) throw new Error(msg.msg || 'edge status');
  const parsed = EdgeStatusSchema.safeParse(msg.obj);
  return parsed.success ? parsed.data : EdgeStatusSchema.parse({});
}

/* Fetched on demand: the config text is only worth a round trip once the
   operator asks to compare it with what nginx actually loaded. */
async function fetchEdgeConfig(): Promise<string> {
  const msg = await HttpUtil.get<{ config?: string }>('/panel/api/edge/config', undefined, {
    silent: true,
  });
  if (!msg.success) throw new Error(msg.msg || 'edge config');
  return typeof msg.obj?.config === 'string' ? msg.obj.config : '';
}

export function useEdgeStatus() {
  return useQuery({ queryKey: STATUS_KEY, queryFn: fetchEdgeStatus, retry: false });
}

export function useEdgeConfig(enabled: boolean) {
  return useQuery({ queryKey: CONFIG_KEY, queryFn: fetchEdgeConfig, enabled, retry: false });
}

/* Rejections carry the backend's own words: a config nginx refused is the most
   useful sentence this tab can show, so it is never replaced by a local string. */
function rejectUnlessSuccess(msg: { success: boolean; msg: string }, fallback: string): void {
  if (!msg.success) throw new Error(msg.msg || fallback);
}

export function useApplyEdge() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const msg = await HttpUtil.post('/panel/api/edge/apply', undefined, { silent: true });
      rejectUnlessSuccess(msg, 'apply');
      return msg;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: STATUS_KEY });
      queryClient.invalidateQueries({ queryKey: CONFIG_KEY });
    },
  });
}

export function useStopEdge() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const msg = await HttpUtil.post('/panel/api/edge/stop', undefined, { silent: true });
      rejectUnlessSuccess(msg, 'stop');
      return msg;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: STATUS_KEY }),
  });
}

/* Installing refreshes the status so the card flips to "installed" in place: a page
   reload after installing would read as a second, unexplained step. */
export function useInstallEdgeNginx() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (): Promise<EdgeInstallReport> => {
      const msg = await HttpUtil.post('/panel/api/edge/install-nginx', undefined, { silent: true });
      rejectUnlessSuccess(msg, 'install nginx');
      const parsed = EdgeInstallReportSchema.safeParse(msg.obj);
      return parsed.success ? parsed.data : EdgeInstallReportSchema.parse({});
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: STATUS_KEY }),
  });
}
