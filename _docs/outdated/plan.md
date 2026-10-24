# Kafka Pipeline Visualizer & Editor
## Technical Specification — Draft v0.1

**Archived:** superseded by the GitHub issue backlog at
https://github.com/zssvaidar/kpv/issues, kept for reference.  
**Status:** Initial specification  
**Scope:** MVP  
**Target users:** Kafka developers  
**Primary goal:** Build a visual development environment for creating, understanding, editing, testing, deploying, and monitoring Kafka message-processing pipelines.

---

## 1. Product overview

Kafka Pipeline Visualizer is a browser-based tool that represents Kafka processing pipelines as editable visual graphs while maintaining a relationship between the graph and source code.

The tool supports two workflows:

1. **Visual-first development:** Create a pipeline using visual blocks, edit processing logic, and generate a complete runnable project.
2. **Code-first development:** Import an existing local project, recognize supported pipeline constructs, visualize the supported portions, and edit them without destroying unsupported code.

The MVP targets simple, linear message-processing pipelines:

**Kafka source → processing steps → Kafka destination**

The tool supports Python, TypeScript, and Go, with language-specific parsing, code generation, and runtime packaging.

### Core product principles

- Visual editing and source-code editing work together.
- Existing user code must not be silently overwritten.
- Unsupported code is preserved rather than guessed or discarded.
- Pipelines have independent deployment and runtime lifecycles.
- Kafka connectivity and deployment are controlled by a local agent.
- Testing and inspection happen before production deployment.
- Pipeline definitions remain portable and file-based.

## 2. MVP scope

### Included

- Create pipelines from scratch using visual blocks.
- Import existing projects from local directories.
- Support Python, TypeScript, and Go.
- Display a linear pipeline graph.
- Edit processing logic in embedded and full-file Monaco editors.
- Synchronize graph and code where the implementation supports reliable mapping.
- Preserve unsupported code as opaque blocks.
- Generate complete runnable projects for visual-first pipelines.
- Reuse existing dependency definitions for imported projects.
- Connect to external Kafka clusters using configurable bootstrap servers and SASL/TLS settings.
- Use Docker Compose and one container per pipeline.
- Configure local environment variables and `.env` files without committing secrets.
- Replay sampled or manually entered messages locally.
- Inspect intermediate processing outputs.
- Display live logs and basic runtime status.
- Deploy, stop, and recreate pipelines.
- Sample live Kafka messages for inspection and field inference.

### Explicitly outside the initial scope

- Git integration.
- Branching and conditional graph execution.
- Avro and Protobuf message formats.
- Automatic dependency installation or modification.
- Automatic pipeline restart after failure.
- Full message-level tracing across every processing stage.
- Advanced orchestration platforms.
- Automatic schema enforcement.
- A database-backed configuration system.
- A built-in test runner for conventional automated test suites.

These exclusions can be revisited after the MVP.

## 3. Supported pipeline model

### 3.1 Graph structure

The MVP graph is linear and ordered.

Example:

`Kafka Source → Filter → Transform → Validate → Kafka Destination`

Not every pipeline needs all processing steps. A source and destination with zero or more processing blocks are sufficient.

The graph supports drag-and-drop ordering and block insertion as part of the intended visual editing experience.

### 3.2 Block types

| Block | Responsibility |
|---|---|
| Kafka Source | Configure the input topic and consumer behavior |
| Filter | Decide whether a message continues through the pipeline |
| Transform / Map | Modify a message payload |
| Validate | Check message conditions and handle invalid messages according to configured behavior |
| Custom Code | Execute user-defined Python, TypeScript, or Go processing logic |
| Kafka Destination | Configure the output topic and producer behavior |

Source and destination blocks contain Kafka configuration. Processing blocks contain editable logic.

### 3.3 Message representation

The MVP supports JSON and plain-text payloads.

For JSON messages, the tool infers available fields from sampled messages. Field dropdowns can suggest fields for transformations and filters.

Inferred fields are suggestions, not a formal schema. Missing or changing fields must not automatically be treated as a guaranteed validation failure.

### 3.4 Kafka message metadata

- Preserve the original message key by default.
- Allow custom processing code to change the key.
- Preserve Kafka headers and timestamps where possible.
- Expose partition and offset as read-only inspection metadata.
- Do not treat partition and offset as ordinary editable payload fields.

## 4. Architecture

The proposed architecture has three main components:

1. Browser-based frontend.
2. Local agent written in Go.
3. Docker-based pipeline runtimes connected to Kafka clusters.

