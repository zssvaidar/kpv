# Kafka Pipeline Visualizer — Task Backlog (archived)

Superseded. The backlog lives as GitHub issues in
https://github.com/zssvaidar/kpv/issues — issues #1-#47 were generated from
this file, one per task, and are the source of truth. This copy is kept for
reference; editing it changes nothing.

Each task is groomed into the shape described in `_docs/task-template.md` on
the issue itself, before anyone implements it. The two-field Goal/Description
form below predates that template. All 47 have since been groomed on the
issues themselves, and follow-up issues #48-#55 were filed for work moved out
of scope during grooming.

Derived from `_docs/outdated/plan.md`.

**Stack assumption:** the agent is Go, as `_docs/outdated/plan.md` section 4.2
requires. The repository is a polyglot monorepo:

- `schema/` — the authoritative, language-neutral `pipeline.schema.json`, with
  Go structs and a TypeScript Zod package (`@kpv/schema`) generated from it, so
  the agent and the browser cannot drift apart. The JSON Schema is the single
  source of truth; both outputs are generated and neither is hand-edited.
- `agent/` — the Go local agent (`net/http` on loopback with token auth,
  WebSocket, path guard, Docker Compose lifecycle, codegen, parser harness),
  built as one module and shipped as a single static binary with the web assets
  embedded. A pure-Go Kafka client is preferred so `CGO_ENABLED=0` builds and
  cross-compilation keep working.
- `@kpv/web` — React + Vite + `@xyflow/react`, Monaco for full-file editing and
  CodeMirror 6 embedded in custom-code blocks.
- parser sidecars for Python (`ast`) and TypeScript (`ts-morph`), invoked as
  subprocesses, so import of those languages needs `python3` and `node` on the
  host. Go source is parsed in-process by the agent with `go/ast` behind the
  same contract, with no sidecar.

The task boundaries below still hold. The package names, library choices, and
schema strategy in the descriptions below do not: where a description says
`@kpv/agent`, a Fastify server, or a Zod-first schema, the groomed issue is
correct and this archived file is not.

**How to read this:** tasks are listed in a sensible build order, but each one
is written to stand alone. Descriptions name the packages and artifacts a task
touches rather than referring to other task numbers, so any single task can be
handed over without reading the rest. Where a task needs something that does
not exist yet, it is expected to stub that thing behind a narrow interface.

---

## 1. Set up the empty project with a passing test
Goal: A clean monorepo that installs, type-checks, lints, and runs one green test.
Description: Create a pnpm workspace with TypeScript in strict mode, Vitest, ESLint, and Prettier configured once at the root and inherited by packages. Add an empty `@kpv/schema` package containing a single trivial test so `pnpm test` passes from a fresh clone. Add root scripts for `test`, `typecheck`, `lint`, and `build`, and a CI workflow that runs them.

## 2. Define the pipeline model schema
Goal: One authoritative, validated definition of a pipeline.
Description: In `@kpv/schema`, define Zod schemas for `Pipeline` (id, language, source, processingSteps, destination, runtimeConfiguration) and for each block type listed in the plan's block table. Every processing step carries a stable identifier, a type discriminator, its configuration, and an optional custom-code reference. Export inferred TypeScript types alongside the schemas and cover valid and invalid fixtures with tests.

## 3. Read and write pipeline.yaml
Goal: Load and save a pipeline definition as YAML without losing fidelity.
Description: Add a serializer and parser that convert between the pipeline model and a `pipeline.yaml` file, validating on read and producing deterministic key ordering on write. Invalid or unknown fields must produce structured errors that name the offending path, not a thrown stack trace. Test round-tripping a representative pipeline and the error shape for several malformed files.

## 4. Agent HTTP server with loopback binding and token auth
Goal: A local API that only the intended browser session can reach.
Description: Stand up the `@kpv/agent` Fastify server bound to `127.0.0.1` on an ephemeral port, generating a random session token at startup and printing the URL that carries it. Require the token on every request and validate the `Origin` header against an allowlist, rejecting everything else with no detail in the body. Include tests for missing token, wrong token, and disallowed origin.

