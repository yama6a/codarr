import type { PlanKindBreakdown } from '../../api/types';

const rows: { key: keyof PlanKindBreakdown; label: string }[] = [
  { key: 'video', label: 'Video' },
  { key: 'audio', label: 'Audio' },
  { key: 'subtitles', label: 'Subtitles' },
  { key: 'remux', label: 'Remux' },
  { key: 'skip', label: 'Skipped' },
];

export function PlanKindBreakdownList({ breakdown }: { breakdown: PlanKindBreakdown }) {
  return (
    <dl className="grid grid-cols-2 gap-2 rounded-lg border border-slate-800 bg-slate-900/60 p-3 text-sm">
      {rows.map((row) => (
        <div key={row.key} className="flex justify-between gap-3">
          <dt className="text-slate-400">{row.label}</dt>
          <dd className="font-medium text-slate-100">{breakdown[row.key].toLocaleString()}</dd>
        </div>
      ))}
      <p className="col-span-2 text-[11px] text-slate-500">A file counts under every label it carries.</p>
    </dl>
  );
}
