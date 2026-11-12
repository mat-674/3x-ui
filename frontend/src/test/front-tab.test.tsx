import { screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import FrontTab from '@/pages/settings/FrontTab';
import { AllSetting } from '@/models/setting';
import { HttpUtil, Msg } from '@/utils';
import enUS from '../../../internal/web/translation/en-US.json';
import { renderWithProviders } from './test-utils';

const DAY_MS = 86_400_000;

function certRow(id: number, notAfterInDays: number | null, domains = 'panel.example.com') {
  return {
    id,
    domains,
    mode: 'file',
    certPath: `/etc/x-ui/front/certs/${id}.crt`,
    keyPath: `/etc/x-ui/front/certs/${id}.key`,
    issuer: 'file',
    lastError: '',
    notAfter:
      notAfterInDays === null ? null : new Date(Date.now() + notAfterInDays * DAY_MS).toISOString(),
  };
}

function mockFrontApi(certs: unknown[]) {
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
    if (url.endsWith('/panel/api/front/certs')) return new Msg(true, '', certs);
    if (url.endsWith('/panel/api/front/sites')) return new Msg(true, '', []);
    return new Msg(true, '', null);
  });
}

function renderTab() {
  renderWithProviders(<FrontTab allSetting={new AllSetting()} updateSetting={vi.fn()} />);
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('FrontTab certificate warnings', () => {
  it('flags a certificate expiring inside the warning window instead of only listing its date', async () => {
    mockFrontApi([certRow(1, 3), certRow(2, 120)]);

    renderTab();

    const expiring = await screen.findByTestId('front-cert-expiry-1');
    expect(expiring.textContent).toMatch(/Renew within \d+ days?/);
    // The healthy row must stay unremarkable, or the warning means nothing.
    expect(screen.queryByTestId('front-cert-expiry-2')).toBeNull();
  });

  it('marks a certificate that already expired', async () => {
    mockFrontApi([certRow(7, -5)]);

    renderTab();

    const expired = await screen.findByTestId('front-cert-expiry-7');
    expect(expired.textContent).toMatch(/Expired \d+ days ago/);
  });

  it('warns loudly when the front has no certificate at all', async () => {
    mockFrontApi([]);

    renderTab();

    const warning = await screen.findByTestId('front-no-cert');
    expect(warning.textContent).toContain(enUS.pages.settings.front.noCert);
  });

  it('drops the no-certificate warning once a certificate exists', async () => {
    mockFrontApi([certRow(4, 200)]);

    renderTab();

    await screen.findByText(/days left/);
    expect(screen.queryByTestId('front-no-cert')).toBeNull();
  });
});
