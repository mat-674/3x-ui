import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { z } from 'zod';

import { HttpUtil } from '@/utils';

/* Every field carries a fallback: the status card is read on page load, and one
   unexpected shape must degrade a row, never blank the tab. */
export const FirewallStatusSchema = z
  .object({
    loaded: z.boolean().optional().default(false),
    table: z.string().optional().default(''),
    sshPort: z.number().optional().default(0),
    manageSsh: z.boolean().optional().default(false),
    allowedTcp: z.array(z.number()).catch([]),
    allowedUdp: z.array(z.number()).catch([]),
    pendingRollback: z.boolean().optional().default(false),
    rollbackId: z.string().optional().default(''),
    rollbackDeadline: z.string().nullish(),
    message: z.string().optional().default(''),
  })
  .loose();
export type FirewallStatus = z.infer<typeof FirewallStatusSchema>;

export const FIREWALL_STATUS_KEY = ['firewall', 'status'] as const;
const CONFIG_KEY = ['firewall', 'config'] as const;
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } } as const;

/* A failed status request still carries the report: nft being unavailable is
   exactly the case where the operator needs the backend's own sentence. */
async function fetchFirewallStatus(): Promise<FirewallStatus> {
  const msg = await HttpUtil.get<unknown>('/panel/api/firewall/status', undefined, {
    silent: true,
  });
  const parsed = FirewallStatusSchema.safeParse(msg.obj);
  if (parsed.success) return parsed.data;
  if (!msg.success) throw new Error(msg.msg || 'firewall status');
  return FirewallStatusSchema.parse({});
}

/* Fetched on demand: the ruleset is only worth a round trip once the operator
   asks to read what nft would be handed. */
async function fetchFirewallConfig(): Promise<string> {
  const msg = await HttpUtil.get<{ config?: string }>('/panel/api/firewall/config', undefined, {
    silent: true,
  });
  if (!msg.success) throw new Error(msg.msg || 'firewall config');
  return typeof msg.obj?.config === 'string' ? msg.obj.config : '';
}

export function useFirewallStatus() {
  return useQuery({ queryKey: FIREWALL_STATUS_KEY, queryFn: fetchFirewallStatus, retry: false });
}

export function useFirewallConfig(enabled: boolean) {
  return useQuery({ queryKey: CONFIG_KEY, queryFn: fetchFirewallConfig, enabled, retry: false });
}

export interface FirewallApplyInput {
  /* Required, never defaulted: the backend refuses an apply with no window, and
     a window the UI invented would not be the one the operator agreed to. */
  confirmInSeconds: number;
}

export interface FirewallApplyResult {
  deadlineId: string;
  confirmInSeconds: number;
}

export function useApplyFirewall() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ confirmInSeconds }: FirewallApplyInput): Promise<FirewallApplyResult> => {
      const msg = await HttpUtil.post<{ deadlineId?: string; confirmInSeconds?: number }>(
        '/panel/api/firewall/apply',
        { confirmInSeconds },
        JSON_HEADERS,
      );
      if (!msg.success) throw new Error(msg.msg || 'apply firewall');
      return {
        deadlineId: typeof msg.obj?.deadlineId === 'string' ? msg.obj.deadlineId : '',
        confirmInSeconds: msg.obj?.confirmInSeconds ?? confirmInSeconds,
      };
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: FIREWALL_STATUS_KEY });
      queryClient.invalidateQueries({ queryKey: CONFIG_KEY });
    },
  });
}

export function useConfirmFirewall() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ deadlineId }: { deadlineId: string }) => {
      const msg = await HttpUtil.post('/panel/api/firewall/confirm', { deadlineId }, JSON_HEADERS);
      if (!msg.success) throw new Error(msg.msg || 'confirm firewall');
      return msg;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: FIREWALL_STATUS_KEY }),
  });
}
