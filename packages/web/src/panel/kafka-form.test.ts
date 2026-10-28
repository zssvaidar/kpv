// The Kafka configuration form's model (#12), without rendering anything.

import { describe, expect, it } from 'vitest';

import type { Pipeline } from '@kpv/schema';
import { pipelineZodSchema } from '@kpv/schema';

import { graphFixtures } from '../graph/fixtures.ts';
import type { DestinationDraft, SourceDraft } from './kafka-form.ts';
import {
  blockFromDraft,
  checkDraft,
  draftFor,
  isDirty,
  kafkaChoices,
  saveDraft,
} from './kafka-form.ts';
import formSource from './kafka-form.ts?raw';
import componentSource from './KafkaBlockForm.tsx?raw';

const everyType = (): Pipeline =>
  structuredClone(graphFixtures['every-block-type']);

const sourceDraft = (
  pipeline: Pipeline,
  change: Partial<SourceDraft>,
): SourceDraft => ({
  ...(draftFor(pipeline, 'source') as SourceDraft),
  ...change,
});

const destinationDraft = (
  pipeline: Pipeline,
  change: Partial<DestinationDraft>,
): DestinationDraft => ({
  ...(draftFor(pipeline, 'destination') as DestinationDraft),
  ...change,
});

describe('the form as it starts', () => {
  it('holds the source topic, consumer group and offset reset', () => {
    const pipeline = everyType();
    expect(draftFor(pipeline, 'source')).toEqual({
      role: 'source',
      topic: 'orders.v1',
      consumerGroupId: 'kpv-orders-enriched',
      autoOffsetReset: 'earliest',
    });
    expect(isDirty(pipeline, draftFor(pipeline, 'source'))).toBe(false);
  });

  it('holds the destination topic and producer acknowledgements', () => {
    const pipeline = everyType();
    expect(draftFor(pipeline, 'destination')).toEqual({
      role: 'destination',
      topic: 'billing.v1',
      acks: 'all',
    });
  });

  it('shows a property the block leaves out as blank', () => {
    const pipeline = graphFixtures['zero-processing-steps'];
    const draft = draftFor(pipeline, 'source') as SourceDraft;
    if (pipeline.source.consumerGroupId === undefined) {
      expect(draft.consumerGroupId).toBe('');
    }
  });

  it('offers the choices and defaults the schema has, not its own', () => {
    expect(kafkaChoices.autoOffsetReset).toEqual({
      options: ['earliest', 'latest'],
      defaultValue: 'latest',
    });
    expect(kafkaChoices.acks).toEqual({
      options: ['0', '1', 'all'],
      defaultValue: 'all',
    });
  });
});

describe('validation as the user types', () => {
  it('is clean for the block as saved', () => {
    const pipeline = everyType();
    expect(checkDraft(pipeline, draftFor(pipeline, 'source'))).toEqual({
      fields: {},
      form: [],
      valid: true,
    });
  });

  it('puts each schema error on the field it is about', () => {
    const pipeline = everyType();
    const empty = checkDraft(pipeline, sourceDraft(pipeline, { topic: '' }));
    expect(empty.valid).toBe(false);
    expect(Object.keys(empty.fields)).toEqual(['topic']);
    expect(empty.fields.topic).toBeTruthy();

    const spaced = checkDraft(
      pipeline,
      sourceDraft(pipeline, { topic: 'orders v1' }),
    );
    expect(spaced.fields.topic).toMatch(/does not allow/);

    const long = checkDraft(
      pipeline,
      sourceDraft(pipeline, { consumerGroupId: 'g'.repeat(256) }),
    );
    expect(long.fields).toEqual({
      consumerGroupId: 'Must be at most 255 characters.',
    });

    const acks = checkDraft(
      pipeline,
      destinationDraft(pipeline, { topic: 'bad/topic', acks: '2' }),
    );
    expect(Object.keys(acks.fields).sort()).toEqual(['acks', 'topic']);
    expect(acks.fields.acks).toBe('Must be one of 0, 1, all.');
  });

  it('agrees with the schema on every input it is given', () => {
    const pipeline = everyType();
    const topics = [
      '',
      'a',
      'orders.v1',
      'a b',
      'x'.repeat(249),
      'x'.repeat(250),
      'ü',
    ];
    for (const topic of topics) {
      const draft = sourceDraft(pipeline, { topic });
      const schemaSays = pipelineZodSchema.safeParse({
        ...pipeline,
        source: { ...pipeline.source, topic },
      }).success;
      expect(checkDraft(pipeline, draft).valid).toBe(schemaSays);
    }
  });
});

