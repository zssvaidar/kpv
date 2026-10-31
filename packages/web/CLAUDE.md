# @kpv/web

Vite + React in strict TypeScript: the shell, the graph editor, the side
panel, the directory picker, and the one client for the agent. Tests run in
Node through the root `vitest.config.ts`: components are rendered to a string
and code is handed its window, fetch and timers, so there is no DOM. There are
two `tsc` projects: `src/` (`tsconfig.json`) and `dev/` plus `vite.config.ts`
(`tsconfig.node.json`).

Rules a change can break

- `src/api/client.ts` is the only place the page reaches the agent. ESLint
  (`eslint.config.mjs`) refuses `fetch`, `WebSocket`, `XMLHttpRequest` and
  `EventSource` anywhere else in `src/`. The token is handed to
  `createAgentClient` once and sent as `Authorization: Bearer` on every
  request; no method takes headers or a URL. Answers are parsed with the
  generated Zod schemas from `@kpv/schema`, never hand-written types
- the agent answers every failed check with one uniform `404`
  (`agent/internal/httpapi/CLAUDE.md`), so the client reads a `404` that is not
  the endpoint's typed error as a rejection, shown as an authentication
  problem, and no response at all as an absent agent. A route the agent does
  not serve also reads as a rejection
- the token is held in memory only: `src/session/token.ts` removes it from the
  address bar and history with `history.replaceState` before anything renders,
  and ESLint refuses every storage API and `document.cookie` in `src/`. A
  reload therefore shows the authentication state; reopen the URL `pnpm dev`
  printed
- the page sends no `Referer`: a `<meta>` in `index.html`, and in development
  a `Referrer-Policy` header in `vite.config.ts`, because Vite's client script
  loads ahead of the `<meta>`
- whether a pipeline is valid is decided in one place: `schemaIssues` in
  `src/graph/edit.ts`, the generated Zod plus the schema's annotation rules.
  Every change to the pipeline is a pure transform in that file that returns
  an `EditResult` through `commit` and never changes the pipeline it was
  given. The panel forms (`src/panel/*-form.ts`) check as you type with the
  same `schemaIssues` and save through `updateKafkaBlock` and `updateStep`
  there (`edit.test.ts`, `kafka-form.test.ts`, `step-form.test.ts`)
- `src/connection/monitor.ts` holds the connection state the status bar shows.
  The channel WebSocket is the liveness signal; on a drop one request says why,
  and it reconnects with backoff (`backoff.ts`: 250ms doubling to 10s) unless
  it was a rejection

Development (`pnpm dev`, `dev/`)

- `pnpm dev` (`dev/dev.ts`) builds the agent from this checkout, reads its
  startup lines for the port and token (`dev/agent-process.ts`), starts Vite
  (5173 or the next free port) and prints `open http://127.0.0.1:<port>/?token=...`.
  Every run is a new agent, port and token; nothing is configured
- `/api` is proxied by `dev/agent-proxy.ts`, not Vite's `server.proxy`, which
  answers `502` for a dead agent and logs URLs that carry the token. The proxy
  presents the agent's origin for a request that is same-origin to the dev
  server, passes anything else through for the agent to reject, and never
  touches the token
- `server.cors` and `preview.cors` are off (#65), held by `dev/cors.test.ts`
- `dev/dev.test.ts` runs all of this against a real agent, twice in a row
- `/graph-preview.html?fixture=<name>` and `/blocks-preview.html` draw
  checked-in fixtures in the dev server with no agent (`src/preview/`); a
  build leaves them out
