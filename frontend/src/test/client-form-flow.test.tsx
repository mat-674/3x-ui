import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent, waitFor, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { ThemeProvider } from '@/hooks/useTheme';
import ClientFormModal from '@/pages/clients/ClientFormModal';
import type { ClientRecord, InboundOption } from '@/hooks/useClients';

function makeQC() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

const REALITY_INBOUND = {
  id: 4,
  port: 10443,
  protocol: 'vless',
  tag: 'in-10443-tcp',
  tlsFlowCapable: true,
  enable: true,
} as unknown as InboundOption;

const NAIVE_INBOUND = {
  id: 7,
  tag: 'naive-proxy',
  protocol: 'naive',
  enable: true,
} as InboundOption;

const CLIENT = {
  email: 'testuser',
  flow: 'xtls-rprx-vision',
  uuid: '11111111-1111-1111-1111-111111111111',
  subId: 'subid123',
  enable: true,
} as unknown as ClientRecord;

function savedFlow(save: ReturnType<typeof vi.fn>): unknown {
  return (save.mock.calls[0][0] as Record<string, unknown>).flow;
}

describe('ClientFormModal — Vision flow preservation', () => {
  it('keeps xtls-rprx-vision with a stable Reality inbound', async () => {
    const qc = makeQC();
    const save = vi.fn().mockResolvedValue({ success: true });
    render(
      <ThemeProvider>
        <QueryClientProvider client={qc}>
          <ClientFormModal
            open
            mode="edit"
            client={CLIENT}
            inbounds={[REALITY_INBOUND]}
            attachedIds={[4]}
            save={save}
            onOpenChange={() => {}}
          />
        </QueryClientProvider>
      </ThemeProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: /save/i }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(savedFlow(save)).toBe('xtls-rprx-vision');
  });

  it('saves the shared username and password for a Naive client', async () => {
    const qc = makeQC();
    const save = vi.fn().mockResolvedValue({ success: true });
    const naiveClient = { ...CLIENT, password: 'naive-secret' } as ClientRecord;
    render(
      <ThemeProvider>
        <QueryClientProvider client={qc}>
          <ClientFormModal
            open
            mode="edit"
            client={naiveClient}
            inbounds={[NAIVE_INBOUND]}
            attachedIds={[NAIVE_INBOUND.id]}
            save={save}
            onOpenChange={() => {}}
          />
        </QueryClientProvider>
      </ThemeProvider>,
    );

    fireEvent.click(await screen.findByRole('tab', { name: 'Credentials' }));
    await screen.findByDisplayValue('naive-secret');
    fireEvent.click(await screen.findByRole('button', { name: /save/i }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect((save.mock.calls[0][0] as Record<string, unknown>).email).toBe('testuser');
    expect((save.mock.calls[0][0] as Record<string, unknown>).password).toBe('naive-secret');
  });

  it('creates a Naive client with its username and generated password', async () => {
    const qc = makeQC();
    const save = vi.fn().mockResolvedValue({ success: true });
    render(
      <ThemeProvider>
        <QueryClientProvider client={qc}>
          <ClientFormModal
            open
            mode="add"
            client={null}
            inbounds={[NAIVE_INBOUND]}
            save={save}
            onOpenChange={() => {}}
          />
        </QueryClientProvider>
      </ThemeProvider>,
    );

    fireEvent.click(await screen.findByRole('button', { name: 'Select all' }));
    fireEvent.click(await screen.findByRole('button', { name: /create/i }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    const payload = save.mock.calls[0][0] as {
      client: Record<string, unknown>;
      inboundIds: number[];
    };
    expect(payload.inboundIds).toEqual([NAIVE_INBOUND.id]);
    expect(payload.client.email).toEqual(expect.any(String));
    expect(String(payload.client.password).length).toBeGreaterThan(0);
  });

  it('requires a password when a Naive inbound is selected', async () => {
    const qc = makeQC();
    const save = vi.fn().mockResolvedValue({ success: true });
    const naiveClient = { ...CLIENT, password: 'naive-secret' } as ClientRecord;
    render(
      <ThemeProvider>
        <QueryClientProvider client={qc}>
          <ClientFormModal
            open
            mode="edit"
            client={naiveClient}
            inbounds={[NAIVE_INBOUND]}
            attachedIds={[NAIVE_INBOUND.id]}
            save={save}
            onOpenChange={() => {}}
          />
        </QueryClientProvider>
      </ThemeProvider>,
    );

    fireEvent.click(await screen.findByRole('tab', { name: 'Credentials' }));
    const passwordInput = await screen.findByDisplayValue('naive-secret');
    fireEvent.change(passwordInput, { target: { value: '' } });
    fireEvent.click(await screen.findByRole('button', { name: /save/i }));
    expect(save).not.toHaveBeenCalled();
  });
});
