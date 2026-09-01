import React from 'react';

export type BadgeTone = 'neutral' | 'info' | 'success' | 'warning' | 'danger' | 'accent';

const toneClasses: Record<BadgeTone, string> = {
  neutral: 'border-slate-700 bg-slate-800 text-slate-300',
  info: 'border-sky-800 bg-sky-950 text-sky-300',
  success: 'border-green-800 bg-green-950 text-green-300',
  warning: 'border-amber-700 bg-amber-950 text-amber-300',
  danger: 'border-red-800 bg-red-950 text-red-300',
  accent: 'border-primary/60 bg-primary/15 text-blue-300',
};

interface BadgeProps {
  tone?: BadgeTone;
  children: React.ReactNode;
  title?: string;
  className?: string;
}

export function Badge({ tone = 'neutral', children, title, className = '' }: BadgeProps) {
  return (
    <span
      title={title}
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium whitespace-nowrap ${toneClasses[tone]} ${className}`}
    >
      {children}
    </span>
  );
}
