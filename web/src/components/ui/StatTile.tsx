import React from 'react';
import { Icon, type IconName } from './Icon';

interface StatTileProps {
  label: string;
  value: string;
  icon?: IconName;
  hint?: string;
  tone?: 'default' | 'good' | 'bad';
}

const valueTone = {
  default: 'text-white',
  good: 'text-green-400',
  bad: 'text-red-400',
};

export function StatTile({ label, value, icon, hint, tone = 'default' }: StatTileProps) {
  return (
    <div className="rounded-xl border border-slate-800 bg-surface-dark p-4">
      <div className="flex items-center gap-2 text-xs font-medium tracking-wide text-slate-400 uppercase">
        {icon && <Icon name={icon} size={14} />}
        {label}
      </div>
      <p className={`mt-2 text-2xl font-semibold ${valueTone[tone]}`}>{value}</p>
      {hint && <p className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}

interface KeyValueProps {
  label: string;
  children: React.ReactNode;
  className?: string;
}

export function KeyValue({ label, children, className = '' }: KeyValueProps) {
  return (
    <div className={className}>
      <dt className="text-[11px] font-medium tracking-wide text-slate-500 uppercase">{label}</dt>
      <dd className="mt-0.5 text-sm text-slate-200">{children}</dd>
    </div>
  );
}
