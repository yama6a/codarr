import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { Icon, type IconName } from './Icon';

export type ToastVariant = 'default' | 'success' | 'error' | 'warning';

export interface Toast {
  id: string;
  title?: string;
  description: string;
  variant: ToastVariant;
  duration: number;
}

export interface ToastOptions {
  title?: string;
  description: string;
  variant?: ToastVariant;
  duration?: number;
}

type ToastListener = (toast: Toast) => void;

// A singleton rather than a hook, so the API client can raise a toast from outside React and every
// caller only has to handle the success path.
class ToastManager {
  private listeners = new Set<ToastListener>();
  private idCounter = 0;

  subscribe(listener: ToastListener): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  show(options: ToastOptions): string {
    const id = `toast-${++this.idCounter}`;
    const next: Toast = {
      id,
      title: options.title,
      description: options.description,
      variant: options.variant ?? 'default',
      duration: options.duration ?? 5000,
    };
    this.listeners.forEach((listener) => listener(next));
    return id;
  }

  success(description: string, title?: string) {
    return this.show({ description, title, variant: 'success' });
  }

  error(description: string, title = 'Error') {
    return this.show({ description, title, variant: 'error' });
  }

  warning(description: string, title?: string) {
    return this.show({ description, title, variant: 'warning' });
  }
}

// eslint-disable-next-line react-refresh/only-export-components
export const toast = new ToastManager();

interface ToastContextValue {
  toasts: Toast[];
  dismiss: (id: string) => void;
  dismissAll: () => void;
}

const ToastContext = createContext<ToastContextValue | undefined>(undefined);

// eslint-disable-next-line react-refresh/only-export-components
export function useToast(): ToastContextValue {
  const context = useContext(ToastContext);
  if (!context) {
    throw new Error('useToast must be used within a ToastProvider');
  }
  return context;
}

const MAX_TOASTS = 5;

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);

  useEffect(
    () =>
      toast.subscribe((next) => {
        setToasts((prev) => [...prev, next].slice(-MAX_TOASTS));
      }),
    [],
  );

  const dismiss = useCallback((id: string) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const dismissAll = useCallback(() => setToasts([]), []);

  return (
    <ToastContext.Provider value={{ toasts, dismiss, dismissAll }}>
      {children}
      <ToastViewport />
    </ToastContext.Provider>
  );
}

function ToastViewport() {
  const { toasts } = useToast();

  return (
    <div
      className="pointer-events-none fixed right-4 bottom-4 z-[100] flex w-full max-w-sm flex-col gap-2"
      role="region"
      aria-label="Notifications"
    >
      {toasts.map((t) => (
        <ToastItem key={t.id} toast={t} />
      ))}
    </div>
  );
}

const variantStyles: Record<ToastVariant, { border: string; icon: IconName; iconColor: string }> = {
  default: { border: 'border-gray-700', icon: 'info', iconColor: 'text-primary' },
  success: { border: 'border-green-800/50', icon: 'success', iconColor: 'text-green-500' },
  error: { border: 'border-red-800/50', icon: 'error', iconColor: 'text-red-500' },
  warning: { border: 'border-amber-800/50', icon: 'alert', iconColor: 'text-amber-500' },
};

function ToastItem({ toast: t }: { toast: Toast }) {
  const { dismiss } = useToast();
  const [isExiting, setIsExiting] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const handleDismiss = useCallback(() => {
    setIsExiting(true);
    setTimeout(() => dismiss(t.id), 150);
  }, [dismiss, t.id]);

  useEffect(() => {
    if (t.duration > 0) {
      timerRef.current = setTimeout(handleDismiss, t.duration);
    }
    return () => {
      if (timerRef.current) {
        clearTimeout(timerRef.current);
      }
    };
  }, [t.duration, handleDismiss]);

  const s = variantStyles[t.variant];

  return (
    <div
      className={`animate-in slide-in-from-right-full pointer-events-auto relative flex items-start gap-3 rounded-lg border bg-surface-dark p-4 pr-10 shadow-lg transition-all duration-150 ${s.border} ${
        isExiting ? 'translate-x-2 opacity-0' : 'translate-x-0 opacity-100'
      }`}
      role="alert"
    >
      <Icon name={s.icon} size={20} className={`mt-0.5 flex-shrink-0 ${s.iconColor}`} />
      <div className="min-w-0 flex-1">
        {t.title && <p className="mb-0.5 text-sm font-semibold text-white">{t.title}</p>}
        <p className="text-sm break-words text-gray-400">{t.description}</p>
      </div>
      <button
        onClick={handleDismiss}
        aria-label="Dismiss notification"
        className="absolute top-3 right-3 text-gray-400 transition-colors hover:text-white"
      >
        <Icon name="close" size={18} />
      </button>
    </div>
  );
}
