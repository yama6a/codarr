import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import Dashboard from '../Dashboard';
import { dashboard, jobSummary } from '../../../test/fixtures';

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
});
