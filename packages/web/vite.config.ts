import { fileURLToPath } from 'node:url';

import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// The dev server's proxy to the agent is not configured here: the agent's
// port is different on every run, so it is added by dev/dev-server.ts once
// the agent has said where it is. `pnpm dev` is the way to run this.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      // The schema package's source, so neither the dev server nor a build
      // depends on @kpv/schema having been built first.
      '@kpv/schema': fileURLToPath(
        new URL('../schema/src/index.ts', import.meta.url),
      ),
    },
  },
  server: {
    host: '127.0.0.1',
    // The page is opened with the session token in its URL, and Vite puts
    // its own client script ahead of the referrer <meta> in index.html, so
    // the policy is a response header as well: no request the page makes
    // carries that URL as a Referer. The agent serving the page is #55.
    headers: { 'Referrer-Policy': 'no-referrer' },
  },
  build: {
    outDir: 'dist',
  },
});
