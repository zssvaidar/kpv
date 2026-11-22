// Code generated from schema/workcopy.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/workcopy-types.ts is stale.
/**
 * Identifies what a working copy holds: a new value every time something is staged, applied or discarded, never reused.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyRevision".
 */
export type WorkcopyRevision = string;
/**
 * Why a working-copy request failed, or why a listed working copy's pipeline is not available, or why a report's path failed. invalid: the request is not well formed. unavailable: the agent has no config directory, so there is no store and nothing is applied. notAuthorized, rootUnavailable, notFound, link, unreadable: as source.schema.json's SourceErrorKind says of the pipeline, a directory on a path or a file; notFound also a snapshot that is not there. changed: the working copy's revision is not the one sent (something was staged, applied or discarded since: draw the review again), or a directory on the way was swapped while it was checked; nothing was written. generated, reserved, notRegularFile, tooLarge, notText: a path or a file as SourceErrorKind says. tooMany: more than 32 files would be staged. drift: project files are not what was staged from (or, for a restore, what the apply wrote); drift lists them and nothing was written. snapshot: the snapshot could not be written, so nothing in the project was. io: anything else.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyErrorKind".
 */
export type WorkcopyErrorKind =
  | 'invalid'
  | 'unavailable'
  | 'notAuthorized'
  | 'rootUnavailable'
  | 'notFound'
  | 'link'
  | 'changed'
  | 'unreadable'
  | 'generated'
  | 'reserved'
  | 'notRegularFile'
  | 'tooLarge'
  | 'notText'
  | 'tooMany'
  | 'drift'
  | 'snapshot'
  | 'io';
/**
 * Why the root is unavailable. The same values, with the same meaning, as source.schema.json's SourceRootReason.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyRootReason".
 */
export type WorkcopyRootReason =
  | 'notFound'
  | 'notDirectory'
  | 'unreadable'
  | 'forbidden'
  | 'tooBroad'
  | 'changed'
  | 'overlaps'
  | 'io';
/**
 * A staged file's path relative to the pipeline directory, the one holding pipeline.yaml. The same rule as source.schema.json's sourcePath. The agent also refuses an empty, . or .. segment, a segment starting with ., a trailing / and a path under generated/; pipeline.yaml at the top may be staged.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyPath".
 */
export type WorkcopyPath = string;
/**
 * A snapshot's id: the UTC second it was taken and eight random hex digits.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopySnapshotId".
 */
export type WorkcopySnapshotId = string;
/**
 * What a staged change does: added (no file was there when it was staged), modified, or deleted (the whole file is removed, never emptied).
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyChange".
 */
export type WorkcopyChange = 'added' | 'modified' | 'deleted';
/**
 * What the project file is now against what the change was staged from. ready: it is still that file, or still absent. changed, created, deleted: it was changed, created or deleted in the project since. link, notRegularFile, tooLarge, notText, unreadable: it is now a symbolic link, not a regular file (or a directory on its path is a file), over 1 MiB, not UTF-8 text without NUL, or not readable by the agent. Every state but ready is drift: apply refuses it.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyState".
 */
export type WorkcopyState =
  | 'ready'
  | 'changed'
  | 'created'
  | 'deleted'
  | 'link'
  | 'notRegularFile'
  | 'tooLarge'
  | 'notText'
  | 'unreadable';
/**
 * What an apply or restore did with one path. written; notWritten (a failure before it stopped the run, so it stays staged); failed (kind says why; it stays staged); unchanged (a restore's file that already holds the snapshot's bytes, because the apply did not write it).
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyResult".
 */
export type WorkcopyResult = 'written' | 'notWritten' | 'failed' | 'unchanged';

/**
 * The managed working copies and their apply flow (#43), as the agent's working-copy endpoints take and answer them. GET /api/workcopies answers a WorkcopyList; GET /api/workcopies/review?root=<root>&directory=<directory> a WorkcopyReview; POST /api/workcopies/apply takes a WorkcopyRevisionRequest and answers a WorkcopyReport; POST /api/workcopies/restore takes a WorkcopySnapshotRequest and answers a WorkcopyReport; POST /api/workcopies/discard takes a WorkcopyRevisionRequest and answers a WorkcopyDiscarded; DELETE /api/workcopies/snapshot takes a WorkcopySnapshotRequest and answers a WorkcopySnapshotRemoved. A request that could not be served is answered with a WorkcopyError. No route stages a change: only the tool stages, in process (#77, #97). This file is the single source of truth for those shapes; the Go structs in the agent and the TypeScript types and Zod schemas in @kpv/schema are generated from it and must never be hand-edited.
 */
export interface WorkcopyList {
  /**
   * Every working copy, sorted by root then directory, byte order.
   */
  copies: WorkcopySummary[];
  /**
   * How many stored working copies could not be read since the agent started; each was moved aside, and its pipeline has no staged changes.
   */
  unreadable: number;
}
/**
 * One working copy in the list. available is false when its pipeline is no longer listed (its directory renamed or removed, its root revoked or unavailable); reason then says why, and rootReason, present exactly when reason is rootUnavailable, why the root is. A working copy that is not available can be discarded but not applied.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopySummary".
 */
