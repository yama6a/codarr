import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import Library from '../Library';
import { arrInstance, mediaListItem, mediaPage } from '../../../test/fixtures';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
}));

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

const items = [
  mediaListItem(),
  mediaListItem({
    id: 11,
    filename: 'Dune.mkv',
    path: '/media/movies/Dune (2021)/Dune.mkv',
    provenance: 'modified_since_transcode',
    status: 'done',
    plan_kind: 'remux',
  }),
];

function stubGet() {
  mocks.get.mockImplementation((path: string) => {
    if (path === '/api/arr') {
      return Promise.resolve({ data: [arrInstance()] });
    }
    return Promise.resolve({ data: mediaPage(items, 40_000) });
  });
}

function renderLibrary() {
  return render(
    <MemoryRouter>
      <Library />
    </MemoryRouter>,
  );
}

describe('Library', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    stubGet();
  });

  it('asks the server for the filtered, sorted page', async () => {
    renderLibrary();

    expect(await screen.findByText('Arrival')).toBeInTheDocument();
    expect(mocks.get).toHaveBeenCalledWith('/api/media', {
      params: { query: { sort: '-updated_at', page: 1, page_size: 50 } },
    });
  });

  it('renders the codec, audio, subtitle and provenance columns', async () => {
    renderLibrary();

    const row = (await screen.findByText('Arrival')).closest('tr') as HTMLTableRowElement;
    expect(within(row).getByText(/h264 High L5.1/)).toBeInTheDocument();
    expect(within(row).getByText('3840x2160, 42.0 Mbps')).toBeInTheDocument();
    expect(within(row).getByText('eac3 6ch')).toBeInTheDocument();
    expect(within(row).getByText('subrip')).toBeInTheDocument();
    expect(within(row).getByText('Untouched')).toBeInTheDocument();
  });

  it('marks a file that was rewritten after Codarr produced it', async () => {
    renderLibrary();

    const row = (await screen.findByText('Dune')).closest('tr') as HTMLTableRowElement;
    expect(row.className).toContain('border-l-red-500');
    expect(within(row).getByText('Modified since transcode')).toBeInTheDocument();
  });

  it('filters straight to the modified-since-transcode view', async () => {
    renderLibrary();
    await screen.findByText('Arrival');

    fireEvent.click(screen.getByRole('button', { name: 'Show files changed after Codarr wrote them' }));

    await vi.waitFor(() =>
      expect(mocks.get).toHaveBeenCalledWith('/api/media', {
        params: {
          query: {
            provenance: 'modified_since_transcode',
            sort: '-updated_at',
            page: 1,
            page_size: 50,
          },
        },
      }),
    );
  });

  it('sends a filter rather than 40k ids when selecting everything matching', async () => {
    mocks.post.mockResolvedValue({
      data: {
        dry_run: true,
        examined: 40_000,
        count: 1200,
        by_plan_kind: { skip: 0, remux: 400, audio_only: 300, full: 500 },
        queued_job_ids: [],
      },
    });

    renderLibrary();
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Arrival.mkv' }));
    fireEvent.click(screen.getByRole('button', { name: 'Select all 40,000 matching the filter' }));
    fireEvent.click(screen.getByRole('button', { name: 'Re-encode selected' }));

    await vi.waitFor(() =>
      expect(mocks.post).toHaveBeenCalledWith('/api/media/recheck-selected', {
        body: { confirm: false, filter: {} },
      }),
    );
    expect(mocks.post.mock.calls[0][1].body.ids).toBeUndefined();
  });

  it('dry-runs first and states that the operation cannot be undone', async () => {
    mocks.post.mockResolvedValue({
      data: {
        dry_run: true,
        examined: 900,
        count: 120,
        by_plan_kind: { skip: 0, remux: 40, audio_only: 30, full: 50 },
        queued_job_ids: [],
      },
    });

    renderLibrary();
    await screen.findByText('Arrival');
    fireEvent.click(screen.getByRole('button', { name: 'Re-check all' }));

    const dialog = await screen.findByRole('dialog', { name: 'Re-check every done file' });
    expect(mocks.post).toHaveBeenCalledWith('/api/media/recheck-all', { body: { confirm: false } });
    expect(await within(dialog).findByText('120')).toBeInTheDocument();
    expect(within(dialog).getByText('This cannot be undone.')).toBeInTheDocument();
    expect(within(dialog).getByText(/Radarr or Sonarr re-fetching it/)).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Queue 120 jobs' }));
    await vi.waitFor(() =>
      expect(mocks.post).toHaveBeenCalledWith('/api/media/recheck-all', { body: { confirm: true } }),
    );
  });
});
