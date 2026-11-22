// Code generated from schema/workcopy.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/workcopy-zod.ts is stale.

import { z } from 'zod';

export const workcopyPathSchema = z
  .string()
  .regex(new RegExp('^[A-Za-z0-9][A-Za-z0-9._/-]*$'))
  .min(1)
  .max(512)
  .describe(
    "A staged file's path relative to the pipeline directory, the one holding pipeline.yaml. The same rule as source.schema.json's sourcePath. The agent also refuses an empty, . or .. segment, a segment starting with ., a trailing / and a path under generated/; pipeline.yaml at the top may be staged.",
  );

export const workcopyRevisionSchema = z
  .string()
  .regex(new RegExp('^[0-9a-f]{32}$'))
  .describe(
    'Identifies what a working copy holds: a new value every time something is staged, applied or discarded, never reused.',
  );

export const workcopySnapshotIdSchema = z
  .string()
  .regex(new RegExp('^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$'))
  .describe(
    "A snapshot's id: the UTC second it was taken and eight random hex digits.",
  );

export const workcopyRootReasonSchema = z
  .enum([
    'notFound',
    'notDirectory',
    'unreadable',
    'forbidden',
    'tooBroad',
    'changed',
    'overlaps',
    'io',
  ])
  .describe(
    "Why the root is unavailable. The same values, with the same meaning, as source.schema.json's SourceRootReason.",
  );

export const workcopyErrorKindSchema = z
  .enum([
    'invalid',
    'unavailable',
    'notAuthorized',
    'rootUnavailable',
    'notFound',
    'link',
    'changed',
    'unreadable',
    'generated',
    'reserved',
    'notRegularFile',
    'tooLarge',
    'notText',
    'tooMany',
    'drift',
    'snapshot',
    'io',
  ])
  .describe(
    "Why a working-copy request failed, or why a listed working copy's pipeline is not available, or why a report's path failed. invalid: the request is not well formed. unavailable: the agent has no config directory, so there is no store and nothing is applied. notAuthorized, rootUnavailable, notFound, link, unreadable: as source.schema.json's SourceErrorKind says of the pipeline, a directory on a path or a file; notFound also a snapshot that is not there. changed: the working copy's revision is not the one sent (something was staged, applied or discarded since: draw the review again), or a directory on the way was swapped while it was checked; nothing was written. generated, reserved, notRegularFile, tooLarge, notText: a path or a file as SourceErrorKind says. tooMany: more than 32 files would be staged. drift: project files are not what was staged from (or, for a restore, what the apply wrote); drift lists them and nothing was written. snapshot: the snapshot could not be written, so nothing in the project was. io: anything else.",
  );

export const workcopySummarySchema = z
  .object({
    root: z.string().min(1).describe('The root the pipeline is in, as listed.'),
    directory: z
      .string()
      .min(1)
      .describe(
        "The pipeline's directory inside the root. Absent when the root itself is the pipeline.",
      )
      .optional(),
    revision: workcopyRevisionSchema,
    files: z
      .number()
      .int()
      .gte(1)
      .lte(32)
      .describe('How many files are staged.'),
    available: z.boolean(),
    reason: workcopyErrorKindSchema.optional(),
    rootReason: workcopyRootReasonSchema.optional(),
  })
  .strict()
  .describe(
    'One working copy in the list. available is false when its pipeline is no longer listed (its directory renamed or removed, its root revoked or unavailable); reason then says why, and rootReason, present exactly when reason is rootUnavailable, why the root is. A working copy that is not available can be discarded but not applied.',
  );

export const workcopyChangeSchema = z
  .enum(['added', 'modified', 'deleted'])
  .describe(
    'What a staged change does: added (no file was there when it was staged), modified, or deleted (the whole file is removed, never emptied).',
  );

