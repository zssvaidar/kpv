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
    // No CORS from the dev server, for anything (#65). Vite's CORS middleware
    // allows localhost and 127.0.0.1 origins by default and is installed
    // ahead of every plugin's middleware, so left on it answered a preflight
    // for `/api` with 204 and an allow header before the agent proxy
    // (dev/agent-proxy.ts) saw it, and added Access-Control-Allow-Origin to
    // the agent's own rejections. Dev traffic is one origin (#8): the page and
    // `/api` both come from here, so nothing the page needs is cross-origin.
    // The HMR client connects from the same origin too, and nothing else in
    // this setup needs CORS. dev/cors.test.ts fails if this is turned back on.
    cors: false,
  },
  // `vite preview` has its own switch. No script runs it today; if one is
  // added it answers no CORS either, and has to meet #65's criteria the same
  // way.
  preview: {
    cors: false,
  },
  build: {
    outDir: 'dist',
  },
});
