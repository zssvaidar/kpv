// Code generated from schema/pipelines.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/pipelines-types.ts is stale.
/**
 * Why a root is not available, present exactly when available is false. The same values, with the same meaning, as the roots endpoints' RootUnavailableReason; a root found gone or unreadable while it was being scanned is reported with the reason the scan met.
 *
 * This interface was referenced by `PipelineList`'s JSON-Schema
 * via the `definition` "pipelineRootReason".
 */
export type PipelineRootReason =
  | 'notFound'
  | 'notDirectory'
  | 'unreadable'
  | 'forbidden'
  | 'tooBroad'
  | 'changed'
  | 'overlaps'
  | 'io';
/**
 * Why pipeline.yaml did not load. parse: it is not one well-formed YAML document the JSON data model can hold. schema: it is, but it is not a valid pipeline. tooLarge: it is over 1 MiB and was not read. link: it is a symbolic link, which is never read, whatever it points to. notRegularFile: it is a directory, FIFO, socket or device. unreadable: the directory or the file could not be read or searched by the agent.
 *
 * This interface was referenced by `PipelineList`'s JSON-Schema
 * via the `definition` "unreadableKind".
 */
export type UnreadableKind =
  'parse' | 'schema' | 'tooLarge' | 'link' | 'notRegularFile' | 'unreadable';
/**
 * Why a pipeline request failed; a client branches on this, not on the message. invalid: the request is not well formed, the directory name is empty, ., .. or holds /, \ or NUL, or a create field fails the pipeline schema. notAuthorized: the root is not an authorized project root, or was revoked. rootUnavailable: the root is stored but unavailable, which reason says why. notFound: there is no pipeline at that root and directory, or it is not one the list would show. unreadable: pipeline.yaml is there and did not load. rootIsPipeline: a create into a root that is itself a pipeline. changed: the directory a create made was replaced before it was written into, so nothing was. noFreeId: every id derived from the name is taken. io: anything else.
 *
 * This interface was referenced by `PipelineList`'s JSON-Schema
 * via the `definition` "pipelinesErrorKind".
 */
export type PipelinesErrorKind =
  | 'invalid'
  | 'notAuthorized'
  | 'rootUnavailable'
  | 'notFound'
  | 'unreadable'
  | 'rootIsPipeline'
  | 'changed'
  | 'noFreeId'
  | 'io';

/**
 * Every pipeline under the authorized project roots, as the agent's pipeline endpoints answer it (#48): GET /api/pipelines lists them, GET /api/pipelines/open answers a PipelineDocument, and POST /api/pipelines takes a CreatePipelineRequest and answers the PipelineDocument it created. A request that could not be served is answered with a PipelinesError instead. A pipeline is a directory holding pipeline.yaml: an authorized root itself, or a directory directly inside one. It is addressed by its root and directory name, never by its id, because two directories can hold the same id. This file is the single source of truth for every request and response shape of those routes; the Go structs in the agent and the TypeScript types and Zod schemas in @kpv/schema are generated from it and must never be hand-edited.
 */
export interface PipelineList {
  /**
   * Every stored project root, in the order the roots store holds them, available or not. The list is read from disk again for every request.
   */
  roots: PipelineRoot[];
}
/**
 * One stored project root and the pipelines in it. An unavailable root has a reason and no pipelines: it is never left out, and never shown with the pipelines it had before.
 *
 * This interface was referenced by `PipelineList`'s JSON-Schema
 * via the `definition` "pipelineRoot".
 */
export interface PipelineRoot {
  /**
   * The real, absolute path of the root, as GET /api/roots lists it.
   */
  path: string;
  /**
   * Whether the root was authorized and could be scanned for the whole of this list.
   */
  available: boolean;
  reason?: PipelineRootReason;
  /**
   * True when the root has more directory entries than the agent examines (1000), so not every directory in it was looked at. A pipeline may exist that is not listed.
   */
  truncated: boolean;
  /**
   * How many symbolic links directly inside the root were not followed. A link is never listed as a pipeline, whatever it points to.
   */
  skippedLinks: number;
  /**
   * The pipelines in the root, sorted by directory name in byte order. A root that holds pipeline.yaml itself is the one pipeline listed, with no directory, and its subdirectories are not looked at. Empty for an unavailable root.
   */
  pipelines: PipelineEntry[];
}
/**
 * One pipeline directory. A readable entry has its id, language and the time pipeline.yaml was last modified, and its name if the file gives one. An unreadable one has unreadable instead, and no id, name or language, because a file that did not load is not trusted for them; it is still identified by its root and directory.
 *
 * This interface was referenced by `PipelineList`'s JSON-Schema
 * via the `definition` "pipelineEntry".
 */
