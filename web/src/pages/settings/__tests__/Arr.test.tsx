import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import SettingsArr from '../Arr';
import { arrInstance } from '../../../../test/fixtures';
import type { Root } from '../../../api/types';

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), del: vi.fn() }));

vi.mock('../../../api/client', () => ({
  api: { GET: mocks.get, POST: mocks.post, PUT: mocks.put, DELETE: mocks.del },
  unwrap: async <T,>(promise: Promise<{ data?: T }>): Promise<T> => {
    const { data } = await promise;
    if (data === undefined) {
      throw new Error('Request failed');
    }
    return data;
  },
}));

function root(id: number, arrInstanceId: number): Root {
  return {
    id,
    path: '/media/movies',
    imported: true,
    enabled: true,
    arr_instance_id: arrInstanceId,
    created_at: '2026-07-01T10:00:00.000000000Z',
  };
}

function stub(instances = [arrInstance()], roots: Root[] = []) {
  mocks.get.mockImplementation((path: string) =>
    Promise.resolve({ data: path === '/api/arr' ? instances : roots }),
  );
}

function renderArr() {
  return render(
    <MemoryRouter>
      <SettingsArr />
    </MemoryRouter>,
  );
}

describe('Settings, Radarr and Sonarr', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows the webhook URL to paste into the instance', async () => {
    stub();
    renderArr();

    expect(await screen.findByText(`${window.location.origin}/api/webhook/abc123`)).toBeInTheDocument();
  });

  it('sends the masked key straight back when only toggling enabled', async () => {
    stub();
    mocks.put.mockResolvedValue({ data: arrInstance({ enabled: false }) });
    renderArr();

    fireEvent.click(await screen.findByRole('button', { name: 'Disable' }));

    await vi.waitFor(() => expect(mocks.put).toHaveBeenCalled());
    const body = mocks.put.mock.calls[0][1].body;
    expect(body.api_key).toBe('********');
    expect(body.enabled).toBe(false);
  });

  it('errors clearly when two enabled instances claim the same root', async () => {
    stub(
      [arrInstance({ id: 1, name: 'radarr' }), arrInstance({ id: 2, name: 'radarr-4k' })],
      [root(1, 1), root(2, 2)],
    );
    renderArr();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Two enabled instances claim the same tree');
    expect(alert).toHaveTextContent('radarr-4k');
    expect(alert).toHaveTextContent('Codarr never guesses an owner');
  });

  it('does not warn when one instance owns the tree', async () => {
    stub([arrInstance()], [root(1, 1)]);
    renderArr();

    await screen.findByText('radarr-4k');
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
