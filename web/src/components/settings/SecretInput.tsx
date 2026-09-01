import { useState } from 'react';
import { Button } from '../ui/Button';
import { TextInput } from '../ui/TextInput';
import { SECRET_MASK } from '../../api/types';

interface SecretInputProps {
  value: string;
  onChange: (value: string) => void;
  label: string;
  placeholder?: string;
}

// plan.md 18.4: GET returns the mask, and sending the mask back leaves the stored value alone, so
// the field only ever holds characters the user typed.
export function SecretInput({ value, onChange, label, placeholder }: SecretInputProps) {
  const [editing, setEditing] = useState(value !== SECRET_MASK);

  if (!editing) {
    return (
      <div className="flex items-center gap-3">
        <div className="flex-1 rounded-lg border border-slate-700 bg-slate-900/60 px-3 py-2 font-mono text-sm text-slate-500">
          {SECRET_MASK}
        </div>
        <Button
          variant="secondary"
          icon="key"
          onClick={() => {
            setEditing(true);
            onChange('');
          }}
        >
          Change
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <TextInput
        type="password"
        value={value === SECRET_MASK ? '' : value}
        onChange={onChange}
        ariaLabel={label}
        placeholder={placeholder ?? 'Paste the new value'}
      />
      <button
        onClick={() => {
          onChange(SECRET_MASK);
          setEditing(false);
        }}
        className="text-xs text-blue-400 hover:underline"
      >
        Keep the stored value
      </button>
    </div>
  );
}
