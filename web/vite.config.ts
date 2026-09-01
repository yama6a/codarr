import { writeFileSync } from 'node:fs';
import { defineConfig, type Plugin } from 'vitest/config';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// emptyOutDir wipes the committed .gitkeep, and without it `//go:embed all:dist` fails to compile on
// a clean checkout.
const keepGitkeep = (): Plugin => ({
  name: 'codarr-keep-gitkeep',
  closeBundle() {
    writeFileSync('../internal/web/dist/.gitkeep', '');
  },
});

export default defineConfig({
  plugins: [react(), tailwindcss(), keepGitkeep()],
  build: {
    // go:embed needs the output inside the module, so build straight into it rather than copying.
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    port: 3000,
    host: '0.0.0.0',
    // The Go server owns /api in production too, so the app only ever uses relative URLs.
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: './test/setup.ts',
    include: ['**/*.{test,spec}.{ts,tsx}'],
  },
});
