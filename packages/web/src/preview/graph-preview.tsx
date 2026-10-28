// A development page that draws a checked-in pipeline in the real shell, so
// the graph can be looked at in a browser before anything opens a pipeline
// from a project. `pnpm dev` serves it at /graph-preview.html?fixture=<name>,
// one of the names in graph/fixtures.ts; a build leaves it out, because only
// index.html is a build input. It talks to no agent, so it needs no token.

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import type { GraphFixtureName } from '../graph/fixtures.ts';
import { graphFixtures } from '../graph/fixtures.ts';
import { Shell } from '../shell/App.tsx';
import '../shell/shell.css';

const isFixtureName = (name: string): name is GraphFixtureName =>
  Object.hasOwn(graphFixtures, name);

const asked =
  new URL(window.location.href).searchParams.get('fixture') ??
  'every-block-type';
const name: GraphFixtureName = isFixtureName(asked)
  ? asked
  : 'every-block-type';

const root = document.getElementById('root');
if (root === null) {
  throw new Error('graph-preview.html has no #root element');
}
createRoot(root).render(
  <StrictMode>
    <Shell
      state={{ status: 'connected' }}
      client={null}
      pipeline={graphFixtures[name]}
    />
  </StrictMode>,
);