export interface PipelineEntry {
  /**
   * The root the pipeline is in, as listed.
   */
  root: string;
  /**
   * The name of the pipeline's directory inside the root. Absent when the root itself is the pipeline.
   */
  directory?: string;
  /**
   * Whether pipeline.yaml loaded: it is a regular file of at most 1 MiB that parses and passes the pipeline schema.
   */
  readable: boolean;
  /**
   * The pipeline's id, present exactly when readable is true.
   */
  id?: string;
  /**
   * The pipeline's name, when it is readable and has one.
   */
  name?: string;
  /**
   * The pipeline's language, present exactly when readable is true.
   */
  language?: string;
  /**
   * When pipeline.yaml was last modified, as an RFC 3339 UTC time. Absent when the file could not be described.
   */
  modified?: string;
  unreadable?: UnreadablePipeline;
  /**
   * The other listed pipelines with the same id, compared ignoring letter case. Present only when there are any. Every one of them can still be opened.
   *
   * @minItems 1
   */
  duplicateId?: [PipelineLocation, ...PipelineLocation[]];
}
/**
 * Why a pipeline did not load, and every problem found in it.
 *
 * This interface was referenced by `PipelineList`'s JSON-Schema
 * via the `definition` "unreadablePipeline".
 */
export interface UnreadablePipeline {
  kind: UnreadableKind;
  /**
   * The problems in the document, each with where it is. Empty for tooLarge, link, notRegularFile and unreadable, which are about the file rather than what is in it.
   */
  problems: PipelineProblem[];
}
/**
 * One problem in a pipeline document. The message describes the document, never the disk.
 *
 * This interface was referenced by `PipelineList`'s JSON-Schema
 * via the `definition` "pipelineProblem".
 */
export interface PipelineProblem {
  /**
   * A JSON Pointer to the offending value in the document; empty for the document as a whole.
   */
  pointer: string;
  /**
   * The 1-based line in pipeline.yaml, when it is known.
   */
  line?: number;
  /**
   * The 1-based column in pipeline.yaml, when it is known.
   */
  column?: number;
  message: string;
}
/**
 * Where a pipeline is: its root, and its directory inside the root, absent when the root itself is the pipeline.
 *
 * This interface was referenced by `PipelineList`'s JSON-Schema
 * via the `definition` "pipelineLocation".
 */
export interface PipelineLocation {
  root: string;
  directory?: string;
}
/**
 * The answer to GET /api/pipelines/open?root=<root>&directory=<directory> (directory left out for a root that is itself the pipeline), and to a successful POST /api/pipelines: one pipeline as a JSON document, and the SHA-256 of the exact bytes of pipeline.yaml it was read from, which a later save compares (#78).
 *
 * This interface was referenced by `PipelineList`'s JSON-Schema
 * via the `definition` "pipelineDocument".
 */
export interface PipelineDocument {
  root: string;
  directory?: string;
  /**
   * The pipeline, which passed the pipeline schema in the agent. It is a schema/pipeline.schema.json document, which a $ref cannot reach from here, so the page parses it with the generated pipeline Zod before drawing it.
   */
  pipeline: {};
  /**
   * The SHA-256 of the bytes of pipeline.yaml, in lowercase hex.
   */
  sha256: string;
}
/**
 * The body of POST /api/pipelines. It is sent as Content-Type: application/json, is at most 8192 bytes, and carries each field exactly once, spelled exactly so. The values are checked against the pipeline schema by the agent, which derives the id from the name and writes a pipeline with a source block named source, a destination block named destination and no processing steps.
 *
 * This interface was referenced by `PipelineList`'s JSON-Schema
 * via the `definition` "createPipelineRequest".
 */
export interface CreatePipelineRequest {
  /**
   * The root to create the pipeline in, as listed.
   */
  root: string;
  /**
   * The pipeline's name, the pipeline schema's name.
   */
  name: string;
  /**
   * The pipeline schema's language.
   */
  language: string;
  /**
   * The source block's topic.
   */
  sourceTopic: string;
  /**
   * The destination block's topic.
   */
  destinationTopic: string;
  /**
   * The pipeline schema's runtimeConfiguration.networkMode.
   */
  networkMode: string;
}
/**
 * The answer to a pipeline request that could not be served: a kind and a fixed message for that kind, and nothing from the underlying error. No path is carried but the root and directory the request named. reason is present exactly when error is rootUnavailable, unreadable exactly when error is unreadable, problems only when error is invalid because a field fails the pipeline schema, and directoryLeftBehind only on a create that failed after it made the pipeline's directory.
 *
 * This interface was referenced by `PipelineList`'s JSON-Schema
 * via the `definition` "pipelinesError".
 */
export interface PipelinesError {
  error: PipelinesErrorKind;
  /**
   * A sentence for a person, the same for every request with this kind.
   */
  message: string;
  reason?: PipelineRootReason;
  unreadable?: UnreadablePipeline;
  /**
   * Where the pipeline a create would write fails the pipeline schema, by JSON Pointer into that pipeline.
   *
   * @minItems 1
   */
  problems?: [PipelineProblem, ...PipelineProblem[]];
  /**
   * Whether the directory the create made is still there. True when it could not be shown to be the empty directory the create made, which is then never removed.
   */
  directoryLeftBehind?: boolean;
}
