import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import EdgeTab from '@/pages/settings/EdgeTab';
import { AllSetting } from '@/models/setting';
import { HttpUtil, Msg } from '@/utils';
import enUS from '../../../internal/web/translation/en-US.json';
import { renderWithProviders } from './test-utils';

const DAY_MS = 86_400_000;
const DEFAULT_STEPS = ['$ apt-get install -y -q nginx', '$ systemctl disable --now nginx'];

interface MockOptions {
  notAfterInDays?: number | null;
  configValid?: boolean;
  message?: string;
  applyError?: string;
  config?: string;
  nginxInstalled?: boolean;
  nginxInstallable?: boolean;
  nginxHint?: string;
  nginxVersion?: string;
  nginxBin?: string;
  installError?: string;
  installSteps?: string[];
  /* The real backend flips to installed once the binary exists; the status
     query must therefore be refetched for the card to notice. */
  installedAfterInstall?: boolean;
}

function edgeStatus(options: MockOptions) {
  const { notAfterInDays = 200, configValid = true, message = '' } = options;
  return {
    enabled: true,
    running: true,
    configValid,
    listen: '127.0.0.1',
    port: 8443,
    serverNames: ['panel.example.com'],
    panelUpstream: '127.0.0.1:2053',
    subPrefixes: 3,
    certDomains: ['panel.example.com'],
    certNotAfter:
      notAfterInDays === null ? null : new Date(Date.now() + notAfterInDays * DAY_MS).toISOString(),
    message,
    nginxInstalled: options.nginxInstalled ?? true,
    nginxVersion: options.nginxVersion ?? '1.24.0',
    nginxBin: options.nginxBin ?? '/usr/sbin/nginx',
    nginxInstallable: options.nginxInstallable ?? false,
    nginxHint: options.nginxHint ?? '',
  };
}

function mockEdgeApi(options: MockOptions = {}) {
  let statusReads = 0;
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
    if (url.endsWith('/panel/api/edge/status')) {
      statusReads += 1;
      const installed =
        options.installedAfterInstall === true && statusReads > 1
          ? true
          : (options.nginxInstalled ?? true);
      return new Msg(true, '', edgeStatus({ ...options, nginxInstalled: installed }));
    }
    if (url.endsWith('/panel/api/edge/config')) {
      return new Msg(true, '', {
        config: options.config ?? 'server {\n  listen 127.0.0.1:8443;\n}',
      });
    }
    return new Msg(true, '', null);
  });
  vi.spyOn(HttpUtil, 'post').mockImplementation(async (url: string) => {
    if (url.endsWith('/panel/api/edge/apply')) {
      return options.applyError
        ? new Msg(false, options.applyError)
        : new Msg(true, enUS.pages.settings.edge.applied);
    }
    if (url.endsWith('/panel/api/edge/install-nginx')) {
      return options.installError
        ? new Msg(false, options.installError)
        : new Msg(true, '', {
            alreadyInstalled: false,
            packageManager: 'apt',
            version: '1.24.0',
            bin: '/usr/sbin/nginx',
            steps: options.installSteps ?? DEFAULT_STEPS,
          });
    }
    return new Msg(true, '');
  });
}

function renderTab(
  setting = new AllSetting(),
  saveSetting = vi.fn(async () => ({ success: true })),
) {
  renderWithProviders(
    <EdgeTab allSetting={setting} updateSetting={vi.fn()} saveSetting={saveSetting} />,
  );
  return { saveSetting };
}

/* The primary button is the operator's single action; the confirm dialog's OK is
   always "Apply" because the risk it names is the apply, not the install. */
async function clickPrimary(label: string) {
  const user = userEvent.setup();
  await user.click(screen.getByRole('button', { name: label }));
  const dialog = await screen.findByRole('dialog');
  await user.click(within(dialog).getByRole('button', { name: enUS.pages.settings.edge.apply }));
}

async function clickApply() {
  await clickPrimary(enUS.pages.settings.edge.apply);
}

function applyCalls() {
  return vi
    .mocked(HttpUtil.post)
    .mock.calls.filter(([url]) => String(url).endsWith('/panel/api/edge/apply'));
}

