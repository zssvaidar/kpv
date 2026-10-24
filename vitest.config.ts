import { defineConfig } from 'vitest/config';

// One test runner for every TypeScript package in the workspace, configured once
// at the root so a developer and CI run the same thing.
export default defineConfig({
  test: {
    include: ['packages/*/src/**/*.test.ts'],
    passWithNoTests: false,
  },
  resolve: {
    alias: {
      '@kpv/schema': new URL('./packages/schema/src/index.ts', import.meta.url)
        .pathname,
    },
  },
});
