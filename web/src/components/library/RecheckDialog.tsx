import { ConfirmDialog } from '../ui/ConfirmDialog';
import { LoadingSpinner } from '../ui/LoadingSpinner';
import { PlanKindBreakdownList } from './PlanKindBreakdownList';
import type { RecheckResult } from '../../api/types';

interface RecheckDialogProps {
  open: boolean;
  title: string;
  scope: string;
  preview: RecheckResult | null;
  busy: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

// Only ever opens on the back of a `confirm: false` dry run, which plan.md 15.5 requires of
// anything touching more than one file.
export function RecheckDialog({ open, title, scope, preview, busy, onConfirm, onCancel }: RecheckDialogProps) {
  return (
    <ConfirmDialog
      open={open}
      title={title}
      confirmLabel={preview ? `Queue ${preview.count.toLocaleString()} jobs` : 'Queue'}
      busy={busy}
      confirmDisabled={!preview || preview.count === 0}
      onConfirm={onConfirm}
      onCancel={onCancel}
    >
      {!preview ? (
        <LoadingSpinner message="Dry run in progress..." />
      ) : (
        <div className="space-y-3">
          <p className="text-sm text-slate-200">
            Dry run over {scope}. {preview.examined.toLocaleString()} files were re-probed and
            re-planned.
          </p>
          <p className="text-sm text-slate-200">
            <span className="text-2xl font-bold text-white">{preview.count.toLocaleString()}</span> no
            longer match the current policy and would be queued.
          </p>
          <PlanKindBreakdownList breakdown={preview.by_plan_kind} />
          {preview.count === 0 && (
            <p className="text-xs text-slate-400">Nothing to queue. Everything already matches the policy.</p>
          )}
        </div>
      )}
    </ConfirmDialog>
  );
}
