import { z } from 'zod';

// Messages are Zod issue keys resolved through t(), matching the RHF helpers.
export const FrontCertFormSchema = z.object({
  domains: z.string().trim().min(1, 'pages.settings.front.certDomainsRequired'),
  certPath: z.string().trim().min(1, 'pages.settings.front.certCertPathRequired'),
  keyPath: z.string().trim().min(1, 'pages.settings.front.certKeyPathRequired'),
  issuer: z.enum(['file', 'acme']),
  acmeEmail: z.string().trim().optional(),
});

export type FrontCertFormValues = z.infer<typeof FrontCertFormSchema>;

export const EMPTY_CERT_FORM: FrontCertFormValues = {
  domains: '',
  certPath: '',
  keyPath: '',
  issuer: 'file',
  acmeEmail: '',
};
