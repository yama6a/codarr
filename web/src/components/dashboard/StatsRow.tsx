import { StatTile } from '../ui/StatTile';
import { formatBytes, formatHours, formatPercent, formatSignedBytes } from '../../lib/format';
import type { Stats } from '../../api/types';

export function StatsRow({ stats }: { stats: Stats }) {
  return (
    <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <StatTile
        label="Space saved"
        icon="disk"
        // bytes_saved is signed: an AV1 source re-encoded to HEVC grows by design (plan.md 18.1).
        value={formatSignedBytes(stats.bytes_saved)}
        tone={stats.bytes_saved < 0 ? 'bad' : 'good'}
        hint={`${formatBytes(stats.bytes_in)} in, ${formatBytes(stats.bytes_out)} out${
          stats.avg_saving_pct !== undefined ? `, ${formatPercent(stats.avg_saving_pct)} average` : ''
        }`}
      />
      <StatTile
        label="Files done"
        icon="check"
        value={stats.files_done.toLocaleString()}
        hint={`${stats.files_total.toLocaleString()} known, ${stats.files_pending.toLocaleString()} pending`}
      />
      <StatTile
        label="Encode hours"
        icon="clock"
        value={formatHours(stats.encode_seconds)}
        hint={`${stats.jobs_done.toLocaleString()} jobs done, ${stats.jobs_failed.toLocaleString()} failed`}
      />
      <StatTile
        label="Not processed"
        icon="ban"
        value={(stats.files_skipped + stats.files_ignored + stats.files_missing).toLocaleString()}
        hint={`${stats.files_skipped.toLocaleString()} skipped, ${stats.files_ignored.toLocaleString()} ignored, ${stats.files_missing.toLocaleString()} missing`}
      />
    </div>
  );
}