```text
┌────────────────────────────────────────────┐
│              Browser UI                    │
│                                            │
│ React + TypeScript                         │
│ React Flow / @xyflow/react                 │
│ Monaco Editor                              │
│ Graph editor · Code editor · Logs          │
└─────────────────────┬──────────────────────┘
                      │
             Local HTTP + WebSocket
                      │
┌─────────────────────▼──────────────────────┐
│                Go Agent                    │
│                                            │
│ Local project files                        │
│ Pipeline model and synchronization         │
│ Docker Compose lifecycle                   │
│ Kafka connectivity checks                  │
│ Logs and runtime status                    │
│ Local configuration and credentials        │
└─────────────────────┬──────────────────────┘
                      │
                 Docker Engine
                      │
       ┌──────────────┼──────────────┐
       ▼              ▼              ▼
  Pipeline A      Pipeline B     Pipeline C
  Container       Container      Container
       │              │              │
       └──────────────┼──────────────┘
                      ▼
              External Kafka
```

Each pipeline has its own container and independent lifecycle. The agent manages local development and deployment, while the browser provides the editing interface.

### 4.1 Frontend

**Technology:** React + TypeScript.

Responsibilities:

- Render and edit the graph.
- Display block configuration.
- Embed Monaco editors in custom-code blocks.
- Provide a full-file code editor.
- Show message inputs and intermediate outputs.
- Display deployment status and logs.
- Present configuration and parsing warnings.

**Graph library:** `@xyflow/react` is the candidate.

**Code editor:** Monaco Editor is used for both embedded and full-file editing.

### 4.2 Local agent

**Technology:** Go.

Responsibilities:

- Expose the local HTTP API.
- Stream logs and status over WebSocket.
- Read and write pipeline configuration files.
- Manage working copies and generated files.
- Parse or invoke language-specific parsing components.
- Manage Docker Compose configurations and containers.
- Validate Kafka connectivity.
- Perform deployment and stop operations.
- Run local message-replay tests.
- Protect local credentials and project files.

The agent should be a standalone executable.

### 4.3 Pipeline containers

Each pipeline runs in its own container with independent:

- Runtime configuration.
- Logs.
- Deployment lifecycle.
- Consumer group configuration.
- Failure status.

Use separate minimal images for Python, TypeScript/Node.js, and Go. For imported projects, build using existing dependency configuration instead of silently installing or modifying dependencies through the editor.

### 4.4 Kafka networking

Support two modes:

- Explicit Docker networking as the default.
- Optional host networking for local development.

The user configures bootstrap servers and the appropriate security settings. The tool must account for Kafka advertised listeners, which can cause connectivity failures when broker addresses differ between the host and container environments.

## 5. Pipeline source of truth and synchronization

This is a central architectural requirement.

### 5.1 Visual-first pipelines

The language-neutral pipeline model is authoritative.

The graph and model determine the generated processing code and runtime configuration.

A proposed internal representation is:

```text
Pipeline
 ├── id
 ├── language
 ├── source
 ├── processingSteps[]
 ├── destination
 └── runtimeConfiguration
```

Each processing step has a stable identifier, type, configuration, and optional custom-code reference.

The actual schema should be finalized before implementing code generation.

### 5.2 Imported projects

Existing source files remain authoritative.

The tool parses the source using language-specific AST parsers and maps recognized constructs into graph blocks. Optional annotations may clarify ambiguous stages.

The MVP should recognize only a defined set of supported patterns. It must not assume that arbitrary code is equivalent to a supported visual block.

### 5.3 Unsupported constructs

Unsupported code is represented as an opaque block.

Requirements:

- Preserve the original code.
- Allow supported sections to be edited around it.
- Warn when adjacent edits may affect the opaque block.
- Avoid silently deleting or rewriting unknown constructs.
- Keep synchronization limited to code that can be mapped reliably.

### 5.4 Conflict handling

When code and graph edits occur concurrently, the system needs a deterministic synchronization strategy.

Proposed approach:

- Maintain a current source snapshot.
- Track changes made by each editor.
- Reparse affected code after edits.
- Update graph blocks only when the mapping is reliable.
- Surface conflicts rather than silently choosing one representation.
- Preserve a recoverable copy before applying changes to the original project.

The exact incremental synchronization mechanism remains an implementation decision.

## 6. Project files and configuration

Pipeline configuration is stored in versionable local files alongside source code.

A proposed project layout is:

```text
pipeline/
├── pipeline.yaml
├── config/
│   └── runtime.yaml
├── src/
│   └── ...
├── generated/
│   └── ...
├── docker/
│   └── Dockerfile
├── compose.yaml
├── .env
└── .gitignore
```

