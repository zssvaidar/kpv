// Code generated from schema/channel.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/channel-json-schema.ts is stale.

/** The authoritative JSON Schema, verbatim, for JSON Schema validators. */
export const channelJsonSchema: Readonly<Record<string, unknown>> = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  $id: 'https://github.com/zssvaidar/kpv/schema/channel.schema.json',
  title: 'ChannelMessage',
  description:
    'One message the agent pushes to a subscribed client over the WebSocket channel: what kind of message it is, which pipeline it is about, and the kind-specific payload. This file is the single source of truth for the channel envelope; the Go structs in the agent and the TypeScript types and Zod schema in @kpv/schema are generated from it and must never be hand-edited. It is a separate file from pipeline.schema.json because the envelope is transport and not part of the pipeline model: it carries a pipeline id, it is not a pipeline.',
  type: 'object',
  additionalProperties: false,
  required: ['kind', 'pipelineId', 'payload'],
  properties: {
    kind: {
      $ref: '#/$defs/channelMessageKind',
      description:
        'What this message is. A client branches on this before it reads the payload.',
    },
    pipelineId: {
      $ref: '#/$defs/pipelineId',
      description:
        'The pipeline the message is about, and the subscription it is delivered on. Every message belongs to exactly one pipeline, so there is no broadcast that reaches a client which asked for nothing.',
    },
    payload: {
      $ref: '#/$defs/channelPayload',
      description:
        'The kind-specific body. Required and an object even when it is empty, so a client never has to tell an absent payload from an empty one.',
    },
  },
  $defs: {
    pipelineId: {
      title: 'PipelineID',
      description:
        'The id of the pipeline a message is about. The pattern is the one pipeline.schema.json gives an identifier, and scripts/generate-schema.mjs fails when the two drift apart.',
      type: 'string',
      pattern: '^[A-Za-z][A-Za-z0-9_-]{0,62}$',
    },
    channelMessageKind: {
      title: 'ChannelMessageKind',
      description:
        "The kinds of message the channel carries. subscribed and unsubscribed acknowledge a client's own command, so a client knows when a subscription is in force rather than having to wait and hope. error reports that a command could not be honoured, and never quotes back what the client sent. log and status are the two streams the channel exists for; what goes in their payloads is #34 and #35.",
      type: 'string',
      enum: ['subscribed', 'unsubscribed', 'error', 'log', 'status'],
    },
    channelPayload: {
      title: 'ChannelPayload',
      description:
        "A message body, as an open object. It is deliberately unconstrained here: the channel's job is delivery, and what a log or a status message contains is defined by #34 and #35. Nothing that authorizes a client ever appears in it: the session token reaches the agent on the handshake and the code that builds a payload cannot see it.",
      type: 'object',
      additionalProperties: true,
    },
    channelCommand: {
      title: 'ChannelCommand',
      description:
        'The one thing a client may send the agent on the channel: which pipeline it wants messages for. There is no command that reads, writes or starts anything, so the channel stays a push channel.',
      type: 'object',
      additionalProperties: false,
      required: ['command', 'pipelineId'],
      properties: {
        command: {
          $ref: '#/$defs/channelCommandName',
          description: 'Whether the client is subscribing or unsubscribing.',
        },
        pipelineId: {
          $ref: '#/$defs/pipelineId',
          description: 'The pipeline to subscribe to or unsubscribe from.',
        },
      },
    },
    channelCommandName: {
      title: 'ChannelCommandName',
      description: 'The commands a client may send.',
      type: 'string',
      enum: ['subscribe', 'unsubscribe'],
    },
  },
};
