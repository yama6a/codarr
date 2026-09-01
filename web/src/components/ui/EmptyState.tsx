import { Icon, type IconName } from './Icon';

interface EmptyStateProps {
  message: string;
  icon?: IconName;
}

export function EmptyState({ message, icon = 'inbox' }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-12 text-gray-400">
      <Icon name={icon} size={40} className="text-gray-600" />
      <p className="text-sm">{message}</p>
    </div>
  );
}
