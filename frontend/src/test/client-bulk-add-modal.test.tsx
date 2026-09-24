import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';

import { renderWithProviders } from './test-utils';
import type { InboundOption } from '@/hooks/useClients';

const { bulkCreate } = vi.hoisted(() => ({ bulkCreate: vi.fn() }));

vi.mock('@/hooks/useClients', () => ({
  useClients: () => ({ bulkCreate }),
}));

import ClientBulkAddModal from '@/pages/clients/ClientBulkAddModal';

const NAIVE_INBOUND = {
  id: 17,
  tag: 'naive-proxy',
  protocol: 'naive',
  enable: true,
} as InboundOption;

describe('ClientBulkAddModal NaiveProxy support', () => {
  it('offers Naive inbounds and submits a generated password', async () => {
    bulkCreate.mockResolvedValue({ success: true, obj: { created: 1, skipped: [] } });
    renderWithProviders(
      <ClientBulkAddModal open inbounds={[NAIVE_INBOUND]} onOpenChange={() => {}} />,
    );

    await screen.findByRole('button', { name: 'Create' });
    fireEvent.click(screen.getByRole('button', { name: 'Select all' }));
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(bulkCreate).toHaveBeenCalledTimes(1));
    expect(bulkCreate).toHaveBeenCalledWith([
      expect.objectContaining({
        inboundIds: [NAIVE_INBOUND.id],
        client: expect.objectContaining({
          email: expect.any(String),
          password: expect.any(String),
        }),
      }),
    ]);
  });
});
