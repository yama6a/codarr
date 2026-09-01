export interface SelectOption {
  value: string;
  label: string;
}

interface SelectProps {
  value: string;
  onChange: (value: string) => void;
  options: SelectOption[];
  ariaLabel: string;
  id?: string;
  className?: string;
}

export function Select({ value, onChange, options, ariaLabel, id, className = '' }: SelectProps) {
  return (
    <select
      id={id}
      aria-label={ariaLabel}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className={`rounded-lg border border-slate-700 bg-slate-900 px-3 py-2 text-sm text-white focus:border-primary focus:ring-1 focus:ring-primary focus:outline-none ${className}`}
    >
      {options.map((option) => (
        <option key={option.value} value={option.value}>
          {option.label}
        </option>
      ))}
    </select>
  );
}