export const workcopyStateSchema = z
  .enum([
    'ready',
    'changed',
    'created',
    'deleted',
    'link',
    'notRegularFile',
    'tooLarge',
    'notText',
    'unreadable',
  ])
  .describe(
    'What the project file is now against what the change was staged from. ready: it is still that file, or still absent. changed, created, deleted: it was changed, created or deleted in the project since. link, notRegularFile, tooLarge, notText, unreadable: it is now a symbolic link, not a regular file (or a directory on its path is a file), over 1 MiB, not UTF-8 text without NUL, or not readable by the agent. Every state but ready is drift: apply refuses it.',
  );

export const workcopyFileSchema = z
  .object({
    path: workcopyPathSchema,
    change: workcopyChangeSchema,
    state: workcopyStateSchema,
    baseText: z
      .string()
      .describe(
        'The project file as it is now, which is what the change was staged from.',
      )
      .optional(),
    stagedText: z.string().describe('The staged text.').optional(),
  })
  .strict()
  .describe(
    'One staged file in the review. baseText and stagedText are present only when state is ready: baseText when the change was staged from a file (modified or deleted), stagedText unless the change is deleted. Both are exact text, line endings and a missing final newline kept.',
  );

export const workcopySnapshotSchema = z
  .object({
    id: workcopySnapshotIdSchema,
    taken: z
      .string()
      .datetime({ offset: true })
      .describe('When it was taken, RFC 3339 in UTC.'),
    paths: z.array(workcopyPathSchema).min(1),
  })
  .strict()
  .describe(
    'One snapshot an apply took before its first write: when, in UTC, and every path it holds, sorted. Nothing removes one but the user.',
  );

export const workcopyReviewSchema = z
  .object({
    root: z.string().min(1).describe('The root the pipeline is in, as listed.'),
    directory: z
      .string()
      .min(1)
      .describe(
        "The pipeline's directory inside the root. Absent when the root itself is the pipeline.",
      )
      .optional(),
    revision: workcopyRevisionSchema.optional(),
    files: z.array(workcopyFileSchema).max(32),
    unreadable: z.boolean(),
    snapshots: z.array(workcopySnapshotSchema),
  })
  .strict()
  .describe(
    'What is staged for one listed pipeline, and its snapshots. revision is present exactly when files is not empty, and is what apply and discard send. files are sorted by path, byte order. unreadable is true when the stored working copy of this pipeline could not be read and was moved aside, so nothing is staged. snapshots are newest first.',
  );

export const workcopyRevisionRequestSchema = z
  .object({
    root: z.string().min(1).describe('The root the pipeline is in, as listed.'),
    directory: z
      .string()
      .min(1)
      .describe(
        "The pipeline's directory inside the root. Absent when the root itself is the pipeline.",
      )
      .optional(),
    revision: workcopyRevisionSchema,
  })
  .strict()
  .describe(
    'The body of POST /api/workcopies/apply and POST /api/workcopies/discard: the pipeline, and the revision the review was drawn from. Sent as Content-Type: application/json, each field at most once, spelled exactly so, at most 8192 bytes.',
  );

export const workcopySnapshotRequestSchema = z
  .object({
    root: z.string().min(1).describe('The root the pipeline is in, as listed.'),
    directory: z
      .string()
      .min(1)
      .describe(
        "The pipeline's directory inside the root. Absent when the root itself is the pipeline.",
      )
      .optional(),
    snapshot: workcopySnapshotIdSchema,
  })
  .strict()
  .describe(
    'The body of POST /api/workcopies/restore and DELETE /api/workcopies/snapshot: the pipeline and the snapshot. Read as WorkcopyRevisionRequest is.',
  );

export const workcopyResultSchema = z
  .enum(['written', 'notWritten', 'failed', 'unchanged'])
  .describe(
    "What an apply or restore did with one path. written; notWritten (a failure before it stopped the run, so it stays staged); failed (kind says why; it stays staged); unchanged (a restore's file that already holds the snapshot's bytes, because the apply did not write it).",
  );

export const workcopyOutcomeSchema = z
  .object({
    path: workcopyPathSchema,
    result: workcopyResultSchema,
    kind: workcopyErrorKindSchema.optional(),
  })
  .strict()
  .describe(
    'One path of a report. kind is present exactly when result is failed.',
  );

