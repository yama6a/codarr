import React from 'react';
import { Badge } from '../ui/Badge';
import { decisionTone } from '../../lib/tone';
import { humanise } from '../../lib/format';
import type { Decision } from '../../api/types';

interface CompareRowProps {
  label: string;
  sublabel?: string;
  action?: Decision;
  reason?: string;
  before: React.ReactNode;
  after: React.ReactNode;
}

export function CompareRow({ label, sublabel, action, reason, before, after }: CompareRowProps) {
  return (
    <div className="border-t border-slate-800 py-3 first:border-t-0">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium text-slate-200">{label}</span>
        {sublabel && <span className="text-xs text-slate-500">{sublabel}</span>}
        {action && <Badge tone={decisionTone(action)}>{humanise(action)}</Badge>}
      </div>
      {reason && <p className="mt-1 text-xs text-slate-400">{reason}</p>}
      <div className="mt-2 grid gap-2 sm:grid-cols-2">
        <div className="rounded-lg bg-slate-900/70 px-3 py-2 text-xs text-slate-300">{before}</div>
        <div className="rounded-lg bg-slate-900/70 px-3 py-2 text-xs text-slate-300">{after}</div>
      </div>
    </div>
  );
}

export function CompareHeader({ afterLabel, afterHint }: { afterLabel: string; afterHint: string }) {
  return (
    <div className="grid gap-2 pb-2 sm:grid-cols-2">
      <div>
        <p className="text-xs font-semibold tracking-wide text-slate-400 uppercase">Before</p>
        <p className="text-[11px] text-slate-500">What is on disk now</p>
      </div>
      <div>
        <p className="text-xs font-semibold tracking-wide text-blue-300 uppercase">{afterLabel}</p>
        <p className="text-[11px] text-slate-500">{afterHint}</p>
      </div>
    </div>
  );
}