This is an illustrative layout, not a finalized directory contract.

Generated files must be separated from user-owned files so regeneration does not overwrite custom code.

### 6.1 Dependencies

- Detect existing dependency manifests.
- Reuse the dependencies already defined by the project.
- Do not automatically install packages through the editing interface.
- Do not automatically modify dependency manifests.
- Report missing dependencies and build failures clearly.

For visual-first pipelines, the tool generates the runtime and project scaffolding required to run the pipeline.

### 6.2 Secrets

Use environment variables and local `.env` files.

Requirements:

- Exclude `.env` files containing secrets from Git by default through the generated `.gitignore`.
- Inject credentials into containers at runtime.
- Avoid writing secrets into generated source files or ordinary logs.
- Warn when credentials are detected in tracked files where detection is possible.
- Keep browser-facing configuration separate from sensitive credentials.

## 7. Deployment and runtime lifecycle

### 7.1 Deployment

The user reviews and explicitly applies changes to the managed working copy and original project.

Before deployment, perform basic syntax and required-configuration checks.

The MVP does not require a conventional automated test suite or build-preview workflow, but it does support local sample-message replay.

The exact deployment confirmation policy remains open.

### 7.2 Updates

When a running pipeline is updated, stop and recreate its container.

The MVP accepts downtime during updates.

The implementation must document the implications for in-flight messages, offset commits, and potential duplicate processing. It must not promise exactly-once behavior across container restarts.

### 7.3 Consumer groups

Generate a unique consumer group ID per pipeline by default. Allow users to configure it manually.

Use normal Kafka client behavior for committed offsets and consumer-group recovery across restarts.

### 7.4 Failures

When a pipeline fails:

- Mark it as failed.
- Expose its logs.
- Allow the developer to diagnose the problem.
- Require manual restart in the MVP.

Automatic restarts and advanced recovery policies are outside the initial scope.

### 7.5 Runtime status

Display basic status for the pipeline and individual blocks where information is available:

- Running.
- Stopped.
- Failed.

The MVP does not require throughput, error-rate, or consumer-lag metrics in the graph.

## 8. Local message testing and inspection

The testing workflow is a key product feature.

### 8.1 Inputs

Allow developers to:

- Sample messages from a live Kafka topic.
- Paste or manually write JSON and plain-text messages.

### 8.2 Replay

Replay messages through the processing logic locally before deployment.

For each supported stage, display the input and resulting output.

The replay workflow must not publish test results to production Kafka destinations by default. It should isolate test execution from deployed pipelines and clearly indicate where the runtime differs from production.

### 8.3 Inspection

Display:

- Message payload.
- Kafka key.
- Headers and timestamp where available.
- Partition and offset for sampled Kafka messages.
- Intermediate processing outputs.
- Processing errors where available.

Message history retention, payload truncation, and redaction policies remain to be specified.

## 9. Logging and monitoring

### 9.1 MVP logs

Provide a basic live log viewer based on container stdout and stderr.

Include basic search and severity filtering where severity can be reliably determined.

Do not require fully structured per-message logs for the MVP.

### 9.2 Future extension

The logging architecture should allow later additions for:

- Structured processing-stage events.
- Message correlation IDs.
- Per-stage execution duration.
- Detailed failure context.
- More comprehensive message tracing.

### 9.3 Graph monitoring

The graph provides visual runtime status. Sampled-message inspection provides visibility into data transformations.

Full live tracing through every processing stage is not an MVP requirement.

## 10. Local API and security

The local agent has access to Docker, project files, and credentials. It must be treated as a privileged local service.

Required controls:

- Bind the API to loopback only.
- Generate a random session token.
- Require the token for authorized browser requests.
- Validate browser origins.
- Protect WebSocket connections with equivalent authentication and origin validation.
- Avoid exposing credentials in logs or error messages.
- Restrict file operations to explicitly authorized project directories.
- Validate paths to prevent traversal outside the selected project.
- Avoid arbitrary shell execution from user-controlled API parameters.

The deployment and destructive-action confirmation policy is still open.

## 11. Import and compatibility

The MVP imports local project directories only.

It should:

1. Inspect the directory structure.
2. Detect the programming language and dependency manifests.
3. Identify likely Kafka pipeline entry points.
4. Parse supported constructs.
5. Build a graph from recognized stages.
6. Represent unrecognized code as opaque blocks.
7. Report uncertainty and parsing warnings.
8. Create a managed working copy before applying edits.

The supported Kafka libraries, entry-point detection rules, project layouts, and exact AST coverage must be defined separately for Python, TypeScript, and Go.

