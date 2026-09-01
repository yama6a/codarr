import { Icon } from './Icon';

interface SearchInputProps {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
}

export function SearchInput({ value, onChange, placeholder = 'Search...' }: SearchInputProps) {
  return (
    <div className="relative">
      <Icon
        name="search"
        size={18}
        className="pointer-events-none absolute top-1/2 left-3.5 -translate-y-1/2 text-gray-500"
      />
      <input
        type="search"
        className="w-full rounded-lg border border-gray-700 bg-gray-800 py-2.5 pr-4 pl-10 text-sm text-white transition-all placeholder:text-gray-500 focus:border-primary focus:ring-1 focus:ring-primary focus:outline-none"
        placeholder={placeholder}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  );
}
