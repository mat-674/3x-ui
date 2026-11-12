import { act, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import FirewallTab from '@/pages/settings/FirewallTab';
import FirewallRollbackBanner from '@/pages/settings/firewall/FirewallRollbackBanner';
import { AllSetting } from '@/models/setting';
import { HttpUtil, Msg } from '@/utils';
import enUS from '../../../internal/web/translation/en-US.json';
import { renderWithProviders } from './test-utils';

const T = enUS.pages.settings.firewall;

const RULESET = [
  'table inet xui {',
  '  chain input {',
  '    type filter hook input priority 0; policy drop;',
  '  }',
  '}',
].join('\n');

interface MockOptions {
  status?: Partial<{
    loaded: boolean;
    table: string;
    sshPort: number;
    manageSsh: boolean;
    allowedTcp: number[];
    allowedUdp: number[];
    pendingRollback: boolean;
    rollbackId: string;
    rollbackDeadline: string;
    message: string;
  }>;
  applyError?: string;
  config?: string;
  calls?: string[];
}

function firewallStatus(options: MockOptions = {}) {
  return {
    loaded: true,
    table: 'inet xui',
    sshPort: 2222,
    manageSsh: true,
    allowedTcp: [2222, 443],
    allowedUdp: [],
    pendingRollback: false,
    message: '',
    ...options.status,
  };
}

function mockFirewallApi(options: MockOptions = {}) {
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
    if (url.endsWith('/panel/api/firewall/status')) {
      return new Msg(true, '', firewallStatus(options));
    }
    if (url.endsWith('/panel/api/firewall/config')) {
      return new Msg(true, '', { config: options.config ?? RULESET });
    }
    return new Msg(true, '', null);
  });
  vi.spyOn(HttpUtil, 'post').mockImplementation(async (url: string, body?: unknown) => {
    if (url.endsWith('/panel/api/firewall/apply')) {
      options.calls?.push('apply');
      if (options.applyError) return new Msg<null>(false, options.applyError);
      const window = (body as { confirmInSeconds?: number } | undefined)?.confirmInSeconds ?? 0;
      return new Msg(true, 'success', { deadlineId: 'dl-1', confirmInSeconds: window });
    }
    if (url.endsWith('/panel/api/firewall/confirm')) {
      options.calls?.push('confirm');
      return new Msg(true, 'success');
    }
    return new Msg(true, '');
  });
}

function renderTab(setting = new AllSetting({ firewallSSHPort: 2222, firewallManageSSH: true })) {
  const calls: string[] = [];
  const saveSetting = vi.fn(async () => {
    calls.push('save');
    return { success: true };
  });
  renderWithProviders(
    <FirewallTab allSetting={setting} updateSetting={vi.fn()} saveSetting={saveSetting} />,
  );
  return { saveSetting, calls };
}

function applyCallBody(): unknown {
  const call = vi
    .mocked(HttpUtil.post)
    .mock.calls.find(([url]) => String(url).endsWith('/panel/api/firewall/apply'));
  return call?.[1];
}

function applyCallCount(): number {
  return vi
    .mocked(HttpUtil.post)
    .mock.calls.filter(([url]) => String(url).endsWith('/panel/api/firewall/apply')).length;
}

function confirmCallBody(): unknown {
  const call = vi
    .mocked(HttpUtil.post)
    .mock.calls.find(([url]) => String(url).endsWith('/panel/api/firewall/confirm'));
  return call?.[1];
}

