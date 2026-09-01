import { Icon } from './Icon';

interface PaginationProps {
  total: number;
  limit: number;
  offset: number;
  onPageChange: (offset: number) => void;
}

const btnBase = 'flex h-8 min-w-8 items-center justify-center rounded-lg px-2 text-sm font-medium transition-colors';
const btnEnabled = 'border border-slate-700 text-slate-300 hover:bg-slate-800';
const btnDisabled = 'border border-slate-800 text-slate-600 cursor-not-allowed';
const btnActive = 'border border-primary bg-primary text-white';

function pageNumbers(currentPage: number, totalPages: number): number[] {
  const maxVisible = 5;
  const end = Math.min(totalPages, Math.max(1, currentPage - Math.floor(maxVisible / 2)) + maxVisible - 1);
  const start = Math.max(1, end - maxVisible + 1);
  return Array.from({ length: end - start + 1 }, (_, i) => start + i);
}

export function Pagination({ total, limit, offset, onPageChange }: PaginationProps) {
  if (total <= limit) {
    return null;
  }

  const currentPage = Math.floor(offset / limit) + 1;
  const totalPages = Math.ceil(total / limit);

  return (
    <div className="flex items-center justify-between pt-4">
      <span className="text-xs text-slate-400">
        Showing {offset + 1}-{Math.min(offset + limit, total)} of {total}
      </span>
      <div className="flex items-center gap-1">
        <button
          onClick={() => onPageChange(offset - limit)}
          disabled={currentPage === 1}
          aria-label="Previous page"
          className={`${btnBase} ${currentPage === 1 ? btnDisabled : btnEnabled}`}
        >
          <Icon name="chevron_left" size={18} />
        </button>

        {pageNumbers(currentPage, totalPages).map((page) => (
          <button
            key={page}
            onClick={() => onPageChange((page - 1) * limit)}
            aria-current={page === currentPage ? 'page' : undefined}
            className={`${btnBase} ${page === currentPage ? btnActive : btnEnabled}`}
          >
            {page}
          </button>
        ))}

        <button
          onClick={() => onPageChange(offset + limit)}
          disabled={currentPage === totalPages}
          aria-label="Next page"
          className={`${btnBase} ${currentPage === totalPages ? btnDisabled : btnEnabled}`}
        >
          <Icon name="chevron_right" size={18} />
        </button>
      </div>
    </div>
  );
}
