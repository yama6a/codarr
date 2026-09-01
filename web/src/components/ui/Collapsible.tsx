import React, { useState } from 'react';
import { Icon } from './Icon';

interface CollapsibleProps {
  title: string;
  defaultOpen?: boolean;
  children: React.ReactNode;
}

export function Collapsible({ title, defaultOpen = false, children }: CollapsibleProps) {
  const [open, setOpen] = useState(defaultOpen);

  return (
    <div className="rounded-lg border border-slate-800">
      <button
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="flex w-full items-center justify-between gap-2 px-4 py-2.5 text-left text-sm font-medium text-slate-200 hover:bg-slate-800/50"
      >
        {title}
        <Icon name={open ? 'chevron_up' : 'chevron_down'} size={18} className="text-slate-400" />
      </button>
      {open && <div className="border-t border-slate-800 p-4">{children}</div>}
    </div>
  );
}