async function openApply() {
  const user = userEvent.setup();
  await user.click(screen.getByRole('button', { name: T.apply }));
  const dialog = await screen.findByRole('dialog');
  return { user, dialog };
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('FirewallTab perimeter facts', () => {
  it('says the ruleset drops input except loopback, ICMP, SSH and 443, and only owns inet xui', async () => {
    mockFirewallApi();

    renderTab();

    const intro = await screen.findByText(T.intro);
    expect(intro.textContent).toContain('443/tcp');
    expect(intro.textContent).toContain('inet xui');
    expect(intro.textContent).toContain('Docker');
  });
});

describe('FirewallTab SSH management', () => {
  it('warns that nothing can be applied while SSH is unmanaged', async () => {
    mockFirewallApi();

    renderTab(new AllSetting({ firewallSSHPort: 2222, firewallManageSSH: false }));

    const warning = await screen.findByTestId('firewall-manage-ssh-off');
    expect(warning.textContent).toContain(T.manageSshOffWarn);
  });

  it('drops the warning once the panel manages SSH', async () => {
    mockFirewallApi();

    renderTab();

    await screen.findByTestId('firewall-loaded');
    expect(screen.queryByTestId('firewall-manage-ssh-off')).toBeNull();
  });
});

describe('FirewallTab current state', () => {
  it('reports the loaded table and the ports the ruleset opens', async () => {
    mockFirewallApi();

    renderTab();

    expect((await screen.findByTestId('firewall-loaded')).textContent).toBe(T.loaded);
    expect(screen.getByTestId('firewall-table').textContent).toBe('inet xui');
    const tcp = screen.getByTestId('firewall-allowed-tcp').textContent ?? '';
    expect(tcp).toContain('443');
    expect(tcp).toContain('2222');
    // UDP is a separate chain: an empty one must not copy the TCP ports.
    expect(screen.queryByTestId('firewall-allowed-udp')).toBeNull();
  });

  it('quotes the backend when the panel ruleset is not the one loaded', async () => {
    const reason =
      "the panel's ruleset is not loaded: the host firewall is whatever the distribution, Docker or another tool installed";
    mockFirewallApi({ status: { loaded: false, message: reason } });

    renderTab();

    const message = await screen.findByTestId('firewall-status-message');
    expect(message.textContent).toContain(reason);
    expect(screen.getByTestId('firewall-loaded').textContent).toBe(T.notLoaded);
  });

  it('shows a pending rollback together with its deadline', async () => {
    const deadline = new Date(Date.now() + 120_000).toISOString();
    mockFirewallApi({
      status: { pendingRollback: true, rollbackId: 'dl-7', rollbackDeadline: deadline },
    });

    renderTab();

    expect((await screen.findByTestId('firewall-pending-rollback')).textContent).toContain(
      T.pendingRollbackTag,
    );
    const shown = await screen.findByTestId('firewall-rollback-deadline');
    expect(shown.textContent).toBe(new Date(deadline).toLocaleString());
    // A reload during the window must still offer the way out.
    expect(await screen.findByTestId('firewall-keep-rules')).toBeTruthy();
  });
});

describe('FirewallTab apply window', () => {
  it('sends the confirmation window the operator entered, never an apply without one', async () => {
    mockFirewallApi();

    const { saveSetting } = renderTab();
    await screen.findByTestId('firewall-loaded');
    const { user, dialog } = await openApply();

    const windowInput = within(dialog).getByLabelText(T.applyWindowLabel);
    await user.clear(windowInput);
    await user.type(windowInput, '45');
    await user.click(within(dialog).getByRole('button', { name: T.apply }));

    expect(applyCallBody()).toEqual({ confirmInSeconds: 45 });
    // Apply reads the policy from the DB, so the SSH port is persisted first.
    expect(saveSetting).toHaveBeenCalledOnce();
  });

  it('refuses to apply while the window field is empty', async () => {
    mockFirewallApi();

    renderTab();
    await screen.findByTestId('firewall-loaded');
    const { user, dialog } = await openApply();

    const windowInput = within(dialog).getByLabelText(T.applyWindowLabel);
    await user.clear(windowInput);
    await user.click(within(dialog).getByRole('button', { name: T.apply }));

    expect(await within(dialog).findByText(T.windowRequired)).toBeTruthy();
    expect(applyCallCount()).toBe(0);
  });

  it('renders the backend refusal verbatim instead of a generic failure', async () => {
    const refusal =
      'firewall: a confirmation window is required; an unconfirmed apply would be permanent';
    mockFirewallApi({ applyError: refusal });

    renderTab();
    await screen.findByTestId('firewall-loaded');
    const { user, dialog } = await openApply();
    await user.click(within(dialog).getByRole('button', { name: T.apply }));

    const error = await screen.findByTestId('firewall-apply-error');
    expect(error.textContent).toContain(refusal);
  });
});

describe('FirewallTab rollback window', () => {
  it('keeps the countdown and the keep button visible after an apply', async () => {
    mockFirewallApi();

    renderTab();
    await screen.findByTestId('firewall-loaded');
    const { user, dialog } = await openApply();
    await user.click(within(dialog).getByRole('button', { name: T.apply }));

    const countdown = await screen.findByTestId('firewall-countdown');
    const seconds = Number(/Rollback in (\d+)s/.exec(countdown.textContent ?? '')?.[1]);
    expect(seconds).toBeGreaterThan(0);
    expect(seconds).toBeLessThanOrEqual(300);

    const keep = screen.getByTestId('firewall-keep-rules');
    expect(keep.textContent).toContain(T.keepRules);
    expect(keep.hasAttribute('disabled')).toBe(false);
    // The deadline id is the operator's handle on the armed rollback.
    expect(screen.getByTestId('firewall-rollback').textContent).toContain('dl-1');
  });

  it('confirms the deadline id returned by the apply', async () => {
    mockFirewallApi();

    renderTab();
    await screen.findByTestId('firewall-loaded');
    const { user, dialog } = await openApply();
    await user.click(within(dialog).getByRole('button', { name: T.apply }));

    const keep = await screen.findByTestId('firewall-keep-rules');
    await user.click(keep);

    expect(confirmCallBody()).toEqual({ deadlineId: 'dl-1' });
    expect(screen.queryByTestId('firewall-rollback')).toBeNull();
  });

  it('counts the window down, because at zero the previous ruleset returns', async () => {
    vi.useFakeTimers();
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, 'success'));

    renderWithProviders(
      <FirewallRollbackBanner
        deadlineId="dl-9"
        deadlineAt={Date.now() + 90_000}
        onKept={vi.fn()}
      />,
    );

    expect(screen.getByTestId('firewall-countdown').textContent).toBe('Rollback in 90s');
    await act(async () => {
      vi.advanceTimersByTime(30_000);
    });
    expect(screen.getByTestId('firewall-countdown').textContent).toBe('Rollback in 60s');
  });

  it('still offers the keep button once the window ran out, because the rollback may not have fired', async () => {
    vi.useFakeTimers();
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, 'success'));

    renderWithProviders(
      <FirewallRollbackBanner deadlineId="dl-9" deadlineAt={Date.now() - 5_000} onKept={vi.fn()} />,
    );

    expect(screen.getByTestId('firewall-countdown').textContent).toBe(T.rollbackExpired);
    const keep = screen.getByTestId('firewall-keep-rules');
    expect(keep.hasAttribute('disabled')).toBe(false);
  });
});

describe('FirewallTab rendered ruleset', () => {
  it('fetches the ruleset only once the operator asks for it', async () => {
    mockFirewallApi();

    renderTab();
    await screen.findByTestId('firewall-loaded');
    const configCalls = () =>
      vi
        .mocked(HttpUtil.get)
        .mock.calls.filter(([url]) => String(url).endsWith('/panel/api/firewall/config'));
    expect(configCalls()).toHaveLength(0);

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: T.showConfig }));

    const textarea = await screen.findByRole('textbox', { name: T.configTitle });
    expect((textarea as HTMLTextAreaElement).readOnly).toBe(true);
    expect((textarea as HTMLTextAreaElement).value).toBe(RULESET);
    expect(configCalls()).toHaveLength(1);
  });
});