The product should not claim support for arbitrary projects merely because their language is supported.

## 12. Proposed implementation phases

### Phase 1 — Foundation

- Define the pipeline model and configuration schema.
- Establish the local project layout.
- Implement the Go agent.
- Build the React application.
- Implement secure local HTTP and WebSocket communication.
- Integrate the graph and Monaco Editor.

**Deliverable:** A local application shell with editable graph state and file access.

### Phase 2 — Visual-first pipelines

- Implement source, destination, filter, transform, validate, and custom-code blocks.
- Implement pipeline-model persistence.
- Generate runnable projects for Python, TypeScript, and Go.
- Add language-specific Docker images and Compose configuration.
- Add dependency-manifest detection.

**Deliverable:** Create a pipeline visually and generate a runnable project.

### Phase 3 — Kafka and local testing

- Configure external Kafka connections.
- Implement SASL/TLS configuration.
- Implement connectivity checks.
- Sample live messages.
- Support manual test inputs.
- Replay messages locally and inspect intermediate outputs.

**Deliverable:** Test processing logic before deployment.

### Phase 4 — Runtime management

- Deploy each pipeline in its own container.
- Implement stop and recreate operations.
- Display runtime status.
- Stream container logs.
- Handle failed containers and manual restart.
- Validate configuration before deployment.

**Deliverable:** Manage pipeline lifecycles from the browser.

### Phase 5 — Import and synchronization

- Implement language-specific AST parsing.
- Recognize supported Kafka patterns.
- Map recognized code to visual blocks.
- Preserve opaque blocks.
- Add optional annotations.
- Synchronize graph and source edits where mapping is reliable.
- Add conflict warnings and recovery safeguards.

**Deliverable:** Safely inspect and edit supported portions of existing projects.

### Phase 6 — Hardening

- Test security boundaries.
- Test Docker networking modes.
- Test restarts and offset behavior.
- Test failure reporting.
- Verify generated files cannot overwrite user-owned code.
- Test supported language patterns and unsupported-code preservation.

**Deliverable:** A dependable developer-facing MVP.

## 13. Initial acceptance criteria

The MVP is ready for evaluation when a developer can:

1. Create a linear pipeline with a source, processing blocks, and a destination.
2. Generate a runnable project in Python, TypeScript, or Go.
3. Edit processing code in a block and in the full-file editor.
4. Import a local project and visualize supported constructs.
5. Preserve unsupported code while editing supported sections.
6. Configure a Kafka connection without committing secrets.
7. Sample or manually enter messages and replay them locally.
8. Inspect intermediate processing outputs.
9. Deploy a pipeline in its own Docker container.
10. View live logs and running, stopped, or failed status.
11. Stop and recreate a pipeline after changes.
12. Recover from common configuration and runtime errors using logs and warnings.

The detailed support matrix and acceptance tests must be finalized for each language and Kafka client.

## 14. Open decisions

These questions remain intentionally unresolved:

- Deployment confirmation and automation policy.
- Rollback and pipeline deletion behavior.
- Docker installation and prerequisite checks.
- Exact Kafka connectivity validation.
- Large-message display and truncation.
- Sample and trace retention.
- Undo and redo behavior across graph and code.
- Error presentation and diagnostics.
- Exact import patterns and Kafka libraries.
- Whether the three languages require identical capabilities in the MVP.
- Unit and integration testing requirements.
- Agent distribution and supported operating systems.
- Single-user assumptions and telemetry/privacy policy.

These should be decided before finalizing the implementation plan.

## 15. Main architectural risks

**Bidirectional synchronization:** Parsing arbitrary source code into a reliable visual model is the hardest technical challenge. Limit automatic synchronization to explicitly supported patterns.

**Code generation:** Generated projects must be runnable and predictable without overwriting user-owned files.

**Kafka networking:** Advertised listeners, TLS configuration, SASL credentials, and container DNS can complicate connectivity.

**Restart semantics:** Container recreation can cause downtime, replay, and duplicate processing depending on offset commits and message-processing behavior.

**Testing fidelity:** Local replay is useful but cannot fully reproduce Kafka partitioning, consumer-group behavior, transactions, or production timing.

**Security:** The local agent is a privileged service. Authentication, origin validation, path restrictions, and careful handling of credentials are essential.

## 16. MVP guiding principle

The product should prioritize **safe, understandable editing over attempting to visualize every possible Kafka program**.

A small set of reliable visual constructs, complete generated projects, local message replay, and safe preservation of unsupported code will provide a stronger first release than broad but unreliable language support.
