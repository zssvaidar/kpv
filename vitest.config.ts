import { defineConfig } from 'vitest/config';

// One test runner for every TypeScript package in the workspace, configured once
// at the root so a developer and CI run the same thing.
//
// @kpv/web's tests run here too, in Node rather than a browser: the code they
// test is handed its window, fetch, WebSocket and timers, and its components
// are rendered to a string. Its dev/ tests start a real agent and a real dev
// server, which is why the timeout is per test there rather than raised here.
export default defineConfig({
  test: {
    include: [
      'packages/*/src/**/*.test.{ts,tsx}',
      'packages/*/dev/**/*.test.ts',
    ],
    passWithNoTests: false,
  },
  resolve: {
    alias: {
      '@kpv/schema': new URL('./packages/schema/src/index.ts', import.meta.url)
        .pathname,
    },
  },
});
