import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import Dashboard from '../Dashboard';
import { completion, dashboard, jobSummary } from '../../../test/fixtures';

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

function renderDashboard() {
  return render(
    <MemoryRouter>
      <Dashboard />
    </MemoryRouter>,
  );
}

describe('Dashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.post.mockResolvedValue({ data: {} });
  });

  it('polls GET /api/dashboard once and renders the current job', async () => {
    mocks.get.mockResolvedValue({ data: dashboard({ current_job: jobSummary() }) });

    renderDashboard();

    expect(await screen.findAllByText('Arrival.mkv')).toHaveLength(2);
    expect(mocks.get).toHaveBeenCalledTimes(1);
    expect(mocks.get).toHaveBeenCalledWith('/api/dashboard');
    expect(screen.getByRole('progressbar', { name: 'Encode progress' })).toHaveAttribute(
      'aria-valuenow',
      '43',
    );
    expect(screen.getByText('3.20x realtime')).toBeInTheDocument();
    expect(screen.getByText('118.5')).toBeInTheDocument();
    expect(screen.getByText('hevc_qsv')).toBeInTheDocument();
  });

  it('shouts about a software encoder fallback', async () => {
    mocks.get.mockResolvedValue({
      data: dashboard({
        current_job: jobSummary({
          fell_back: true,
          encoder_used: 'libx265',
          fallback_reason: 'QSV device busy',
        }),
      }),
    });

    renderDashboard();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Software encoder fallback');
    expect(alert).toHaveTextContent('QSV device busy');
    expect(alert.className).toContain('border-red-500');
  });

  it('renders a negative space saving with its sign', async () => {
    mocks.get.mockResolvedValue({ data: dashboard() });

    renderDashboard();

    expect(await screen.findByText('-190.7 MB')).toBeInTheDocument();
  });

  it('breaks the compatibility summary down by reason', async () => {
    mocks.get.mockResolvedValue({ data: dashboard() });

    renderDashboard();

    expect(await screen.findByText('Direct play compatibility')).toBeInTheDocument();
    expect(screen.getByText('30')).toBeInTheDocument();
    expect(screen.getByText('60')).toBeInTheDocument();
    for (const reason of ['Audio', 'Subtitles', 'Video', 'Container']) {
      expect(screen.getByText(reason)).toBeInTheDocument();
    }
  });

  it('badges a queued job that was auto-requeued after an interruption', async () => {
    mocks.get.mockResolvedValue({ data: dashboard() });

    renderDashboard();

    expect(await screen.findByText('Attempt 2')).toBeInTheDocument();
  });

  it('cancels the running job and polls again immediately', async () => {
    mocks.get.mockResolvedValue({ data: dashboard({ current_job: jobSummary() }) });

    renderDashboard();
    (await screen.findByRole('button', { name: 'Cancel' })).click();

    await vi.waitFor(() => {
      expect(mocks.post).toHaveBeenCalledWith('/api/jobs/{id}/cancel', {
        params: { path: { id: 1 } },
      });
    });
    await vi.waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(2));
  });

  it('pauses the queue', async () => {
    mocks.get.mockResolvedValue({ data: dashboard() });

    renderDashboard();
    (await screen.findByRole('button', { name: 'Pause' })).click();

    await vi.waitFor(() => expect(mocks.post).toHaveBeenCalledWith('/api/queue/pause'));
  });

  it('says how many failures exist beyond the capped list and pages them in', async () => {
    const failed = jobSummary({
      id: 9,
      state: 'failed',
      failure_code: 'ffmpeg_failed',
      failure_message: 'boom',
    });
    mocks.get.mockImplementation((path: string) => {
      if (path === '/api/jobs') {
        return Promise.resolve({
          data: {
            items: [{ ...failed, id: 10, media_filename: 'Older.mkv' }],
            page: 2,
            page_size: 25,
            total: 26,
          },
        });
      }
      return Promise.resolve({ data: dashboard({ failures: [failed], failures_total: 26 }) });
    });
    renderDashboard();

    expect(await screen.findByText('Failures (latest 1 of 26)')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));

    expect(await screen.findByText('Older.mkv')).toBeInTheDocument();
    expect(mocks.get).toHaveBeenCalledWith('/api/jobs', {
      params: { query: { state: 'failed', page: 1, page_size: 25 } },
    });
  });

  it('lists skipped files among the completions with their analysis time', async () => {
    mocks.get.mockResolvedValue({
      data: dashboard({
        recent_completions: [
          completion(),
          completion({
            job_id: null,
            media_file_id: 11,
            media_filename: 'Dune.mkv',
            kind: [],
            skipped: true,
            source_size: null,
            output_size: null,
            actual_seconds: null,
          }),
        ],
        completions_total: 2,
      }),
    });
    renderDashboard();

    expect(await screen.findByText('Completions (2)')).toBeInTheDocument();
    expect(screen.getByText('Dune.mkv')).toBeInTheDocument();
    expect(screen.getByText('Skipped')).toBeInTheDocument();
    expect(screen.getByText('nothing to do')).toBeInTheDocument();
    expect(screen.getByText('took 10m 00s')).toBeInTheDocument();
  });
});