export const workcopyReportSchema = z
  .object({
    root: z.string().min(1).describe('The root the pipeline is in, as listed.'),
    directory: z
      .string()
      .min(1)
      .describe(
        "The pipeline's directory inside the root. Absent when the root itself is the pipeline.",
      )
      .optional(),
    snapshot: workcopySnapshotIdSchema,
    complete: z.boolean(),
    files: z.array(workcopyOutcomeSchema).min(1),
  })
  .strict()
  .describe(
    'What an apply or restore did, every path in byte order. snapshot is the snapshot the apply took before its first write, or the one restored. complete is true exactly when no path is failed or notWritten; otherwise the paths not written stay staged and the snapshot stays, so the page offers restore.',
  );

export const workcopyDiscardedSchema = z
  .object({
    root: z.string().min(1).describe('The root the pipeline is in, as listed.'),
    directory: z
      .string()
      .min(1)
      .describe(
        "The pipeline's directory inside the root. Absent when the root itself is the pipeline.",
      )
      .optional(),
    discarded: z.number().int().gte(1),
  })
  .strict()
  .describe(
    'The answer to a discard: how many staged files were removed from the working copy. The project is not touched.',
  );

export const workcopySnapshotRemovedSchema = z
  .object({
    root: z.string().min(1).describe('The root the pipeline is in, as listed.'),
    directory: z
      .string()
      .min(1)
      .describe(
        "The pipeline's directory inside the root. Absent when the root itself is the pipeline.",
      )
      .optional(),
    snapshot: workcopySnapshotIdSchema,
  })
  .strict()
  .describe('The answer to removing a snapshot.');

export const workcopyDriftSchema = z
  .object({ path: workcopyPathSchema, now: workcopyStateSchema })
  .strict()
  .describe(
    'One project file that is not what an apply or restore expects, and what it is now (never ready).',
  );

export const workcopyErrorSchema = z
  .object({
    error: workcopyErrorKindSchema,
    message: z
      .string()
      .min(1)
      .describe(
        'A sentence for a person, the same for every request with this kind.',
      ),
    reason: workcopyRootReasonSchema.optional(),
    drift: z.array(workcopyDriftSchema).min(1).optional(),
  })
  .strict()
  .describe(
    "The answer to a working-copy request that could not be served: a kind and a fixed message for that kind, nothing from the underlying error and no path but a drift's. reason is present exactly when error is rootUnavailable; drift exactly when error is drift, listing every differing file sorted by path.",
  );

export const workcopyListSchema = z
  .object({
    copies: z
      .array(workcopySummarySchema)
      .describe(
        'Every working copy, sorted by root then directory, byte order.',
      ),
    unreadable: z
      .number()
      .int()
      .gte(0)
      .describe(
        'How many stored working copies could not be read since the agent started; each was moved aside, and its pipeline has no staged changes.',
      ),
  })
  .strict()
  .describe(
    "The managed working copies and their apply flow (#43), as the agent's working-copy endpoints take and answer them. GET /api/workcopies answers a WorkcopyList; GET /api/workcopies/review?root=<root>&directory=<directory> a WorkcopyReview; POST /api/workcopies/apply takes a WorkcopyRevisionRequest and answers a WorkcopyReport; POST /api/workcopies/restore takes a WorkcopySnapshotRequest and answers a WorkcopyReport; POST /api/workcopies/discard takes a WorkcopyRevisionRequest and answers a WorkcopyDiscarded; DELETE /api/workcopies/snapshot takes a WorkcopySnapshotRequest and answers a WorkcopySnapshotRemoved. A request that could not be served is answered with a WorkcopyError. No route stages a change: only the tool stages, in process (#77, #97). This file is the single source of truth for those shapes; the Go structs in the agent and the TypeScript types and Zod schemas in @kpv/schema are generated from it and must never be hand-edited.",
  );
