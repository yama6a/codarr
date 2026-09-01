import { useCallback, useEffect, useState } from 'react';
import { api, unwrap } from '../../api/client';
import { Badge } from '../ui/Badge';
import { Button } from '../ui/Button';
import { Icon } from '../ui/Icon';
import { KeyValue } from '../ui/StatTile';
import { LoadingSpinner } from '../ui/LoadingSpinner';
import { Modal } from '../ui/Modal';
import { toast } from '../ui/Toast';
import { FailureSection } from './FailureSection';
import { MediaInfoSection } from './MediaInfoSection';
import { PlanSection } from './PlanSection';
import { ProvenanceSection } from './ProvenanceSection';
import { TechnicalSection } from './TechnicalSection';
import { TransformSections } from './TransformSections';
import { formatBytes, formatDateTime, humanise } from '../../lib/format';
import { jobStateTone, mediaStatusTone, planKindTone } from '../../lib/tone';
import type { IntegrityResult, Job, JobState, MediaDetail } from '../../api/types';

const ACTIVE_STATES: JobState[] = ['queued', 'running', 'verifying', 'awaiting_stream_end', 'promoting'];

interface MediaDetailModalProps {
  mediaFileId: number;
  jobId?: number;
  onClose: () => void;
  onChanged?: () => void;
}

