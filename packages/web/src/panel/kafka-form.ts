// The Kafka source and destination configuration panel's model (#12): what
// the form holds while someone types, how that becomes a block, and what the
// schema says about it.
//
// The form never decides on its own what is valid. Every check is the
// generated Zod schema in @kpv/schema, run on the whole pipeline with the
// typed block in place, by the same function commit runs when it saves
// (graph/edit.ts). What is here is only the mapping between text fields and
// the block, and the wording of what the schema reports.
//
// Nothing here imports React, so all of it is tested without rendering.

import type {
  KafkaDestinationBlock,
  KafkaSourceBlock,
  Pipeline,
} from '@kpv/schema';
import {
  kafkaDestinationBlockSchema,
  kafkaSourceBlockSchema,
} from '@kpv/schema';

import type { EditResult, SchemaIssue } from '../graph/edit.ts';
import {
  schemaIssues,
  updateKafkaBlock,
  withKafkaBlock,
} from '../graph/edit.ts';

/** Which Kafka block a form edits. */
export type KafkaRole = 'source' | 'destination';

/**
 * What the source form holds: each field as typed. An empty string is a
 * field left blank, which leaves the property out of the block rather than
 * saving it empty, so the schema's default (or, for the consumer group, the
 * agent's generated per-pipeline value) applies.
 */
export interface SourceDraft {
  role: 'source';
  topic: string;
  consumerGroupId: string;
  autoOffsetReset: string;
}

/** What the destination form holds, the same way. */
export interface DestinationDraft {
  role: 'destination';
  topic: string;
  acks: string;
}

export type KafkaDraft = SourceDraft | DestinationDraft;

/** The fields a form has, by role, in the order it shows them. */
export const kafkaFields = {
  source: ['topic', 'consumerGroupId', 'autoOffsetReset'],
  destination: ['topic', 'acks'],
} as const;

export type SourceField = (typeof kafkaFields.source)[number];
export type DestinationField = (typeof kafkaFields.destination)[number];
export type KafkaField = SourceField | DestinationField;

// The choices a select offers and what it means to leave one unset, read
// from the generated schema rather than restated here.
const offsetReset = kafkaSourceBlockSchema.shape.autoOffsetReset;
const acks = kafkaDestinationBlockSchema.shape.acks;

/** The values the schema allows for each choice, and its default. */
export const kafkaChoices = {
  autoOffsetReset: {
    options: offsetReset.unwrap().options as readonly string[],
    defaultValue: offsetReset.def.defaultValue as string,
  },
  acks: {
    options: acks.unwrap().options as readonly string[],
    defaultValue: acks.def.defaultValue as string,
  },
} as const;

/** The schema's own description of a field, for the hint under it. */
export const fieldDescription = {
  topic: kafkaSourceBlockSchema.shape.topic.description ?? '',
  consumerGroupId:
    kafkaSourceBlockSchema.shape.consumerGroupId.unwrap().description ?? '',
  autoOffsetReset: offsetReset.unwrap().description ?? '',
  acks: acks.unwrap().description ?? '',
} satisfies Record<KafkaField, string>;

/** The block a role names in this pipeline. */
export const kafkaBlock = (
  pipeline: Pipeline,
  role: KafkaRole,
): KafkaSourceBlock | KafkaDestinationBlock =>
  role === 'source' ? pipeline.source : pipeline.destination;

/** The form as it starts for this block: what the pipeline holds now. */
export function draftFor(pipeline: Pipeline, role: KafkaRole): KafkaDraft {
  if (role === 'source') {
    const block = pipeline.source;
    return {
      role,
      topic: block.topic,
      consumerGroupId: block.consumerGroupId ?? '',
      autoOffsetReset: block.autoOffsetReset ?? '',
    };
  }
  const block = pipeline.destination;
  return { role, topic: block.topic, acks: block.acks ?? '' };
}