function postUrls() {
  return vi.mocked(HttpUtil.post).mock.calls.map(([url]) => String(url));
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('EdgeTab topology', () => {
  it('explains that Xray owns 443 and hands the rest to nginx on loopback', async () => {
    mockEdgeApi();

    renderTab();

    expect(await screen.findByText(enUS.pages.settings.edge.intro)).toBeTruthy();
  });
});

describe('EdgeTab certificate expiry', () => {
  it('flags the perimeter certificate expiring inside the shared warning window', async () => {
    mockEdgeApi({ notAfterInDays: 5 });

    renderTab();

    const warning = await screen.findByTestId('edge-cert-expiry');
    expect(warning.textContent).toMatch(/Renew within 5 days/);
  });

  it('leaves a certificate far from expiry unremarkable', async () => {
    mockEdgeApi({ notAfterInDays: 200 });

    renderTab();

    await screen.findByTestId('edge-running');
    // Nothing to warn about: the row shows the date only, not a renewal tag.
    expect(screen.queryByTestId('edge-cert-expiry')).toBeNull();
  });

  it('marks a certificate that already expired', async () => {
    mockEdgeApi({ notAfterInDays: -3 });

    renderTab();

    const expired = await screen.findByTestId('edge-cert-expiry');
    expect(expired.textContent).toMatch(/Expired 3 days ago/);
  });
});

describe('EdgeTab failure messages', () => {
  it('shows the reason the backend rejected the generated config', async () => {
    const reason = 'no server names: set edgeServerNames or use a certificate with a DNS name';
    mockEdgeApi({ configValid: false, message: reason });

    renderTab();

    const invalid = await screen.findByTestId('edge-config-invalid');
    expect(invalid.textContent).toContain(enUS.pages.settings.edge.configInvalid);
    expect(invalid.textContent).toContain(reason);
    // A valid-config card must not carry this marker, or the test proves nothing.
    expect(screen.queryByTestId('edge-status-message')).toBeNull();
  });

  it('renders the nginx rejection verbatim when Apply fails', async () => {
    const rejection =
      'nginx: [emerg] unknown directive "proxy_protocol" in /etc/x-ui/edge/nginx.conf:12';
    mockEdgeApi({ applyError: rejection });

    const { saveSetting } = renderTab();
    await screen.findByTestId('edge-running');
    await clickApply();

    const error = await screen.findByTestId('edge-apply-error');
    expect(error.textContent).toContain(rejection);
    // Apply must persist the eight settings first: it reads them back from the DB.
    expect(saveSetting).toHaveBeenCalledOnce();
  });

  it('names the risk and writes nothing until the operator confirms', async () => {
    mockEdgeApi();

    const { saveSetting } = renderTab();
    await screen.findByTestId('edge-running');
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: enUS.pages.settings.edge.apply }));
    const dialog = await screen.findByRole('dialog');

    // The panel become unreachable except through nginx: that sentence is the point.
    expect(dialog.textContent).toContain(enUS.pages.settings.edge.applyConfirm);
    expect(applyCalls()).toHaveLength(0);
    expect(saveSetting).not.toHaveBeenCalled();
  });
});

describe('EdgeTab PROXY protocol pairing', () => {
  it('warns that xver must be 1 when PROXY protocol is on', async () => {
    mockEdgeApi();

    renderTab(new AllSetting({ edgeProxyProtocol: true }));

    const note = await screen.findByTestId('edge-xver-note');
    expect(note.textContent).toContain(enUS.pages.settings.edge.proxyProtocolOnNote);
    expect(screen.queryByTestId('edge-xver-warning')).toBeNull();
  });

  it('turns the note into a mismatch warning when PROXY protocol is off', async () => {
    mockEdgeApi();

    renderTab(new AllSetting({ edgeProxyProtocol: false }));

    const warning = await screen.findByTestId('edge-xver-warning');
    expect(warning.textContent).toContain(enUS.pages.settings.edge.proxyProtocolOffWarn);
    // The on-state note would tell the operator the exact opposite.
    expect(screen.queryByTestId('edge-xver-note')).toBeNull();
  });
});

describe('EdgeTab generated config', () => {
  it('fetches the generated config only once the operator asks for it', async () => {
    const config = 'server {\n  listen 127.0.0.1:8443 proxy_protocol;\n}';
    mockEdgeApi({ config });

    renderTab();
    await screen.findByTestId('edge-running');
    const configCalls = () =>
      vi
        .mocked(HttpUtil.get)
        .mock.calls.filter(([url]) => String(url).endsWith('/panel/api/edge/config'));
    expect(configCalls()).toHaveLength(0);

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: enUS.pages.settings.edge.showConfig }));

    const textarea = await screen.findByRole('textbox', {
      name: enUS.pages.settings.edge.configTitle,
    });
    expect((textarea as HTMLTextAreaElement).readOnly).toBe(true);
    expect((textarea as HTMLTextAreaElement).value).toBe(config);
    expect(configCalls()).toHaveLength(1);
  });
});

