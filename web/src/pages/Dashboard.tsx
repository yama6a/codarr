import { useCallback, useEffect, useState } from 'react';
import { api, unwrap } from '../api/client';
import { AwaitingPanel } from '../components/dashboard/AwaitingPanel';
import { CompatibilityPanel } from '../components/dashboard/CompatibilityPanel';
import { CompletionsPanel } from '../components/dashboard/CompletionsPanel';
import { CurrentJobPanel } from '../components/dashboard/CurrentJobPanel';
import { FailuresPanel } from '../components/dashboard/FailuresPanel';
import { QueuePanel } from '../components/dashboard/QueuePanel';
import { StatsRow } from '../components/dashboard/StatsRow';
import { MediaDetailModal } from '../components/media/MediaDetailModal';
import { LoadingSpinner } from '../components/ui/LoadingSpinner';
import { toast } from '../components/ui/Toast';
import { usePolling } from '../hooks/usePolling';
import { formatTime } from '../lib/format';
import type { JobState, JobSummary } from '../api/types';

interface Selection {
  mediaFileId: number;
  jobId?: number;
}

const PAGE_SIZE = 25;

/** mergeById appends rows the list does not hold yet; the polled first page shifts under the extra pages. */
function mergeById(base: JobSummary[], extra: JobSummary[]): JobSummary[] {
  const seen = new Set(base.map((job) => job.id));
  return [...base, ...extra.filter((job) => !seen.has(job.id))];
}

/** useMorePages loads further pages of one terminal state below the dashboard's capped list. */
function useMorePages(state: JobState, base: number, resetKey: number) {
  const [extra, setExtra] = useState<JobSummary[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    setExtra([]);
  }, [resetKey]);

  const loadMore = async () => {
    setLoading(true);
    try {
      const page = await unwrap(
        api.GET('/api/jobs', {
          params: { query: { state, page: Math.floor((base + extra.length) / PAGE_SIZE) + 1, page_size: PAGE_SIZE } },
        }),
      );
      setExtra((prev) => mergeById(prev, page.items));
    } catch {
      // Already toasted by the client middleware.
    } finally {
      setLoading(false);
    }
  };

  return { extra, loading, loadMore };
}

export default function Dashboard() {
  // plan.md 18.6: one call every ten seconds, not six.
  const { data, loading, error, refresh } = usePolling(
    useCallback(() => unwrap(api.GET('/api/dashboard')), []),
  );
  const [selection, setSelection] = useState<Selection | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [retryingId, setRetryingId] = useState<number | null>(null);
  const [resetKey, setResetKey] = useState(0);

  const baseFailures = data?.failures ?? [];
  const baseCompletions = data?.recent_completions ?? [];
  const moreFailures = useMorePages('failed', baseFailures.length, resetKey);
  const moreCompletions = useMorePages('done', baseCompletions.length, resetKey);
  const failures = mergeById(baseFailures, moreFailures.extra);
  const completions = mergeById(baseCompletions, moreCompletions.extra);

  const togglePause = async () => {
    if (!data) {
      return;
    }
    setBusy('pause');
    try {
      await unwrap(data.queue_paused ? api.POST('/api/queue/resume') : api.POST('/api/queue/pause'));
      toast.success(data.queue_paused ? 'Queue resumed.' : 'Queue paused.');
      refresh();
    } catch {
      // Already toasted by the client middleware.
    } finally {
      setBusy(null);
    }
  };

  const cancel = async (jobId: number) => {
    setBusy('cancel');
    try {
      await unwrap(api.POST('/api/jobs/{id}/cancel', { params: { path: { id: jobId } } }));
      toast.success('Job cancelled.');
      setResetKey((k) => k + 1);
      refresh();
    } catch {
      // Already toasted.
    } finally {
      setBusy(null);
    }
  };

  const retry = async (jobId: number) => {
    setRetryingId(jobId);
    try {
      await unwrap(api.POST('/api/jobs/{id}/restart', { params: { path: { id: jobId } } }));
      toast.success('Re-queued at the front.');
      setResetKey((k) => k + 1);
      refresh();
    } catch {
      // Already toasted.
    } finally {
      setRetryingId(null);
    }
  };

  if (loading && !data) {
    return <LoadingSpinner message="Loading dashboard..." />;
  }

  if (!data) {
    return (
      <div className="p-8">
        <h1 className="text-2xl font-bold text-white">Dashboard</h1>
        <p className="mt-2 text-sm text-red-400">{error?.message ?? 'The dashboard could not be loaded.'}</p>
      </div>
    );
  }

  return (
    <div className="space-y-6 p-8">
      <header className="flex flex-wrap items-baseline justify-between gap-3">
        <h1 className="text-2xl font-bold text-white">Dashboard</h1>
        <p className="text-xs text-slate-500">
          Polling every 10 seconds. Last update {formatTime(data.generated_at)}.
        </p>
      </header>

      <CurrentJobPanel
        job={data.current_job}
        paused={data.queue_paused}
        cancelling={busy === 'cancel'}
        onCancel={cancel}
        onOpen={(job) => setSelection({ mediaFileId: job.media_file_id, jobId: job.id })}
      />

      <CompatibilityPanel summary={data.compatibility} />

      <StatsRow stats={data.stats} />

      <div className="grid gap-6 xl:grid-cols-2">
        <QueuePanel
          queue={data.queue}
          depth={data.queue_depth}
          paused={data.queue_paused}
          busy={busy === 'pause'}
          onTogglePause={togglePause}
          onOpen={(job) => setSelection({ mediaFileId: job.media_file_id, jobId: job.id })}
        />
        <AwaitingPanel
          items={data.awaiting_stream_end}
          onOpen={(item) => setSelection({ mediaFileId: item.media_file_id, jobId: item.job_id })}
        />
      </div>

      <div className="grid gap-6 xl:grid-cols-2">
        <CompletionsPanel
          jobs={completions}
          total={data.completions_total}
          loadingMore={moreCompletions.loading}
          onLoadMore={moreCompletions.loadMore}
          onOpen={(job) => setSelection({ mediaFileId: job.media_file_id, jobId: job.id })}
        />
        <FailuresPanel
          jobs={failures}
          total={data.failures_total}
          loadingMore={moreFailures.loading}
          onLoadMore={moreFailures.loadMore}
          retryingId={retryingId}
          onRetry={retry}
          onOpen={(job) => setSelection({ mediaFileId: job.media_file_id, jobId: job.id })}
        />
      </div>

      {selection && (
        <MediaDetailModal
          mediaFileId={selection.mediaFileId}
          jobId={selection.jobId}
          onClose={() => setSelection(null)}
          onChanged={refresh}
        />
      )}
    </div>
  );
}