/** Whether the form holds anything the pipeline does not. */
export function isDirty(pipeline: Pipeline, draft: KafkaDraft): boolean {
  const saved = draftFor(pipeline, draft.role) as unknown as Record<
    string,
    string
  >;
  const typed = draft as unknown as Record<string, string>;
  return kafkaFields[draft.role].some((field) => saved[field] !== typed[field]);
}

/** Sets a property to a typed value, or leaves it out when it is blank. */
const setOrOmit = <T extends object>(block: T, key: string, value: string) => {
  const record = block as Record<string, unknown>;
  if (value === '') delete record[key];
  else record[key] = value;
};

/**
 * The block the form describes: the pipeline's current block with the typed
 * fields put in, so whatever the form does not show (its id, its message
 * format, preserveKey) is kept as it is. Not checked: check does that.
 */
export function blockFromDraft(
  pipeline: Pipeline,
  draft: KafkaDraft,
): KafkaSourceBlock | KafkaDestinationBlock {
  if (draft.role === 'source') {
    const block: KafkaSourceBlock = { ...pipeline.source, topic: draft.topic };
    setOrOmit(block, 'consumerGroupId', draft.consumerGroupId);
    setOrOmit(block, 'autoOffsetReset', draft.autoOffsetReset);
    return block;
  }
  const block: KafkaDestinationBlock = {
    ...pipeline.destination,
    topic: draft.topic,
  };
  setOrOmit(block, 'acks', draft.acks);
  return block;
}

/** What the schema says about the form, worded for the field it is about. */
export interface DraftCheck {
  /** The first problem with each field that has one. */
  fields: Partial<Record<KafkaField, string>>;
  /** Problems the schema found anywhere else, which no field can show. */
  form: string[];
  /** Whether the form can be saved as it is. */
  valid: boolean;
}

/** A schema issue in words for the person typing, from what Zod reported. */
export function describeIssue(issue: SchemaIssue): string {
  const { detail } = issue;
  switch (issue.code) {
    case 'too_small':
      return detail.minimum === 1
        ? 'Required.'
        : `Must be at least ${String(detail.minimum)} characters.`;
    case 'too_big':
      return `Must be at most ${String(detail.maximum)} characters.`;
    case 'invalid_format':
      return typeof detail.pattern === 'string'
        ? `Contains a character this field does not allow (allowed: ${detail.pattern}).`
        : issue.message;
    case 'invalid_value':
      return Array.isArray(detail.values)
        ? `Must be one of ${detail.values.map(String).join(', ')}.`
        : issue.message;
    default:
      return issue.message;
  }
}

/**
 * Checks the form against the schema, on the whole pipeline with the typed
 * block in place, which is exactly what saving it would commit.
 */
export function checkDraft(pipeline: Pipeline, draft: KafkaDraft): DraftCheck {
  const issues = schemaIssues(
    withKafkaBlock(pipeline, blockFromDraft(pipeline, draft)),
  );
  const own: readonly string[] = kafkaFields[draft.role];
  const fields: Partial<Record<KafkaField, string>> = {};
  const form: string[] = [];
  for (const issue of issues) {
    const [role, field] = issue.path;
    if (
      role === draft.role &&
      typeof field === 'string' &&
      own.includes(field)
    ) {
      const key = field as KafkaField;
      fields[key] ??= describeIssue(issue);
    } else {
      const where = issue.path.map(String).join('.');
      form.push(`${where === '' ? '' : `${where}: `}${describeIssue(issue)}`);
    }
  }
  return { fields, form, valid: issues.length === 0 };
}

/**
 * Saves the form: the block through updateKafkaBlock, the same validated
 * path every graph edit takes. A form the schema refuses comes back as a
 * rejection carrying the pipeline unchanged.
 */
export function saveDraft(pipeline: Pipeline, draft: KafkaDraft): EditResult {
  return updateKafkaBlock(pipeline, blockFromDraft(pipeline, draft));
}
