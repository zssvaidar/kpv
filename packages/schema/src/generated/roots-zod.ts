// Code generated from schema/roots.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/roots-zod.ts is stale.

import { z } from 'zod';

export const rootUnavailableReasonSchema = z
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
    "Why a stored root is not authorized, present exactly when available is false. notFound: it no longer exists. notDirectory: it is no longer a directory. unreadable: the agent may no longer read or search it. forbidden: it is no longer strictly inside the home directory, or it now holds the agent's own store. tooBroad: it is now the home directory, or above it. changed: it is no longer the directory that was authorized: it now resolves to a different real path, for example because a component of it was replaced by a symbolic link, or another directory has been made at its path. overlaps: it is inside, or contains, a root stored before it. io: it could not be checked.",
  );

export const projectRootSchema = z
  .object({
    path: z
      .string()
      .min(1)
      .describe(
        'The real, absolute path of the root, after every symbolic link on the way to it was followed when it was authorized. Never the link it was authorized through.',
      ),
    available: z
      .boolean()
      .describe(
        'Whether the root is authorized right now. False for a stored root that failed the startup check, or that was found gone or no longer the directory that was authorized when it was last asked about, by a list, an authorization or a browse; it stays false until the root is authorized again or the agent restarts.',
      ),
    reason: rootUnavailableReasonSchema.optional(),
  })
  .strict()
  .describe(
    'One stored project root. An available root is authorized right now and can be browsed and, later, written through. An unavailable one was stored but failed the check the agent repeats at startup; it is listed with the reason rather than silently dropped, and revoking it removes it from the store.',
  );

export const rootRequestSchema = z
  .object({
    path: z
      .string()
      .min(1)
      .describe(
        'An absolute path. For POST, the directory to authorize, which may be spelled any way that resolves to it, a symbolic link included. For DELETE, the root to revoke, spelled as it is listed or any way that resolves to it.',
      ),
  })
  .strict()
  .describe(
    'The body of POST /api/roots, which authorizes a directory, and of DELETE /api/roots, which revokes a root. It is sent as Content-Type: application/json, is at most 4096 bytes, and carries path once and nothing else.',
  );

export const authorizedRootSchema = z
  .object({
    path: z
      .string()
      .min(1)
      .describe(
        'The real, absolute path that was authorized: where the path asked for really leads, never a symbolic link on the way.',
      ),
  })
  .strict()
  .describe(
    'The answer to POST /api/roots: the root that is now authorized. Authorizing a root that already is, under any spelling, answers the same and changes nothing.',
  );

export const rootsErrorKindSchema = z
  .enum([
    'invalid',
    'forbidden',
    'tooBroad',
    'notFound',
    'notDirectory',
    'unreadable',
    'overlaps',
    'io',
  ])
  .describe(
    "Why a root request failed; a client branches on this, not on the message. invalid: the body is not a RootRequest sent as JSON within the size cap, or the path is not absolute. forbidden: the path is outside the home directory, answered the same whether or not anything is there, or it would hold the agent's own store. tooBroad: the path is /, the home directory itself, or above it. notFound: for POST, a path inside the home directory that does not exist; for DELETE, a path that is not an authorized root, whether or not the directory exists. notDirectory: the path is not a directory. unreadable: the agent may not read or search the directory. overlaps: the directory is inside, or contains, a stored root, which root names. io: any other failure, including the store not being written.",
  );

export const rootsErrorSchema = z
  .object({
    error: rootsErrorKindSchema,
    message: z
      .string()
      .min(1)
      .describe(
        'A sentence for a person, the same for every request with this kind.',
      ),
    root: z
      .string()
      .min(1)
      .describe(
        'The stored root the request overlaps, present exactly when error is overlaps.',
      )
      .optional(),
  })
  .strict()
  .describe(
    "The answer to a root request that could not be served. It carries a kind and a fixed message for that kind, and nothing from the underlying error: no resolved path and no cause, so a refusal says nothing about what exists outside the home directory. The one exception is overlaps, which names the stored root the request overlaps, because that root is the user's own and is listed by GET /api/roots anyway.",
  );

export const rootListSchema = z
  .object({
    roots: z
      .array(projectRootSchema)
      .describe(
        'Every stored root, in the order it was authorized, available or not.',
      ),
  })
  .strict()
  .describe(
    "The set of authorized project roots, as the agent's root endpoints answer it (#63): GET /api/roots lists it and DELETE /api/roots answers what is left of it after a revoke. POST /api/roots and DELETE /api/roots take a RootRequest; POST answers an AuthorizedRoot. A request that could not be served is answered with a RootsError instead. This file is the single source of truth for every request and response shape of those routes; the Go structs in the agent and the TypeScript types and Zod schemas in @kpv/schema are generated from it and must never be hand-edited.",
  );