## 5. Agent WebSocket channel
Goal: A authenticated push channel for logs and status.
Description: Add a WebSocket endpoint to the agent that performs the same token and origin validation as the HTTP API before the connection is accepted. Define a typed message envelope (kind, pipeline id, payload) in `@kpv/schema` and implement heartbeat plus clean teardown on client disconnect. Test that unauthenticated and cross-origin upgrade attempts are refused.

## 6. Project path allowlist and traversal guard
Goal: File operations cannot escape an authorized project directory.
Description: Implement a small module in `@kpv/agent` that holds the set of user-authorized project roots and resolves every incoming path against them, rejecting symlinks and `..` segments that leave the root. All later filesystem code is expected to route through this module rather than touching `fs` directly. Unit-test traversal, symlink escape, absolute-path, and case-collision attempts.

## 7. Directory browsing API
Goal: The user can pick a project directory from the UI.
Description: Expose agent endpoints that list subdirectories and detect whether a given directory looks like a project, so the browser can offer a directory picker without a native dialog. Results are limited to authorized roots plus the user's home directory, and entries the process cannot read are reported rather than silently dropped. Include tests for permission errors and for very large directories.

## 8. Web application shell and API client
Goal: A running frontend that can talk to the agent.
Description: Create the `@kpv/web` Vite + React app with a layout shell (graph area, side panel, bottom panel) and a typed API client that injects the session token on every request and reconnects the WebSocket with backoff. Surface agent connection state in the UI so a dead or unreachable agent is obvious. No pipeline features yet — a stub screen that reads one endpoint is enough.

## 9. Render a linear pipeline graph
Goal: Display a pipeline as a left-to-right graph.
Description: Using `@xyflow/react`, render a pipeline model from a checked-in fixture as an ordered chain of nodes with edges, laid out automatically so the author does not position nodes by hand. Include pan, zoom, fit-to-view, and selection. This task is read-only rendering; editing is handled separately.

## 10. Block node components
Goal: Each block type is visually distinct and legible.
Description: Build the presentational React components for Kafka Source, Filter, Transform, Validate, Custom Code, and Kafka Destination nodes, each showing its name, a type affordance, and a slot for a status badge. Include a variant for the opaque "unsupported code" block so imported pipelines have something to render. Cover them with a component story or snapshot test per type in light and dark themes.

## 11. Graph editing operations
Goal: Insert, reorder, and delete blocks in the graph.
Description: Implement the editing layer over the graph: insert a block between two existing blocks, drag to reorder processing steps, and delete a step, each producing a new validated pipeline model rather than mutating nodes in place. Source and destination cannot be deleted or reordered out of their terminal positions. Test each operation as a pure model transform, separately from the UI.

## 12. Kafka source and destination configuration panel
Goal: Configure input and output topics and client behavior.
Description: Build the side-panel form for source and destination blocks covering topic, consumer group, offset reset behavior, and producer acknowledgement settings, validated against the schema as the user types. Field-level errors appear inline and block saving without discarding what was typed. Kafka connection credentials are referenced by name here, not entered inline.

## 13. Filter, transform, and validate configuration panels
Goal: Configure the declarative processing blocks without writing code.
Description: Build forms for the three declarative block types — a filter condition, a field mapping or payload transform, and a validation rule set with a configurable action for invalid messages. Each form reads and writes the block configuration defined in `@kpv/schema` and shows a plain-language summary of the configured behavior. Include tests for the summary rendering of several configurations.

## 14. Embedded code editor in custom-code blocks
Goal: Edit a block's processing logic inside the graph.
Description: Embed a lightweight editor (CodeMirror 6) in the Custom Code node with syntax highlighting for Python, TypeScript, and Go, sized to the block and expandable. The editor holds unsaved state locally and reports dirtiness upward, but does not itself write files. Verify that many editors mounted at once stay responsive.

## 15. Full-file code editor view
Goal: Edit a pipeline's source files directly.
Description: Add a full-file editing view backed by Monaco, with a file tree scoped to the project, tab management, dirty indicators, and explicit save through the agent. Files the tool generated are marked read-only or warn before edit so regeneration does not destroy hand edits. Include a test that saving writes through the path guard and refuses paths outside the project.

