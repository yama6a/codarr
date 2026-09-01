import { Icon } from '../ui/Icon';
import { formatDateTime } from '../../lib/format';
import type { TestResult } from '../../api/types';

interface TestResultLineProps {
  result?: TestResult | null;
  lastTestedAt?: string | null;
  lastTestResult?: string;
}

export function TestResultLine({ result, lastTestedAt, lastTestResult }: TestResultLineProps) {
  if (result) {
    return (
      <p className={`flex items-center gap-2 text-xs ${result.ok ? 'text-green-400' : 'text-red-400'}`}>
        <Icon name={result.ok ? 'success' : 'error'} size={14} />
        {result.message}
        {result.server_name && ` (${result.server_name}${result.server_version ? ` ${result.server_version}` : ''})`}
      </p>
    );
  }
  if (!lastTestedAt) {
    return <p className="text-xs text-slate-500">Never tested.</p>;
  }
  return (
    <p className="text-xs text-slate-400">
      Last tested {formatDateTime(lastTestedAt)}: {lastTestResult || 'no detail recorded'}
    </p>
  );
}