describe('EdgeTab without the nginx binary', () => {
  it('offers no install button when the host cannot install nginx, and says why', async () => {
    const hint =
      'the panel runs in a container: add nginx to the image instead of installing it at runtime';
    mockEdgeApi({ nginxInstalled: false, nginxInstallable: false, nginxHint: hint });

    renderTab();

    const blocked = await screen.findByTestId('edge-nginx-blocked');
    expect(blocked.textContent).toContain(hint);
    // A button that can only fail is the dead end this tab must not offer.
    expect(
      screen.queryByRole('button', { name: enUS.pages.settings.edge.installNginx }),
    ).toBeNull();
    expect(
      screen.queryByRole('button', { name: enUS.pages.settings.edge.installAndApply }),
    ).toBeNull();
    // Apply cannot do anything without the binary, so it is not offered either.
    const applyButton = screen.getByRole('button', { name: enUS.pages.settings.edge.apply });
    expect(applyButton.hasAttribute('disabled')).toBe(true);
    // …but the operator who installs it by hand can make the panel look again.
    expect(
      screen.getByRole('button', { name: enUS.pages.settings.edge.nginxRecheck }),
    ).toBeTruthy();
  });

  it('offers the install button and the combined action when it can be installed', async () => {
    mockEdgeApi({ nginxInstalled: false, nginxInstallable: true });

    renderTab();

    await screen.findByTestId('edge-nginx-missing');
    expect(
      screen.getByRole('button', { name: enUS.pages.settings.edge.installNginx }),
    ).toBeTruthy();
    expect(
      screen.getByRole('button', { name: enUS.pages.settings.edge.installAndApply }),
    ).toBeTruthy();
  });

  it('installs before applying so apply never fails on the binary it just fetched', async () => {
    mockEdgeApi({ nginxInstalled: false, nginxInstallable: true, installedAfterInstall: true });

    renderTab();
    await screen.findByTestId('edge-nginx-missing');
    await clickPrimary(enUS.pages.settings.edge.installAndApply);

    const urls = postUrls();
    const installAt = urls.findIndex((url) => url.endsWith('/panel/api/edge/install-nginx'));
    const applyAt = urls.findIndex((url) => url.endsWith('/panel/api/edge/apply'));
    expect(installAt).toBeGreaterThanOrEqual(0);
    expect(applyAt).toBeGreaterThanOrEqual(0);
    // Install first, apply second: the reverse order is a guaranteed failure.
    expect(installAt).toBeLessThan(applyAt);
  });

  it('shows the package manager commands that ran on the server', async () => {
    mockEdgeApi({ nginxInstalled: false, nginxInstallable: true, installedAfterInstall: true });

    renderTab();
    await screen.findByTestId('edge-nginx-missing');
    await clickPrimary(enUS.pages.settings.edge.installAndApply);

    const steps = await screen.findAllByTestId('edge-install-step');
    expect(steps.map((step) => step.textContent)).toEqual(DEFAULT_STEPS);
    expect(screen.getByText(enUS.pages.settings.edge.installStepsTitle)).toBeTruthy();
  });

  it('renders the install refusal verbatim instead of a generic failure', async () => {
    const refusal = 'edge: apt-get install -y -q nginx failed, so nginx is not installed: exit 100';
    mockEdgeApi({
      nginxInstalled: false,
      nginxInstallable: true,
      installError: refusal,
      installedAfterInstall: false,
    });

    renderTab();
    await screen.findByTestId('edge-nginx-missing');
    await clickPrimary(enUS.pages.settings.edge.installAndApply);

    const error = await screen.findByTestId('edge-install-error');
    expect(error.textContent).toContain(refusal);
    // A failed install must not look like a successful apply.
    expect(applyCalls()).toHaveLength(0);
  });

  it('refreshes the status after a successful install instead of needing a page reload', async () => {
    mockEdgeApi({
      nginxInstalled: false,
      nginxInstallable: true,
      installedAfterInstall: true,
      nginxVersion: '1.24.0',
      nginxBin: '/usr/sbin/nginx',
    });

    renderTab();
    const user = userEvent.setup();
    await screen.findByTestId('edge-nginx-missing');
    await user.click(screen.getByRole('button', { name: enUS.pages.settings.edge.installNginx }));

    // The banner goes away only because /status was read again.
    expect(
      await screen.findByText(enUS.pages.settings.edge.installStepsTitle, undefined, {
        timeout: 3000,
      }),
    ).toBeTruthy();
    expect(screen.queryByTestId('edge-nginx-missing')).toBeNull();
    expect((await screen.findByTestId('edge-nginx-version')).textContent).toBe('1.24.0');
    expect(screen.getByTestId('edge-nginx-bin').textContent).toBe('/usr/sbin/nginx');
  });
});

describe('EdgeTab nginx state in the status card', () => {
  it('reports the installed version and path with no install button', async () => {
    mockEdgeApi({
      nginxInstalled: true,
      nginxVersion: '1.26.2',
      nginxBin: '/usr/local/sbin/nginx',
    });

    renderTab();

    expect((await screen.findByTestId('edge-nginx-version')).textContent).toBe('1.26.2');
    expect(screen.getByTestId('edge-nginx-bin').textContent).toBe('/usr/local/sbin/nginx');
    expect(screen.queryByTestId('edge-nginx-missing')).toBeNull();
    expect(screen.queryByTestId('edge-nginx-blocked')).toBeNull();
  });

  it('surfaces a stale-status apply failure as the missing binary, not a config problem', async () => {
    // The binary vanished after the page loaded: the status still says installed,
    // and only the apply answer carries the real reason.
    mockEdgeApi({
      nginxInstalled: true,
      applyError:
        'somethingWentWrong (nginx is not installed: install it from the panel and apply again)',
    });

    renderTab();
    await screen.findByTestId('edge-running');
    await clickApply();

    const error = await screen.findByTestId('edge-apply-error');
    expect(error.textContent).toContain('nginx is not installed: install it from the panel');
    expect(error.textContent).toContain(enUS.pages.settings.edge.nginxMissingApplyTitle);
    // The generic title would hide the one thing the operator has to fix.
    expect(error.textContent).not.toContain(enUS.pages.settings.edge.applyFailed);
  });
});