## 16. Code generation framework and file ownership boundary
Goal: A predictable way to emit project files without overwriting user code.
Description: Define the codegen interface (pipeline model in, set of files out) plus the rule that generated output lands only under `generated/` while user-owned code lives under `src/`, with a manifest recording what was generated and its hash. Regeneration must refuse to clobber a generated file the user has since modified, reporting it instead. Implement the framework and manifest with a fake single-file generator; real language generators are separate tasks.

## 17. Python runtime template and generator
Goal: Generate a runnable Python pipeline project.
Description: Write the Python runtime scaffolding — consumer loop, stage dispatch, key and header preservation, structured startup logging, configuration from environment variables — and the generator that fills it from a pipeline model. The generated project must run against a real broker and must not hardcode credentials. Include a test that generates a project from a fixture and asserts the file set and key contents.

## 18. TypeScript runtime template and generator
Goal: Generate a runnable Node/TypeScript pipeline project.
Description: Write the Node runtime scaffolding — consumer loop, stage dispatch, key and header preservation, startup logging, environment-based configuration — and the generator that fills it from a pipeline model. Dependencies are declared in a generated manifest but never installed by the tool. Include a generation test asserting the emitted file set and key contents.

## 19. Go runtime template and generator
Goal: Generate a runnable Go pipeline project.
Description: Write the Go runtime scaffolding — consumer loop, stage dispatch, key and header preservation, startup logging, environment-based configuration — and the generator that fills it from a pipeline model. The emitted module must compile with `go build` using only declared dependencies. Include a generation test asserting the emitted file set and key contents.

## 20. Minimal runtime container images
Goal: One small, reproducible base image per supported language.
Description: Author Dockerfiles for Python, Node, and Go pipeline runtimes using pinned base images, non-root users, and multi-stage builds where it reduces size. Each image takes the pipeline source as a build context and declares no credentials. Document the build command and record resulting image sizes.

## 21. Compose file generation
Goal: Each pipeline gets its own container definition.
Description: Generate a `compose.yaml` per pipeline with one service, an env-file reference, a bridge network by default, and no published ports unless configured. Service and network names derive deterministically from the pipeline id so repeated generation is stable. Test the emitted YAML for two pipelines and confirm they do not collide.

## 22. Dependency manifest detection
Goal: Report what an existing project depends on without changing it.
Description: Detect and parse `requirements.txt`/`pyproject.toml`, `package.json`, and `go.mod` in a project directory, reporting the declared dependencies and whether a recognized Kafka client is among them. The tool never installs or edits these files; missing or unparseable manifests are reported as warnings with the file path. Cover each language plus the ambiguous case of several manifests in one tree.

## 23. Secrets, .env, and gitignore generation
Goal: Credentials stay out of source control and out of generated code.
Description: Implement `.env` reading and writing for runtime configuration, generate a `.gitignore` that excludes env files by default, and inject values into containers at run time rather than baking them into images or generated source. Add a scan that warns when something credential-shaped appears in a tracked file. Test redaction so secret values never reach API responses or agent logs.

## 24. Kafka connection configuration
Goal: Define and store a reusable connection to an external cluster.
Description: Model named Kafka connections (bootstrap servers, security protocol, SASL mechanism and credentials, TLS settings) in `@kpv/schema`, persist them outside the pipeline file, and build the UI form for creating and editing them. Credential fields are write-only from the browser's perspective — never echoed back after saving. Include validation tests for each supported protocol and mechanism combination.

## 25. Containerized Kafka connectivity probe
Goal: Verify a connection the same way the pipeline will use it.
Description: Implement a connectivity check that runs in a short-lived container using the same Kafka client as the target runtime, reporting broker reachability, authentication result, and topic metadata. Running it in a container makes advertised-listener and DNS problems visible instead of hidden behind a host-only check. Map common failures (TLS, SASL, unresolvable advertised host, timeout) to actionable messages and test that mapping against recorded failures.

