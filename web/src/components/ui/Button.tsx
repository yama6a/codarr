import React from 'react';
import { Icon, type IconName } from './Icon';

interface ButtonProps {
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger';
  icon?: IconName;
  loading?: boolean;
  disabled?: boolean;
  children: React.ReactNode;
  onClick?: () => void;
  type?: 'button' | 'submit' | 'reset';
  className?: string;
}

const variantClasses = {
  primary: 'bg-primary hover:bg-primary/90 text-white px-5 py-2.5 shadow-sm focus:ring-primary',
  secondary:
    'border border-gray-600 bg-gray-800 px-4 py-2 text-white hover:bg-gray-700 focus:ring-gray-500',
  ghost: 'text-gray-400 hover:text-white hover:bg-gray-800 px-3 py-2 focus:ring-gray-500',
  danger: 'bg-red-600 hover:bg-red-500 text-white px-5 py-2.5 shadow-sm focus:ring-red-500',
};

export function Button({
  variant = 'primary',
  icon,
  loading = false,
  disabled = false,
  children,
  onClick,
  type = 'button',
  className = '',
}: ButtonProps) {
  const base =
    'flex items-center justify-center gap-2 rounded-lg text-sm font-medium transition-all focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-offset-background-dark disabled:opacity-50 disabled:cursor-not-allowed';

  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled || loading}
      className={`${base} ${variantClasses[variant]} ${className}`}
    >
      {loading ? (
        <span className="size-4 animate-spin rounded-full border-2 border-current/30 border-t-current" />
      ) : (
        icon && <Icon name={icon} size={18} />
      )}
      {children}
    </button>
  );
}
