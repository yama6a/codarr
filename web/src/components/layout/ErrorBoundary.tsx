import { Component, type ErrorInfo, type ReactNode } from 'react';
import { Icon } from '../ui/Icon';

interface Props {
  children: ReactNode;
  fallback?: ReactNode;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

export default class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = { hasError: false, error: null };
  }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error('ErrorBoundary caught an error:', error, errorInfo);

    // A lazy chunk that 404s means the tab is running an older build than the server now serves.
    if (
      error.message.includes('Failed to fetch dynamically imported module') ||
      error.message.includes('Loading chunk') ||
      error.message.includes('Loading CSS chunk')
    ) {
      window.location.reload();
    }
  }

  handleReset = () => {
    this.setState({ hasError: false, error: null });
  };

  render() {
    if (!this.state.hasError) {
      return this.props.children;
    }
    if (this.props.fallback) {
      return this.props.fallback;
    }

    return (
      <div className="flex h-full items-center justify-center p-8">
        <div className="w-full max-w-md text-center">
          <Icon name="error" size={56} className="mb-6 text-red-500" />
          <h1 className="mb-2 text-2xl font-bold text-white">Something went wrong</h1>
          <p className="mb-6 text-gray-400">
            An unexpected error occurred. Try again, or reload the page.
          </p>
          {this.state.error && (
            <details className="mb-6 rounded-lg bg-gray-800 p-4 text-left">
              <summary className="cursor-pointer text-sm font-medium text-gray-300">Error details</summary>
              <pre className="mt-2 overflow-auto text-xs text-red-400">{this.state.error.message}</pre>
            </details>
          )}
          <div className="flex justify-center gap-3">
            <button
              onClick={this.handleReset}
              className="rounded-lg bg-primary px-4 py-2 font-medium text-white transition-colors hover:bg-primary/90"
            >
              Try again
            </button>
            <button
              onClick={() => window.location.reload()}
              className="rounded-lg bg-gray-700 px-4 py-2 font-medium text-gray-300 transition-colors hover:bg-gray-600"
            >
              Reload
            </button>
          </div>
        </div>
      </div>
    );
  }
}
