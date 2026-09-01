import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { MediaDetailModal } from '../MediaDetailModal';
import { job, mediaDetail, transformRecord } from '../../../../test/fixtures';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
}));

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

function stub(media = mediaDetail(), current = job()) {
  mocks.get.mockImplementation((path: string) =>
    Promise.resolve({ data: path === '/api/media/{id}' ? media : current }),
  );
}

function renderModal() {
  return render(
    <MemoryRouter>
      <MediaDetailModal mediaFileId={10} onClose={vi.fn()} />
    </MemoryRouter>,
  );
}

describe('MediaDetailModal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.post.mockResolvedValue({ data: job() });
  });

  it('labels the after column as the plan while the job is unfinished', async () => {
    stub();
    renderModal();

    expect(await screen.findByText('After (planned)')).toBeInTheDocument();
    expect(
      screen.getByText('This job has not produced output yet. The after column is the plan.'),
    ).toBeInTheDocument();
    expect(screen.getByText('actual pending')).toBeInTheDocument();
  });

  it('labels the after column as produced once the job is done', async () => {
    stub(
      mediaDetail(),
      job({
        state: 'done',
        transform: transformRecord({ duration_seconds: { estimated: 1800, actual: 1650 } }),
      }),
    );
    renderModal();

    expect(await screen.findByText('After (produced)')).toBeInTheDocument();
    expect(
      screen.getByText('This job finished. The after column is what was actually produced.'),
    ).toBeInTheDocument();
    expect(screen.getByText('actual 27m 30s')).toBeInTheDocument();
  });

  it('shows each stream action with its reason', async () => {
    stub();
    renderModal();

    await screen.findByText('After (planned)');
    expect(screen.getByText('H.264 High L5.1 exceeds the copy level')).toBeInTheDocument();
    expect(screen.getByText('EAC3 5.1 is on the copy list')).toBeInTheDocument();
    expect(screen.getByText('PGS forces a burn-in transcode')).toBeInTheDocument();
    expect(screen.getByText('Drop')).toBeInTheDocument();
  });

  it('says plainly that a job was interrupted and offers a retry', async () => {
    stub(
      mediaDetail({ status: 'failed' }),
      job({
        state: 'failed',
        attempt: 3,
        failure_code: 'interrupted',
        failure_message: 'Codarr restarted while the job was running',
      }),
    );
    renderModal();

    expect(await screen.findByText('Interrupted')).toBeInTheDocument();
    expect(screen.getByText(/Codarr restarted while this job was running/)).toBeInTheDocument();

    fireEvent.click(screen.getAllByRole('button', { name: 'Retry' })[0]);
    await vi.waitFor(() =>
      expect(mocks.post).toHaveBeenCalledWith('/api/jobs/{id}/restart', { params: { path: { id: 1 } } }),
    );
  });

  it('shows the ffmpeg stderr tail on an ffmpeg failure', async () => {
    stub(
      mediaDetail({ status: 'failed' }),
      job({
        state: 'failed',
        failure_code: 'ffmpeg_failed',
        failure_message: 'exit status 1',
        stderr_tail: 'Error while opening encoder',
      }),
    );
    renderModal();

    expect(await screen.findByText('ffmpeg failed')).toBeInTheDocument();
    expect(screen.getByText('Error while opening encoder')).toBeInTheDocument();
  });

  it('says something rewrote the file and shows both fingerprints', async () => {
    stub(
      mediaDetail({
        provenance: 'modified_since_transcode',
        fingerprint: 'xxh3-128:bbbb',
        codarr_output_fingerprint: 'xxh3-128:aaaa',
      }),
    );
    renderModal();

    expect(
      await screen.findByText('Something rewrote this file after Codarr produced it.'),
    ).toBeInTheDocument();
    expect(screen.getByText('xxh3-128:aaaa')).toBeInTheDocument();
    expect(screen.getByText('xxh3-128:bbbb')).toBeInTheDocument();
  });

  it('keeps the raw argv and probe output collapsed until asked', async () => {
    stub();
    renderModal();

    const toggle = await screen.findByRole('button', { name: 'ffmpeg argv' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText(/ffmpeg \\/)).toBeInTheDocument();
  });

  it('shouts when the encoder fell back to software', async () => {
    stub(mediaDetail(), job({ fell_back: true, encoder_used: 'libx265', fallback_reason: 'QSV busy' }));
    renderModal();

    const alerts = await screen.findAllByRole('alert');
    const fallback = alerts.find((node) => node.textContent?.includes('Fell back to a software encoder.'));
    expect(fallback).toBeDefined();
    expect(within(fallback as HTMLElement).getByText(/QSV busy/)).toBeInTheDocument();
  });
});
