import { ConfirmDialog } from '../ui/ConfirmDialog';
import { LoadingSpinner } from '../ui/LoadingSpinner';
import { PlanKindBreakdownList } from './PlanKindBreakdownList';
import { formatBytes, formatPercent } from '../../lib/format';
import type { SpaceSweepPreview } from '../../api/types';

interface SpaceSweepDialogProps {
  open: boolean;
  preview: SpaceSweepPreview | null;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function SpaceSweepDialog({ open, preview, busy, onConfirm, onCancel }: SpaceSweepDialogProps) {
  return (
    <ConfirmDialog
      open={open}
      title="Space reclaim sweep"
      confirmLabel={preview ? `Queue ${preview.count.toLocaleString()} re-encodes` : 'Queue'}
      busy={busy}
      confirmDisabled={!preview || preview.count === 0}
      onConfirm={onConfirm}
      onCancel={onCancel}
    >
      {!preview ? (
        <LoadingSpinner message="Sample-probing candidates..." />
      ) : (
        <div className="space-y-3">
          <p className="text-sm text-slate-200">
            {preview.examined.toLocaleString()} H.264 files above the policy bitrate threshold were
            sample-probed. {preview.count.toLocaleString()} clear the minimum projected saving.
          </p>
          <div className="rounded-lg border border-slate-800 bg-slate-900/60 p-3 text-sm">
            <p className="text-slate-300">
              {formatBytes(preview.current_bytes)} now, {formatBytes(preview.projected_bytes)} projected
            </p>
            <p className="mt-1 text-lg font-semibold text-green-400">
              {formatBytes(preview.projected_saving_bytes)} saved ({formatPercent(preview.projected_saving_pct)})
            </p>
          </div>
          <PlanKindBreakdownList breakdown={preview.by_plan_kind} />
          <p className="text-xs text-slate-400">
            These are projections from a short sample encode, not measurements. The real saving will
            differ.
          </p>
        </div>
      )}
    </ConfirmDialog>
  );
}
