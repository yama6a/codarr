interface TextInputProps {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  type?: 'text' | 'password' | 'number' | 'url';
  disabled?: boolean;
  id?: string;
  ariaLabel?: string;
  className?: string;
  onFocus?: () => void;
}

const base =
  'w-full rounded-lg border border-slate-700 bg-slate-900 px-3 py-2 text-sm text-white transition-colors placeholder:text-slate-500 focus:border-primary focus:ring-1 focus:ring-primary focus:outline-none disabled:opacity-50';

export function TextInput({
  value,
  onChange,
  placeholder,
  type = 'text',
  disabled = false,
  id,
  ariaLabel,
  className = '',
  onFocus,
}: TextInputProps) {
  return (
    <input
      id={id}
      type={type}
      value={value}
      disabled={disabled}
      placeholder={placeholder}
      aria-label={ariaLabel}
      onFocus={onFocus}
      onChange={(e) => onChange(e.target.value)}
      className={`${base} ${className}`}
    />
  );
}
