import createClient, { type Middleware } from 'openapi-fetch';
import { toast } from '../components/ui/Toast';
import type { paths } from './schema';

// The Go binary serves the SPA and the API from one origin, so the base is relative. In dev, Vite
// proxies /api to the Go server (vite.config.ts).
const BASE_URL = '/';

async function describe(response: Response): Promise<string> {
  try {
    const body = (await response.clone().json()) as Record<string, unknown>;
    for (const key of ['message', 'detail', 'error']) {
      const value = body[key];
      if (typeof value === 'string' && value !== '') {
        return value;
      }
    }
  } catch {
    // Not JSON, or the body was already consumed. Fall through to the status line.
  }
  return `${response.status} ${response.statusText}`.trim();
}

// Failures raise a toast here so callers only handle the success path.
const reportFailures: Middleware = {
  async onResponse({ response }) {
    if (!response.ok) {
      toast.error(await describe(response));
    }
    return response;
  },
  onError({ error }) {
    toast.error(error instanceof Error ? error.message : String(error), 'Network error');
  },
};

export const api = createClient<paths>({ baseUrl: BASE_URL });
api.use(reportFailures);
