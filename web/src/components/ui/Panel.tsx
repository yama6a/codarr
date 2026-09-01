import React from 'react';
import { Icon, type IconName } from './Icon';

interface PanelProps {
  title: string;
  icon?: IconName;
  actions?: React.ReactNode;
  description?: string;
  children: React.ReactNode;
  className?: string;
}

export function Panel({ title, icon, actions, description, children, className = '' }: PanelProps) {
  return (
    <section className={`rounded-xl border border-slate-800 bg-surface-dark ${className}`}>
      <header className="flex items-start justify-between gap-4 border-b border-slate-800 px-5 py-3.5">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold tracking-wide text-white uppercase">
            {icon && <Icon name={icon} size={16} className="text-slate-400" />}
            {title}
          </h2>
          {description && <p className="mt-1 text-xs text-slate-400">{description}</p>}
        </div>
        {actions && <div className="flex flex-shrink-0 items-center gap-2">{actions}</div>}
      </header>
      <div className="p-5">{children}</div>
    </section>
  );
}
