import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { z } from 'zod';

import { HttpUtil } from '@/utils';

/* Every certificate field is optional on purpose: the certificate controller is
   built in parallel, and a shape change must not blank the table or hide the
   no-certificate warning the operator depends on. */
export const FrontCertSchema = z
  .object({
    id: z.number().optional(),
    domains: z.string().optional(),
    mode: z.string().optional(),
    certPath: z.string().optional(),
    keyPath: z.string().optional(),
    issuer: z.string().optional(),
    acmeEmail: z.string().optional(),
    notAfter: z.string().nullish(),
    lastError: z.string().nullish(),
    lastAttempt: z.string().nullish(),
    nextAttempt: z.string().nullish(),
  })
  .loose();
export type FrontCert = z.infer<typeof FrontCertSchema>;

export const FrontSiteSchema = z
  .object({
    id: z.number(),
    name: z.string().optional().default(''),
    bytes: z.number().optional().default(0),
    fileCount: z.number().optional().default(0),
    entryFile: z.string().optional().default(''),
    active: z.boolean().optional().default(false),
    createdAt: z.string().optional().default(''),
  })
  .loose();
export type FrontSite = z.infer<typeof FrontSiteSchema>;

/* The reload answer carries the backend's own verdict on whether anything is
   configured, which is the only reliable signal when rows exist but no file
   loads. Everything else there stays optional. */
export const FrontCertReloadSchema = z
  .object({
    hasCertificate: z.boolean().optional(),
    warning: z.string().optional(),
    certificates: z.array(z.unknown()).optional(),
  })
  .loose();
export type FrontCertReloadReport = z.infer<typeof FrontCertReloadSchema>;

export interface FrontCertInput {
  id?: number;
  domains: string;
  certPath: string;
  keyPath: string;
  issuer: string;
  acmeEmail: string;
}

const SITES_KEY = ['front', 'sites'] as const;
const CERTS_KEY = ['front', 'certs'] as const;
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } } as const;

function parseRows<T>(rows: unknown, schema: z.ZodType<T>): T[] {
  const list: unknown[] = Array.isArray(rows) ? rows : [];
  return list.flatMap((row) => {
    const parsed = schema.safeParse(row);
    return parsed.success ? [parsed.data] : [];
  });
}

async function listFrontSites(): Promise<FrontSite[]> {
  const msg = await HttpUtil.get('/panel/api/front/sites', undefined, { silent: true });
  if (!msg.success) throw new Error(msg.msg || 'front sites');
  return parseRows(msg.obj, FrontSiteSchema);
}

/* A successful answer with no rows is the "no certificate" case, not an error:
   the caller warns on an empty list and only on a failed request. */
async function listFrontCerts(): Promise<FrontCert[]> {
  const msg = await HttpUtil.get('/panel/api/front/certs', undefined, { silent: true });
  if (!msg.success) throw new Error(msg.msg || 'front certificates');
  return parseRows(msg.obj, FrontCertSchema);
}

export function useFrontSites() {
  return useQuery({ queryKey: SITES_KEY, queryFn: listFrontSites, retry: false });
}

export function useFrontCerts() {
  return useQuery({ queryKey: CERTS_KEY, queryFn: listFrontCerts, retry: false });
}

export function useUploadFrontSite() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ name, file }: { name: string; file: File }) => {
      const form = new FormData();
      form.append('file', file);
      form.append('name', name);
      const msg = await HttpUtil.post('/panel/api/front/sites/upload', form);
      if (!msg.success) throw new Error(msg.msg || 'upload');
      return msg;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: SITES_KEY }),
  });
}

export function useActivateFrontSite() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (id: number) => {
      const msg = await HttpUtil.post(`/panel/api/front/sites/${id}/activate`);
      if (!msg.success) throw new Error(msg.msg || 'activate');
      return msg;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: SITES_KEY }),
  });
}

export function useDeleteFrontSite() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (id: number) => {
      const msg = await HttpUtil.post(`/panel/api/front/sites/${id}/delete`);
      if (!msg.success) throw new Error(msg.msg || 'delete');
      return msg;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: SITES_KEY }),
  });
}

export function useSaveFrontCert() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: FrontCertInput) => {
      const msg = await HttpUtil.post('/panel/api/front/certs', input, JSON_HEADERS);
      if (!msg.success) throw new Error(msg.msg || 'save certificate');
      return msg;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: CERTS_KEY }),
  });
}

export function useDeleteFrontCert() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (id: number) => {
      const msg = await HttpUtil.post(`/panel/api/front/certs/${id}/delete`);
      if (!msg.success) throw new Error(msg.msg || 'delete certificate');
      return msg;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: CERTS_KEY }),
  });
}

export function useReloadFrontCerts() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (): Promise<FrontCertReloadReport> => {
      const msg = await HttpUtil.post('/panel/api/front/certs/reload');
      if (!msg.success) throw new Error(msg.msg || 'reload certificates');
      const parsed = FrontCertReloadSchema.safeParse(msg.obj);
      return parsed.success ? parsed.data : {};
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: CERTS_KEY }),
  });
}

export function splitDomains(raw: string | null | undefined): string[] {
  if (!raw) return [];
  return raw
    .split(/[,\s]+/)
    .map((d) => d.trim())
    .filter(Boolean);
}
