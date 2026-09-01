import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import Logs from '../Logs';

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), del: vi.fn() }));

vi.mock('../../api/client', () => ({
  api: { GET: mocks.get, POST: mocks.post, PUT: mocks.put, DELETE: mocks.del },
  unwrap: async <T,>(promise: Promise<{ data?: T }>): Promise<T> => {
    const { data } = await promise;
    if (data === undefined) {
      throw new Error('Request failed');
    }
    return data;
  },
}));

const page = {
  items: [
    {
      id: 7,
      level: 'warn' as const,
      category: 'promote',
      message: 'deferred, Plex is streaming the file',
      created_at: '2026-08-01T10:00:00.000000000Z',
    },
  ],
  next_since_id: 7,
  has_more: false,
};

function renderLogs() {
  return render(
    <MemoryRouter>
      <Logs />
    </MemoryRouter>,
  );
}

describe('Logs', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.get.mockResolvedValue({ data: page });
  });

  it('reads GET /api/events and renders the level, category and message', async () => {
    renderLogs();

    expect(await screen.findByText('deferred, Plex is streaming the file')).toBeInTheDocument();
    expect(screen.getByText('warn')).toBeInTheDocument();
    expect(screen.getByText('promote')).toBeInTheDocument();
    expect(mocks.get).toHaveBeenCalledWith('/api/events', {
      params: { query: { level: undefined, category: undefined, since_id: undefined, limit: 200 } },
    });
  });

  it('advances the cursor with the id the server handed back', async () => {
    renderLogs();
    await screen.findByText('deferred, Plex is streaming the file');

    fireEvent.click(screen.getByRole('button', { name: 'Jump to latest' }));
    fireEvent.change(screen.getByLabelText('Category'), { target: { value: '' } });

    await vi.waitFor(() => expect(mocks.get).toHaveBeenCalled());
    const lastQuery = mocks.get.mock.calls.at(-1)?.[1].params.query;
    expect(lastQuery.limit).toBe(200);
  });

  it('resets the cursor when the level filter changes', async () => {
    renderLogs();
    await screen.findByText('deferred, Plex is streaming the file');

    fireEvent.change(screen.getByLabelText('Minimum level'), { target: { value: 'error' } });

    await vi.waitFor(() =>
      expect(mocks.get).toHaveBeenCalledWith('/api/events', {
        params: { query: { level: 'error', category: undefined, since_id: undefined, limit: 200 } },
      }),
    );
  });
});
