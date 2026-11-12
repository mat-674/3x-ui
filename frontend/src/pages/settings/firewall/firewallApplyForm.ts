import { z } from 'zod';

const SSH_PORT = 'pages.settings.firewall.sshPortRequired';
const WINDOW = 'pages.settings.firewall.windowRequired';

// Messages are Zod issue keys resolved through t(), matching the RHF helpers.
export const FirewallApplySchema = z.object({
  sshPort: z.number({ error: SSH_PORT }).int(SSH_PORT).min(1, SSH_PORT).max(65535, SSH_PORT),
  confirmInSeconds: z.number({ error: WINDOW }).int(WINDOW).min(1, WINDOW).max(3600, WINDOW),
});

export type FirewallApplyValues = z.infer<typeof FirewallApplySchema>;

/* Null means "not entered yet": the panel cannot know the SSH port, and a
   guessed default is how an apply locks the operator out. */
export interface FirewallApplyDraft {
  sshPort: number | null;
  confirmInSeconds: number;
}

export const CONFIRM_WINDOW_DEFAULT = 300;

export function applyDraftOf(sshPort: number): FirewallApplyDraft {
  return {
    sshPort: sshPort > 0 ? sshPort : null,
    confirmInSeconds: CONFIRM_WINDOW_DEFAULT,
  };
}
