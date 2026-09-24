import { z } from 'zod';

export const NaiveClientSchema = z.object({
  email: z.string().min(1),
  password: z.string().default(''),
  limitIp: z.number().int().min(0).default(0),
  totalGB: z.number().int().min(0).default(0),
  expiryTime: z.number().int().default(0),
  enable: z.boolean().default(true),
  tgId: z
    .union([z.number(), z.string()])
    .transform((v) => Number(v) || 0)
    .default(0),
  subId: z.string().default(''),
  comment: z.string().default(''),
  reset: z.number().int().min(0).default(0),
  created_at: z.number().int().optional(),
  updated_at: z.number().int().optional(),
});
export type NaiveClient = z.infer<typeof NaiveClientSchema>;

export const NaiveInboundSettingsSchema = z.object({
  domain: z
    .string()
    .default('')
    .refine((domain) => domain.trim() !== '', {
      message: 'pages.inbounds.naive.domainRequired',
    }),
  fallbackRoot: z.string().default(''),
  probeResistance: z.boolean().default(true),
  hideIp: z.boolean().default(true),
  hideVia: z.boolean().default(true),
  encode: z.boolean().default(true),
  clients: z.array(NaiveClientSchema).default([]),
});
export type NaiveInboundSettings = z.infer<typeof NaiveInboundSettingsSchema>;