describe('a consumer group left blank', () => {
  it('is left out of the block, not saved as an empty string', () => {
    const pipeline = everyType();
    const draft = sourceDraft(pipeline, { consumerGroupId: '' });
    const block = blockFromDraft(pipeline, draft);
    expect('consumerGroupId' in block).toBe(false);
    expect(checkDraft(pipeline, draft).valid).toBe(true);

    const saved = saveDraft(pipeline, draft);
    expect(saved.ok).toBe(true);
    expect('consumerGroupId' in saved.pipeline.source).toBe(false);
    expect(JSON.stringify(saved.pipeline)).not.toContain('"consumerGroupId"');
  });

  it('as does a choice left at its default', () => {
    const pipeline = everyType();
    const source = saveDraft(
      pipeline,
      sourceDraft(pipeline, { autoOffsetReset: '' }),
    );
    expect('autoOffsetReset' in source.pipeline.source).toBe(false);
    const destination = saveDraft(
      pipeline,
      destinationDraft(pipeline, { acks: '' }),
    );
    expect('acks' in destination.pipeline.destination).toBe(false);
  });
});

describe('saving', () => {
  it('writes the typed fields and keeps what the form does not show', () => {
    const pipeline = everyType();
    const result = saveDraft(
      pipeline,
      sourceDraft(pipeline, {
        topic: 'orders.v2',
        consumerGroupId: 'kpv-new',
        autoOffsetReset: 'latest',
      }),
    );
    expect(result.ok).toBe(true);
    expect(result.pipeline.source).toEqual({
      ...pipeline.source,
      topic: 'orders.v2',
      consumerGroupId: 'kpv-new',
      autoOffsetReset: 'latest',
    });
    expect(result.pipeline.destination).toBe(pipeline.destination);

    const destination = saveDraft(
      pipeline,
      destinationDraft(pipeline, { topic: 'billing.v2', acks: '0' }),
    );
    expect(destination.pipeline.destination).toEqual({
      ...pipeline.destination,
      topic: 'billing.v2',
      acks: '0',
    });
  });

  it('refuses an invalid form and changes nothing', () => {
    const pipeline = everyType();
    const draft = sourceDraft(pipeline, { topic: 'no spaces allowed' });
    const result = saveDraft(pipeline, draft);
    expect(result.ok).toBe(false);
    expect(result.pipeline).toBe(pipeline);
    // What was typed is the caller's, untouched by the refusal.
    expect(draft.topic).toBe('no spaces allowed');
    expect(isDirty(pipeline, draft)).toBe(true);
  });

  it('a saved form matches the pipeline it made', () => {
    const pipeline = everyType();
    const draft = sourceDraft(pipeline, { topic: 'orders.v3' });
    const result = saveDraft(pipeline, draft);
    expect(isDirty(result.pipeline, draft)).toBe(false);
  });
});

describe('the form modules', () => {
  it('state no validation rule of their own: every check is the schema', () => {
    for (const source of [formSource, componentSource]) {
      expect(source).not.toMatch(/RegExp|\.test\(|\.match\(/);
      expect(source).not.toMatch(/\b(249|255)\b/);
      expect(source).not.toMatch(/\.safeParse\(|\.parse\(\s*[a-z]/);
    }
    expect(formSource).toContain('schemaIssues');
  });

  it('render no credential input', () => {
    expect(componentSource).not.toMatch(
      /type="password"|password|sasl|secret|bootstrap/i,
    );
  });
});