export interface WorkcopySummary {
  /**
   * The root the pipeline is in, as listed.
   */
  root: string;
  /**
   * The pipeline's directory inside the root. Absent when the root itself is the pipeline.
   */
  directory?: string;
  revision: WorkcopyRevision;
  /**
   * How many files are staged.
   */
  files: number;
  available: boolean;
  reason?: WorkcopyErrorKind;
  rootReason?: WorkcopyRootReason;
}
/**
 * One staged file in the review. baseText and stagedText are present only when state is ready: baseText when the change was staged from a file (modified or deleted), stagedText unless the change is deleted. Both are exact text, line endings and a missing final newline kept.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyFile".
 */
export interface WorkcopyFile {
  path: WorkcopyPath;
  change: WorkcopyChange;
  state: WorkcopyState;
  /**
   * The project file as it is now, which is what the change was staged from.
   */
  baseText?: string;
  /**
   * The staged text.
   */
  stagedText?: string;
}
/**
 * One snapshot an apply took before its first write: when, in UTC, and every path it holds, sorted. Nothing removes one but the user.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopySnapshot".
 */
export interface WorkcopySnapshot {
  id: WorkcopySnapshotId;
  /**
   * When it was taken, RFC 3339 in UTC.
   */
  taken: string;
  /**
   * @minItems 1
   */
  paths: [WorkcopyPath, ...WorkcopyPath[]];
}
/**
 * What is staged for one listed pipeline, and its snapshots. revision is present exactly when files is not empty, and is what apply and discard send. files are sorted by path, byte order. unreadable is true when the stored working copy of this pipeline could not be read and was moved aside, so nothing is staged. snapshots are newest first.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyReview".
 */
export interface WorkcopyReview {
  /**
   * The root the pipeline is in, as listed.
   */
  root: string;
  /**
   * The pipeline's directory inside the root. Absent when the root itself is the pipeline.
   */
  directory?: string;
  revision?: WorkcopyRevision;
  /**
   * @maxItems 32
   */
  files: WorkcopyFile[];
  unreadable: boolean;
  snapshots: WorkcopySnapshot[];
}
/**
 * The body of POST /api/workcopies/apply and POST /api/workcopies/discard: the pipeline, and the revision the review was drawn from. Sent as Content-Type: application/json, each field at most once, spelled exactly so, at most 8192 bytes.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyRevisionRequest".
 */
export interface WorkcopyRevisionRequest {
  /**
   * The root the pipeline is in, as listed.
   */
  root: string;
  /**
   * The pipeline's directory inside the root. Absent when the root itself is the pipeline.
   */
  directory?: string;
  revision: WorkcopyRevision;
}
/**
 * The body of POST /api/workcopies/restore and DELETE /api/workcopies/snapshot: the pipeline and the snapshot. Read as WorkcopyRevisionRequest is.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopySnapshotRequest".
 */
export interface WorkcopySnapshotRequest {
  /**
   * The root the pipeline is in, as listed.
   */
  root: string;
  /**
   * The pipeline's directory inside the root. Absent when the root itself is the pipeline.
   */
  directory?: string;
  snapshot: WorkcopySnapshotId;
}
/**
 * One path of a report. kind is present exactly when result is failed.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyOutcome".
 */
export interface WorkcopyOutcome {
  path: WorkcopyPath;
  result: WorkcopyResult;
  kind?: WorkcopyErrorKind;
}
/**
 * What an apply or restore did, every path in byte order. snapshot is the snapshot the apply took before its first write, or the one restored. complete is true exactly when no path is failed or notWritten; otherwise the paths not written stay staged and the snapshot stays, so the page offers restore.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyReport".
 */
export interface WorkcopyReport {
  /**
   * The root the pipeline is in, as listed.
   */
  root: string;
  /**
   * The pipeline's directory inside the root. Absent when the root itself is the pipeline.
   */
  directory?: string;
  snapshot: WorkcopySnapshotId;
  complete: boolean;
  /**
   * @minItems 1
   */
  files: [WorkcopyOutcome, ...WorkcopyOutcome[]];
}
/**
 * The answer to a discard: how many staged files were removed from the working copy. The project is not touched.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyDiscarded".
 */
export interface WorkcopyDiscarded {
  /**
   * The root the pipeline is in, as listed.
   */
  root: string;
  /**
   * The pipeline's directory inside the root. Absent when the root itself is the pipeline.
   */
  directory?: string;
  discarded: number;
}
/**
 * The answer to removing a snapshot.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopySnapshotRemoved".
 */
export interface WorkcopySnapshotRemoved {
  /**
   * The root the pipeline is in, as listed.
   */
  root: string;
  /**
   * The pipeline's directory inside the root. Absent when the root itself is the pipeline.
   */
  directory?: string;
  snapshot: WorkcopySnapshotId;
}
/**
 * One project file that is not what an apply or restore expects, and what it is now (never ready).
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyDrift".
 */
export interface WorkcopyDrift {
  path: WorkcopyPath;
  now: WorkcopyState;
}
/**
 * The answer to a working-copy request that could not be served: a kind and a fixed message for that kind, nothing from the underlying error and no path but a drift's. reason is present exactly when error is rootUnavailable; drift exactly when error is drift, listing every differing file sorted by path.
 *
 * This interface was referenced by `WorkcopyList`'s JSON-Schema
 * via the `definition` "workcopyError".
 */
export interface WorkcopyError {
  error: WorkcopyErrorKind;
  /**
   * A sentence for a person, the same for every request with this kind.
   */
  message: string;
  reason?: WorkcopyRootReason;
  /**
   * @minItems 1
   */
  drift?: [WorkcopyDrift, ...WorkcopyDrift[]];
}