## 26. Sample live messages from a topic
Goal: Pull a bounded set of real messages for inspection.
Description: Add an agent operation that consumes a configurable number of messages from a topic without committing offsets or joining the pipeline's consumer group, returning payload, key, headers, timestamp, partition, and offset. Large payloads are truncated with the truncation marked explicitly in the response. Cover empty topics, timeouts, and a payload above the size limit.

## 27. JSON field inference and suggestions
Goal: Suggest message fields in the configuration forms.
Description: Infer a field list with observed types from a set of sampled JSON messages, merging across messages and marking fields that appear in only some of them as optional. Expose the result as dropdown suggestions in the filter and transform forms, explicitly labeled as suggestions rather than a schema. Test inference over heterogeneous, nested, and non-JSON samples.

## 28. Manual message input
Goal: Write test messages by hand.
Description: Build the editor for composing a test message — payload as JSON or plain text, optional key, optional headers — with JSON validity feedback and the ability to save a named set of messages with the pipeline. Pasted sampled messages can be edited in place as a starting point. Include tests for malformed JSON and for plain-text payloads that merely look like JSON.

## 29. Local replay engine
Goal: Run messages through the processing stages without touching production.
Description: Execute a pipeline's processing steps against a list of input messages in an isolated local runtime, capturing each stage's input and output plus any error, and never producing to the configured destination topic. The response records where the local runtime differs from the deployed one. Test a successful chain, a filtered-out message, a validation failure, and a stage that throws.

## 30. Stage input and output inspector
Goal: See what each stage did to a message.
Description: Build the UI that walks a replay result stage by stage, showing input and output payloads side by side with the changed fields highlighted, and surfacing errors at the stage where they occurred. Filtered and dropped messages are shown as such rather than disappearing from the view. Include rendering tests for large payloads and for error states.

## 31. Message metadata inspection
Goal: Expose Kafka metadata clearly and read-only.
Description: Display key, headers, timestamp, partition, and offset for sampled messages, with partition and offset visually distinct from payload fields so they are not mistaken for editable data. Headers render as a list that tolerates binary values. Include a test that this view never offers an edit affordance for metadata the tool cannot change.

## 32. Deploy a pipeline
Goal: Build and start a pipeline in its own container.
Description: Implement the agent deploy operation — generate files, build the image, bring the single-service Compose project up, and report build output and failures with the originating step identified. Progress streams over the WebSocket so the UI can show a live build log. Test the happy path plus a deliberate build failure and a missing-Docker environment.

## 33. Stop and recreate a pipeline
Goal: Apply changes to a running pipeline.
Description: Implement stop, and an update that stops and recreates the container, reporting the expected downtime to the user before it happens. The implementation documents the consequences for in-flight messages, offset commits, and possible duplicate processing, and claims nothing about exactly-once delivery. Test stopping an already-stopped pipeline and recreating one whose image no longer builds.

## 34. Container log streaming
Goal: Watch a pipeline's output live.
Description: Stream container stdout and stderr through the agent to the UI over the WebSocket with a bounded in-memory buffer, backfilling recent lines when a viewer attaches. The viewer offers text search and severity filtering where severity can be determined from the line. Test reconnection mid-stream and a container producing output faster than the client consumes it.

## 35. Runtime status model and graph badges
Goal: See at a glance whether a pipeline is running.
Description: Define running, stopped, and failed states in `@kpv/schema`, derive them in the agent from container state, push changes over the WebSocket, and render them as badges on the graph and in the pipeline list. Unknown or transitional states render as unknown rather than guessing. Test the derivation from several container states including exited-nonzero and restarting.

## 36. Pre-deploy validation
Goal: Catch obvious problems before building anything.
Description: Run a check before deployment that validates the pipeline against the schema, confirms required configuration and credential references resolve, and syntax-checks custom code for the target language. Results are a list of errors and warnings with block ids, where errors block deployment and warnings do not. Test each failure category against fixtures.

## 37. Parser sidecar contract and harness
Goal: One language-agnostic format for parsed pipeline fragments.
Description: Define the JSON contract a per-language parser returns — recognized stages with types, configuration, source byte ranges, plus unrecognized regions and warnings — and build the harness in `@kpv/agent` that invokes a sidecar, enforces a timeout, and validates its output. Ship a fake sidecar so the harness is testable without any real parser. Test malformed sidecar output, a crash, and a timeout.

