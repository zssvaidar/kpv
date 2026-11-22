# @kpv/web

Vite + React in strict TypeScript: the shell, the pipeline list, the graph
editor, the side panel, the directory picker, the Kafka connection screen,
and the one client for the agent. Tests run in Node through the root
`vitest.config.ts`: components are rendered to a string and code is handed
its window, fetch and timers, so there is no DOM. There are two `tsc` projects: `src/` (`tsconfig.json`) and `dev/`
plus `vite.config.ts` (`tsconfig.node.json`).

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
- the pipeline list (`src/pipelines/`, #48) is the graph area's entry screen.
  It opens an entry by the root and directory the agent listed, never a path
  it builds, and draws the answer only after `schemaIssues` accepts it: a
  document that fails is shown unreadable with its problems, never drawn, and
  there is no form-level fallback. The create form checks every field with
  `schemaIssues` too (`create-form.ts`) and preselects nothing the schema
  gives no default. Words for every kind are in `pipelines/words.ts`; an
  unavailable root uses the picker's `unavailableWords`
- the Kafka connection screen (`src/connections/`, #24; not `src/connection/`,
  which is the agent connection's state) is its own screen in the graph area,
  reached from the status bar, never the side panel. Its form checks with
  `connectionIssues` and asks `protocolAllowsSasl`/`protocolAllowsTls` from
  `@kpv/schema` which fields to show (`form.ts`), restating no rule. The
  password and client key are never prefilled, show only set or not set, use
  `autocomplete="new-password"`, and are cleared from the form the moment a
  save is sent (`controller.ts`); a kept one is left out of the update, a
  removed one listed in `remove`. A referenced delete or rename shows the
  agent's report and sends `confirm` only when the person asks.
  `client.ts`'s `listConnectionNames` is what #12's select is filled from
- the Kafka form names a connection only by a `<select>` of the names
  `panel/connection-names.ts` read last, re-read whenever a Kafka form is
  shown and after the connection screen's list changes; never free text. A
  name the block holds that the list lacks is offered as it is, marked
  dangling (or unchecked before any read), and kept on save: a reference
  matches exactly, letter case included (`connectionChoices` in
  `kafka-form.ts`). No connection leaves the key out
- `client.ts`'s `readSource` and `writeSource` (#87) read and write a
  pipeline's source file by the listed root and directory and a path inside
  the pipeline directory, for #14's editor. `SourceWrite` holds by its type
  that a write has exactly one of `expectedSha256` (the hash the read
  answered) and `expectedAbsent`; a `changed` error carries the current hash
- a custom-code node shows its step's `customCode.path` in a CodeMirror 6
  editor (#14; only a `customCode` step, highlighting by the pipeline's
  `language`). The node is controlled (`blocks/BlockNode.tsx`,
  `blocks/CodeEditor.tsx`): the shell owns one buffer per path
  (`shell/sources.ts`), reads each path once when an opened pipeline is drawn
  and again only on Reload, and writes only on Save with the hash read. The
  model is pure and tested without a DOM (`blocks/source-buffer.ts`): unsaved
  is judged by what a save would write (a lone surrogate counts as U+FFFD, as
  the agent's decoder writes it), and `textToWrite` keeps each unchanged
  line's own ending, so CRLF and a missing final newline survive. The editor
  hangs below the card, outside the node box `geometry.ts` gives; expanded it
  is 720px wide and its editor 480px tall, over the canvas. Its host carries
  `nodrag nopan nowheel nokey` and stops its keydowns, so nothing typed in it
  reaches the graph. Arrow up/down and Page Up/Down are `lineMotion`, not
  CodeMirror's: zoomed out a line is under 2 screen pixels and CodeMirror
  6.43's `moveVertically` loops forever (`textHeight >> 1` is 0). Deleting the
  last step naming unsaved code, going back to the list and Reload ask first
  (`confirm`); an opened pipeline stays drawn while disconnected, Save
  disabled with the connection state as the reason
- the staged-changes review (`src/review/`, #43): only edits the tool makes
  are staged (in the agent; the page never stages), the person's own Save
  stays a direct write. An opened pipeline with staged files shows how many
  and a "Review staged changes" control (`App.tsx`), and a custom-code node or
  a staged `pipeline.yaml` says `codeWords.staged` ("This file has staged
  changes waiting for review") with the same control; the editor keeps
  showing the disk. `review/controller.ts` owns every request (through
  `client.ts`'s `reviewWorkcopy`, `applyWorkcopy`, `restoreSnapshot`,
  `discardWorkcopy`, `removeSnapshot`, `listWorkcopies`), sends apply only
  when the person activates it with the revision drawn, and reads the review
  again after every action (a `changed` answer means it was out of date).
  The diff model (`diff.ts`: lines with their own ending, so a CRLF or
  final-newline change shows changed lines named CRLF, LF or no final
  newline), the enabled controls (`controls.ts`; disabled while disconnected
  with the connection state as the reason) and the panel rendered to a
  string are tested without a DOM (`review.test.tsx`). After an apply or
  restore the shell re-reads every buffer it holds for a written path, asking
  first where one holds typed code. Discard and removing a snapshot ask
  first. How QA stages changes for a check by hand is in
  `agent/internal/workcopy/CLAUDE.md`
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
- tests have no DOM, so a check in a real browser is by hand: headless
  Google Chrome is at `/usr/bin/google-chrome`, driven over CDP by
  `playwright-core` or `puppeteer-core` installed in a scratch directory. No
  browser driver is a dependency of the workspace (`package.json`,
  `pnpm-lock.yaml`), and adding one is not part of a check. Where `pnpm dev`
  keeps its store is in `agent/CLAUDE.md` (Startup lines)
- 50 custom-code editors, measured by hand (#14): a pipeline of 51
  custom-code steps (50 with an editor: two sharing a file, one generated,
  one missing, one CRLF, the rest 52-line Python files with one line 344
  characters long; one with no reference) opened through `pnpm dev` with `HOME` and
  `XDG_CONFIG_HOME` scratch, in headless Chrome 1600x1000 driven by
  `puppeteer-core`. Keystroke to paint is the Event Timing API's `duration`
  of each `keydown` (to the next frame presented), checked against a
  `requestAnimationFrame` then `setTimeout` stamp, over 80 keys 60ms apart in
  one editor. Fitted to the view (zoom 0.087, all 50 editors on screen) the
  median was 40-112ms and the 95th percentile 64-144ms across runs on a host
  under load average 5-11; zoomed to 0.26 (26 on screen) the median was
  24-72ms. Script per keydown stayed under 1ms (median 0.3ms); the rest is
  style, layout and software paint. Typing in a plain text input on the same
  page measured a median of 75ms in the same conditions. Panning by a mouse
  drag of the pane over 60 moves gave frames of median 16.7ms, 95th
  percentile 33ms. This is the dev server's development React build