export function MediaDetailModal({ mediaFileId, jobId, onClose, onChanged }: MediaDetailModalProps) {
  const [media, setMedia] = useState<MediaDetail | null>(null);
  const [job, setJob] = useState<Job | null>(null);
  const [integrity, setIntegrity] = useState<IntegrityResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(async () => {
    const detail = await unwrap(
      api.GET('/api/media/{id}', { params: { path: { id: mediaFileId } } }),
    );
    setMedia(detail);

    const target = jobId ?? detail.latest_job_id;
    if (target) {
      setJob(await unwrap(api.GET('/api/jobs/{id}', { params: { path: { id: target } } })));
    } else {
      setJob(null);
    }
  }, [mediaFileId, jobId]);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    load()
      .catch(() => undefined)
      .finally(() => {
        if (alive) {
          setLoading(false);
        }
      });
    return () => {
      alive = false;
    };
  }, [load]);

  const run = useCallback(
    async (name: string, action: () => Promise<void>) => {
      setBusy(name);
      try {
        await action();
        onChanged?.();
      } catch {
        // The client middleware already toasted the detail.
      } finally {
        setBusy(null);
      }
    },
    [onChanged],
  );

  const analyze = () =>
    run('analyze', async () => {
      setMedia(await unwrap(api.POST('/api/media/{id}/analyze', { params: { path: { id: mediaFileId } } })));
      toast.success('Re-probed and re-planned.');
    });

  const enqueue = () =>
    run('queue', async () => {
      const result = await unwrap(api.POST('/api/media/{id}/queue', { params: { path: { id: mediaFileId } } }));
      if (result.enqueued) {
        toast.success(`Queued as ${result.plan_kind ?? 'a job'}.`);
      } else {
        toast.warning(result.reason || 'Nothing to do for this file.');
      }
      await load();
    });

  const toggleIgnore = () =>
    run('ignore', async () => {
      const next = media?.ignored
        ? await unwrap(api.DELETE('/api/media/{id}/ignore', { params: { path: { id: mediaFileId } } }))
        : await unwrap(api.POST('/api/media/{id}/ignore', { params: { path: { id: mediaFileId } } }));
      setMedia(next);
    });

  const verify = () =>
    run('verify', async () => {
      const result = await unwrap(
        api.POST('/api/media/{id}/verify-integrity', { params: { path: { id: mediaFileId } } }),
      );
      setIntegrity(result);
      await load();
    });

  const retry = () =>
    run('retry', async () => {
      if (!job) {
        return;
      }
      setJob(await unwrap(api.POST('/api/jobs/{id}/restart', { params: { path: { id: job.id } } })));
      toast.success('Re-queued at the front.');
    });

  const cancel = () =>
    run('cancel', async () => {
      if (!job) {
        return;
      }
      setJob(await unwrap(api.POST('/api/jobs/{id}/cancel', { params: { path: { id: job.id } } })));
      toast.success('Job cancelled.');
    });

  const produced = job?.state === 'done';
  const active = job !== null && ACTIVE_STATES.includes(job.state);

  return (
    <Modal
      open
      onClose={onClose}
      size="xl"
      title={media?.filename ?? 'Loading'}
      subtitle={media?.path}
      footer={
        media && (
          <>
            <Button variant="ghost" icon="probe" loading={busy === 'analyze'} onClick={analyze}>
              Re-analyze
            </Button>
            <Button variant="ghost" icon="ban" loading={busy === 'ignore'} onClick={toggleIgnore}>
              {media.ignored ? 'Unignore' : 'Ignore'}
            </Button>
            {active ? (
              <Button variant="danger" icon="ban" loading={busy === 'cancel'} onClick={cancel}>
                Cancel job
              </Button>
            ) : (
              <Button variant="primary" icon="play" loading={busy === 'queue'} onClick={enqueue}>
                Queue
              </Button>
            )}
          </>
        )
      }
    >
      {loading || !media ? (
        <LoadingSpinner message="Loading file..." />
      ) : (
        <div className="space-y-6">
          <div className="flex flex-wrap items-center gap-2">
            <Badge tone={mediaStatusTone(media.status)}>{humanise(media.status)}</Badge>
            {media.plan_kind && <Badge tone={planKindTone(media.plan_kind)}>{humanise(media.plan_kind)}</Badge>}
            {job && <Badge tone={jobStateTone(job.state)}>Job #{job.id} {humanise(job.state)}</Badge>}
            {media.ignored && <Badge tone="neutral">Ignored</Badge>}
            {media.is_hdr && <Badge tone="warning">HDR</Badge>}
          </div>

          <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <KeyValue label="Owning instance">{media.arr_instance_name ?? 'none'}</KeyValue>
            <KeyValue label="Size">{formatBytes(media.size_bytes)}</KeyValue>
            <KeyValue label="Container">{media.container ?? 'unknown'}</KeyValue>
            <KeyValue label="Last analysed">{formatDateTime(media.analyzed_at)}</KeyValue>
            <KeyValue label="Hard links">{media.nlink ?? 'unknown'}</KeyValue>
            <KeyValue label="Bitrate source">{media.video_bitrate_source ?? 'unknown'}</KeyValue>
            <KeyValue label="Added">{formatDateTime(media.created_at)}</KeyValue>
            <KeyValue label="Updated">{formatDateTime(media.updated_at)}</KeyValue>
          </dl>

          <p className="font-mono text-xs break-all text-slate-500">{media.path}</p>

          {media.last_error && (
            <p className="rounded-lg border border-red-800 bg-red-950/40 p-3 text-xs text-red-200">{media.last_error}</p>
          )}

          {job && job.state === 'failed' && (
            <FailureSection job={job} retrying={busy === 'retry'} onRetry={retry} />
          )}

          {job && (
            <section>
              <h3 className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">Execution</h3>
              {job.fell_back && (
                <div role="alert" className="mb-3 flex items-start gap-3 rounded-lg border-2 border-red-500 bg-red-950 p-3">
                  <Icon name="alert" size={20} className="mt-0.5 flex-shrink-0 text-red-400" />
                  <p className="text-sm text-red-100">
                    <span className="font-bold">Fell back to a software encoder.</span>{' '}
                    {job.fallback_reason || 'The hardware encoder was unavailable for this job.'}
                  </p>
                </div>
              )}
              <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
                <KeyValue label="Encoder used">{job.encoder_used ?? 'not started'}</KeyValue>
                <KeyValue label="Decode path">{job.decode_path ?? 'not started'}</KeyValue>
                <KeyValue label="Origin">{humanise(job.origin)}</KeyValue>
                <KeyValue label="Attempt">{job.attempt}</KeyValue>
                <KeyValue label="Queued">{formatDateTime(job.queued_at)}</KeyValue>
                <KeyValue label="Started">{formatDateTime(job.started_at)}</KeyValue>
                <KeyValue label="Finished">{formatDateTime(job.finished_at)}</KeyValue>
                <KeyValue label="Staging">
                  {job.staging_path || 'none'}
                  {job.used_temp_dir && <span className="ml-1 text-amber-300">(temp dir)</span>}
                </KeyValue>
              </dl>
            </section>
          )}

          <section>
            <h3 className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">
              Before and after
            </h3>
            {job ? (
              <TransformSections transform={job.transform} produced={produced} />
            ) : media.plan ? (
              <PlanSection plan={media.plan} />
            ) : (
              <p className="text-xs text-slate-500">
                This file has not been analysed yet, so there is no plan to compare against.
              </p>
            )}
          </section>

          <section>
            <h3 className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">Provenance</h3>
            <ProvenanceSection
              media={media}
              integrity={integrity}
              verifying={busy === 'verify'}
              onVerify={verify}
            />
          </section>

          {media.media_info && (
            <section>
              <h3 className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">Media info</h3>
              <MediaInfoSection info={media.media_info} />
            </section>
          )}

          <section>
            <h3 className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">Technical</h3>
            <TechnicalSection
              argv={job?.ffmpeg_argv}
              sourceProbe={media.probe_json}
              outputProbe={job?.probe_result}
            />
          </section>
        </div>
      )}
    </Modal>
  );
}
