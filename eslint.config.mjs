// The single ESLint configuration for the whole workspace. Packages do not add
// their own; everything TypeScript is linted from here.
import js from '@eslint/js';
import tseslint from 'typescript-eslint';
import prettier from 'eslint-config-prettier';

// What @kpv/web's page code may never name. Storage, because the session token
// is held in memory only (#8): nothing that persists across a reload is
// reachable, so nothing can persist the token by accident.
const storage = [
  'localStorage',
  'sessionStorage',
  'indexedDB',
  'caches',
  'cookieStore',
];
// Network, because every call to the agent goes through the one typed client,
// which is what puts the token on every request (#8).
const network = ['fetch', 'WebSocket', 'XMLHttpRequest', 'EventSource'];

const restricted = (names, why) =>
  names.map((name) => ({ name, message: `${name}: ${why}` }));
const restrictedOn = (names, why) =>
  ['window', 'globalThis', 'self'].flatMap((object) =>
    names.map((property) => ({
      object,
      property,
      message: `${object}.${property}: ${why}`,
    })),
  );
const storageWhy =
  'the session token is held in memory only, so page code reaches no storage';
const networkWhy =
  'all agent calls go through the typed client in src/api/client.ts';

export default tseslint.config(
  {
    ignores: [
      '**/dist/**',
      '**/node_modules/**',
      'packages/schema/src/generated/**',
      // Local tool state, never repo content: Claude Code's settings and agent
      // worktrees (full copies of the repo), Serena's and RTK's project state.
      // Flat config does not read .gitignore, so they are named here (#72).
      '.claude/**',
      '.serena/**',
      '.rtk/**',
    ],
  },
  js.configs.recommended,
  tseslint.configs.recommendedTypeChecked,
  {
    languageOptions: {
      parserOptions: {
        project: [
          './tsconfig.json',
          './packages/web/tsconfig.json',
          './packages/web/tsconfig.node.json',
        ],
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  {
    files: ['packages/web/src/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-globals': [
        'error',
        ...restricted(storage, storageWhy),
        ...restricted(network, networkWhy),
      ],
      'no-restricted-properties': [
        'error',
        ...restrictedOn(storage, storageWhy),
        ...restrictedOn(network, networkWhy),
        { object: 'document', property: 'cookie', message: storageWhy },
      ],
    },
  },
  {
    // The client is the one place the network is named.
    files: ['packages/web/src/api/client.ts'],
    rules: {
      'no-restricted-globals': ['error', ...restricted(storage, storageWhy)],
      'no-restricted-properties': [
        'error',
        ...restrictedOn(storage, storageWhy),
        { object: 'document', property: 'cookie', message: storageWhy },
      ],
    },
  },
  {
    files: ['**/*.mjs'],
    extends: [tseslint.configs.disableTypeChecked],
  },
  prettier,
);
