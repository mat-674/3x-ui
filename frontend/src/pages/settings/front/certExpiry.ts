export const CERT_EXPIRY_WARN_DAYS = 14;

const DAY_MS = 86_400_000;

export type CertExpiryLevel = 'unknown' | 'ok' | 'expiring' | 'expired';

/* Rounded up, so "expires in 3 days" covers the whole of the third day rather
   than showing 2 for the first hours after midnight. */
export function daysUntilExpiry(
  notAfter: string | null | undefined,
  now: number = Date.now(),
): number | null {
  if (!notAfter) return null;
  const at = Date.parse(notAfter);
  if (Number.isNaN(at)) return null;
  return Math.ceil((at - now) / DAY_MS);
}

export function certExpiryLevel(days: number | null): CertExpiryLevel {
  if (days === null) return 'unknown';
  if (days < 0) return 'expired';
  return days <= CERT_EXPIRY_WARN_DAYS ? 'expiring' : 'ok';
}
