import { Panel } from '../ui/Panel';
import type { CompatibilitySummary } from '../../api/types';

const reasonMeta = [
  { key: 'audio', label: 'Audio', bar: 'bg-purple-500' },
  { key: 'subtitles', label: 'Subtitles', bar: 'bg-sky-500' },
  { key: 'video', label: 'Video', bar: 'bg-amber-500' },
  { key: 'container', label: 'Container', bar: 'bg-emerald-500' },
] as const;

/**
 * CompatibilityPanel is the headline number: how many files still force playback transcoding, and
 * why (plan.md 18.1). Everything else on the dashboard is throughput; this is the goal.
 */
export function CompatibilityPanel({ summary }: { summary: CompatibilitySummary }) {
  const denominator = Math.max(1, summary.files_analyzed);
  const compatiblePct = (summary.files_compatible / denominator) * 100;

  return (
    <Panel
      title="Direct play compatibility"
      icon="gauge"
      description="Files that still force Plex to transcode, and the reason each one does."
    >
      <div className="grid gap-6 lg:grid-cols-[auto_1fr]">
        <div className="flex gap-8">
          <div>
            <p className="text-5xl font-bold text-amber-400">{summary.files_needing_work.toLocaleString()}</p>
            <p className="mt-1 text-xs tracking-wide text-slate-400 uppercase">Need work</p>
          </div>
          <div>
            <p className="text-5xl font-bold text-green-400">{summary.files_compatible.toLocaleString()}</p>
            <p className="mt-1 text-xs tracking-wide text-slate-400 uppercase">Compatible</p>
          </div>
        </div>

        <div className="space-y-3">
          <div>
            <div className="mb-1 flex justify-between text-xs text-slate-400">
              <span>{compatiblePct.toFixed(1)}% of analysed files play directly</span>
              <span>{summary.files_unanalyzed.toLocaleString()} not analysed yet</span>
            </div>
            <div className="h-2.5 w-full overflow-hidden rounded-full bg-slate-800">
              <div className="h-full rounded-full bg-green-500" style={{ width: `${compatiblePct}%` }} />
            </div>
          </div>

          <ul className="space-y-2">
            {reasonMeta.map((reason) => {
              const count = summary.by_reason[reason.key];
              const width = summary.files_needing_work > 0 ? (count / summary.files_needing_work) * 100 : 0;
              return (
                <li key={reason.key} className="flex items-center gap-3">
                  <span className="w-20 flex-shrink-0 text-xs text-slate-400">{reason.label}</span>
                  <span className="h-2 flex-1 overflow-hidden rounded-full bg-slate-800">
                    <span className={`block h-full rounded-full ${reason.bar}`} style={{ width: `${width}%` }} />
                  </span>
                  <span className="w-16 flex-shrink-0 text-right text-xs font-medium text-slate-200">
                    {count.toLocaleString()}
                  </span>
                </li>
              );
            })}
          </ul>
          <p className="text-[11px] text-slate-500">
            A file can need work for more than one reason, so these overlap and do not sum to the
            total.
          </p>

          <div className="flex flex-wrap gap-2 pt-1 text-xs text-slate-400">
            <span>Remux {summary.by_plan_kind.remux.toLocaleString()}</span>
            <span className="text-slate-600">|</span>
            <span>Audio only {summary.by_plan_kind.audio_only.toLocaleString()}</span>
            <span className="text-slate-600">|</span>
            <span>Full {summary.by_plan_kind.full.toLocaleString()}</span>
            <span className="text-slate-600">|</span>
            <span>Skip {summary.by_plan_kind.skip.toLocaleString()}</span>
          </div>
        </div>
      </div>
    </Panel>
  );
}