## 38. Python parser sidecar
Goal: Recognize supported Kafka pipeline patterns in Python source.
Description: Build a sidecar that parses Python with the standard `ast` module and emits the parser contract, recognizing a documented, deliberately narrow set of consumer-loop and stage patterns. Anything outside that set is reported as an unrecognized region with its byte range, never guessed at. Include a fixture suite of recognized files, near-miss files that must not match, and the expected warnings.

## 39. TypeScript parser sidecar
Goal: Recognize supported Kafka pipeline patterns in TypeScript source.
Description: Build a sidecar using `ts-morph` or the TypeScript compiler API that emits the parser contract for a documented set of consumer and stage patterns, resolving imports and aliases where it can. Unrecognized code is returned as opaque regions with byte ranges. Include recognized, near-miss, and warning fixtures as above.

## 40. Go parser sidecar
Goal: Recognize supported Kafka pipeline patterns in Go source.
Description: Build a sidecar using `go/ast` that emits the parser contract for a documented set of consumer-loop and stage patterns across the common Go Kafka clients. Unrecognized code is returned as opaque regions with byte ranges. Include recognized, near-miss, and warning fixtures as above.

## 41. Language and entry-point detection
Goal: Work out what an imported project is before parsing it.
Description: Given a project directory, determine the primary language from manifests and file distribution, then rank likely pipeline entry-point files using documented heuristics, returning candidates with the reason each was chosen. Ambiguous projects return several candidates for the user to choose between rather than picking silently. Test single-language, polyglot, and no-obvious-entry-point trees.

## 42. Map parsed fragments to graph blocks
Goal: Turn a parse result into a renderable pipeline.
Description: Convert the parser contract output into a pipeline model, mapping recognized stages to their block types and wrapping each unrecognized region in an opaque block that retains its original source text verbatim. Parsing warnings travel with the model so the UI can show what the tool is unsure about. Test a fully recognized file, a partially recognized file, and one that is entirely opaque.

## 43. Managed working copy and apply flow
Goal: Never edit the user's project without a reviewed, recoverable step.
Description: Create a managed working copy of an imported project where all tool edits land, and build the review step that shows a per-file diff before anything is written back to the original directory. A recoverable snapshot is taken before the write, and a failed partial write is reported rather than left half-applied. Test applying, cancelling, and a write that fails midway.

## 44. Conflict detection between graph and code edits
Goal: Surface disagreements instead of silently picking a winner.
Description: Track the source snapshot each representation was derived from, reparse affected files after an edit, and detect when graph state and source have diverged for the same stage. Conflicts are reported to the user with both versions; graph blocks update automatically only where the mapping is reliable. Test concurrent edits to the same stage, edits adjacent to an opaque block, and an edit that breaks parsing entirely.

## 45. Docker networking modes
Goal: Pipelines can reach brokers in both common local setups.
Description: Support bridge networking as the default and host networking as an option, including the configuration differences this implies for bootstrap addresses. Detect the advertised-listener mismatch case — where the broker address works from the host but not the container — and explain it in the error rather than reporting a generic timeout. Document both modes with a worked local example.

## 46. Security boundary test suite
Goal: Prove the agent's privileged surface holds.
Description: Write an integration suite that attacks the agent as a hostile local client: missing and forged tokens, cross-origin requests and WebSocket upgrades, path traversal and symlink escape on every file endpoint, and user-controlled values reaching a shell. Credential redaction in API responses, error messages, and agent logs is asserted explicitly. The suite runs in CI and failures are treated as release blockers.

## 47. Generated-file and user-code preservation tests
Goal: Guarantee the tool cannot eat the user's work.
Description: Write an integration suite covering repeated regeneration over a project with hand-edited generated files, editing supported stages around an opaque block, and a code-generation run interrupted partway through. Each case asserts that user-owned content under `src/` and the original text of opaque blocks survive byte-for-byte. Treat failures here as release blockers alongside the security suite.
