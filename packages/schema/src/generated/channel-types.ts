// Code generated from schema/channel.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/channel-types.ts is stale.
/**
 * What this message is. A client branches on this before it reads the payload.
 */
export type ChannelMessageKind =
  'subscribed' | 'unsubscribed' | 'error' | 'log' | 'status';
/**
 * The pipeline the message is about, and the subscription it is delivered on. Every message belongs to exactly one pipeline, so there is no broadcast that reaches a client which asked for nothing.
 */
export type PipelineID = string;

/**
 * One message the agent pushes to a subscribed client over the WebSocket channel: what kind of message it is, which pipeline it is about, and the kind-specific payload. This file is the single source of truth for the channel envelope; the Go structs in the agent and the TypeScript types and Zod schema in @kpv/schema are generated from it and must never be hand-edited. It is a separate file from pipeline.schema.json because the envelope is transport and not part of the pipeline model: it carries a pipeline id, it is not a pipeline.
 */
export interface ChannelMessage {
  kind: ChannelMessageKind;
  pipelineId: PipelineID;
  payload: ChannelPayload;
}
/**
 * The kind-specific body. Required and an object even when it is empty, so a client never has to tell an absent payload from an empty one.
 */
export interface ChannelPayload {
  [k: string]: unknown;
}
