import { Button } from '../ui/Button';

interface LoadMoreProps {
  shown: number;
  total: number;
  loading: boolean;
  onLoadMore: () => void;
}

/** LoadMore is the footer under a capped list: how much is shown and a button for the next page. */
export function LoadMore({ shown, total, loading, onLoadMore }: LoadMoreProps) {
  if (shown >= total) {
    return null;
  }
  return (
    <div className="flex items-center justify-between pt-3 text-xs text-slate-500">
      <span>
        Showing {shown.toLocaleString()} of {total.toLocaleString()}
      </span>
      <Button variant="ghost" icon="chevron_down" loading={loading} onClick={onLoadMore}>
        Load more
      </Button>
    </div>
  );
}
