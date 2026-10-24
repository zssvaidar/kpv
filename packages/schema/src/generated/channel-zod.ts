// Code generated from schema/channel.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/channel-zod.ts is stale.

import { z } from 'zod';

export const pipelineIdSchema = z
  .string()
  .regex(new RegExp('^[A-Za-z][A-Za-z0-9_-]{0,62}$'))
  .describe(
    'The id of the pipeline a message is about. The pattern is the one pipeline.schema.json gives an identifier, and scripts/generate-schema.mjs fails when the two drift apart.',
  );

export const channelMessageKindSchema = z
  .enum(['subscribed', 'unsubscribed', 'error', 'log', 'status'])
  .describe(
    "The kinds of message the channel carries. subscribed and unsubscribed acknowledge a client's own command, so a client knows when a subscription is in force rather than having to wait and hope. error reports that a command could not be honoured, and never quotes back what the client sent. log and status are the two streams the channel exists for; what goes in their payloads is #34 and #35.",
  );

export const channelPayloadSchema = z
  .record(z.string(), z.any())
  .describe(
    "A message body, as an open object. It is deliberately unconstrained here: the channel's job is delivery, and what a log or a status message contains is defined by #34 and #35. Nothing that authorizes a client ever appears in it: the session token reaches the agent on the handshake and the code that builds a payload cannot see it.",
  );

export const channelCommandNameSchema = z
  .enum(['subscribe', 'unsubscribe'])
  .describe('The commands a client may send.');

export const channelCommandSchema = z
  .object({ command: channelCommandNameSchema, pipelineId: pipelineIdSchema })
  .strict()
  .describe(
    'The one thing a client may send the agent on the channel: which pipeline it wants messages for. There is no command that reads, writes or starts anything, so the channel stays a push channel.',
  );

export const channelMessageSchema = z
  .object({
    kind: channelMessageKindSchema,
    pipelineId: pipelineIdSchema,
    payload: channelPayloadSchema,
  })
  .strict()
  .describe(
    'One message the agent pushes to a subscribed client over the WebSocket channel: what kind of message it is, which pipeline it is about, and the kind-specific payload. This file is the single source of truth for the channel envelope; the Go structs in the agent and the TypeScript types and Zod schema in @kpv/schema are generated from it and must never be hand-edited. It is a separate file from pipeline.schema.json because the envelope is transport and not part of the pipeline model: it carries a pipeline id, it is not a pipeline.',
  );
