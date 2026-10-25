// Code generated from schema/directory.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/directory-zod.ts is stale.

import { z } from 'zod';

export const directoryEntryKindSchema = z
  .enum(['directory', 'file', 'symlink', 'other', 'unknown'])
  .describe(
    'What an entry is, without following it. A symbolic link is a symlink whatever it points at. other is a named pipe, a socket or a device, which is never opened. unknown is an entry the agent could not describe, and is always unreadable.',
  );

export const linkTargetSchema = z
  .enum(['directory', 'file', 'other', 'broken', 'outside', 'unknown'])
  .describe(
    'What a symbolic link leads to, present exactly when the entry is a symlink, and judged the way any browsed path is. outside means it leads out of the browsable directories: it is not followed, and nothing is said about what is there. broken is a target inside them that does not exist, or a cycle. unknown is a target that could not be looked at because a directory on the way cannot be read.',
  );

export const directoryEntrySchema = z
  .object({
    name: z.string().min(1).describe("The entry's name in its directory."),
    path: z
      .string()
      .min(1)
      .describe(
        "The listed directory's path joined with the name. For a symbolic link it is the link itself, not where the link leads.",
      ),
    kind: directoryEntryKindSchema,
    unreadable: z
      .boolean()
      .describe(
        'True when the agent cannot read the entry: a directory it cannot open, a file it cannot open, an entry it cannot describe at all, or a symbolic link whose target is one of those. Such an entry is listed by name rather than left out.',
      ),
    linkTarget: linkTargetSchema.optional(),
  })
  .strict()
  .describe('One entry of a directory, described without following it.');

export const projectEvidenceSchema = z
  .object({
    marker: z
      .string()
      .min(1)
      .describe("The marker's name, such as .git or go.mod."),
    describes: z
      .string()
      .min(1)
      .describe('What the marker usually means, for a person to read.'),
  })
  .strict()
  .describe(
    'One project marker found in a directory: a file or directory whose presence usually means a project, and what it usually means. Only presence is checked; nothing is opened or parsed.',
  );

export const directoryErrorKindSchema = z
  .enum([
    'invalid',
    'forbidden',
    'notFound',
    'unreadable',
    'notDirectory',
    'io',
  ])
  .describe(
    'Why a browsing request failed; a client branches on this, not on the message. forbidden is a path outside the browsable directories, answered the same whether or not anything is there. notFound is a path inside them that does not exist, which is the only case that says a path is absent. unreadable is a path inside them the agent may not read. notDirectory is a path that is not a directory. invalid is a request the agent will not reason about: no path, a relative path, a path given twice, or a limit outside 1 to 1000. io is any other failure.',
  );

export const directoryErrorSchema = z
  .object({
    error: directoryErrorKindSchema,
    message: z
      .string()
      .min(1)
      .describe(
        'A sentence for a person, the same for every request with this kind.',
      ),
  })
  .strict()
  .describe(
    'The answer to a browsing request that could not be served. It carries a kind and a fixed message for that kind, and nothing else: no resolved path, no root and no cause, so a refusal says nothing about what exists outside the browsable directories.',
  );

export const directoryListingSchema = z
  .object({
    path: z
      .string()
      .min(1)
      .describe(
        "The real, absolute path of the directory listed, after every symbolic link on the way to it was followed. It is the path to build an entry's path from and the one to ask for again.",
      ),
    parent: z
      .string()
      .min(1)
      .describe(
        'The real path of the directory above this one, present only when that directory can be browsed too. It is absent at the top of a browsable directory, such as the home directory itself.',
      )
      .optional(),
    entries: z
      .array(directoryEntrySchema)
      .max(1000)
      .describe(
        'The entries of this page, sorted by name. Never more than limit of them.',
      ),
    truncated: z
      .boolean()
      .describe(
        'True when the directory has more entries than this page holds. The listing is bounded: a larger directory is not read to the end, so a truncated page is the first limit entries the filesystem returned, sorted by name, and not the alphabetical beginning of the directory.',
      ),
    limit: z
      .number()
      .int()
      .gte(1)
      .lte(1000)
      .describe(
        'The most entries this page could hold: the limit the request asked for, or 1000 when it asked for none.',
      ),
    looksLikeProject: z
      .boolean()
      .describe(
        'Whether the directory looks like a project: true when at least one project marker was found directly in it. A cheap heuristic and nothing more; detecting the language and entry point of a project is #41.',
      ),
    evidence: z
      .array(projectEvidenceSchema)
      .describe(
        'Every project marker found directly in the directory, in a fixed order. Empty when looksLikeProject is false.',
      ),
  })
  .strict()
  .describe(
    "One page of a directory, as the agent's directory browsing endpoint (GET /api/directories) answers it: what the directory holds, whether it looks like a project and on what evidence, and whether the page is all of it. Only the authorized project roots and the user's home directory can be browsed. This file is the single source of truth for the response; the Go structs in the agent and the TypeScript types and Zod schemas in @kpv/schema are generated from it and must never be hand-edited. A failed request is answered with a DirectoryError instead.",
  );
