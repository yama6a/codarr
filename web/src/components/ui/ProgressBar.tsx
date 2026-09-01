interface ProgressBarProps {
  pct: number;
  tone?: 'primary' | 'warning';
  label?: string;
}

export function ProgressBar({ pct, tone = 'primary', label }: ProgressBarProps) {
  const clamped = Math.min(100, Math.max(0, Number.isFinite(pct) ? pct : 0));
  const fill = tone === 'warning' ? 'bg-amber-500' : 'bg-primary';

  return (
    <div
      role="progressbar"
      aria-valuenow={Math.round(clamped)}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-label={label ?? 'Progress'}
      className="h-2.5 w-full overflow-hidden rounded-full bg-slate-800"
    >
      <div className={`h-full rounded-full transition-[width] duration-700 ${fill}`} style={{ width: `${clamped}%` }} />
    </div>
  );
}
