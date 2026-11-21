// The Kafka configuration form's model (#12), without rendering anything.

import { describe, expect, it } from 'vitest';

import type { Pipeline } from '@kpv/schema';
import { pipelineZodSchema } from '@kpv/schema';

import { graphFixtures } from '../graph/fixtures.ts';
import type { DestinationDraft, SourceDraft } from './kafka-form.ts';
import {
  blockFromDraft,
  checkDraft,
  connectionChoices,
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
      connection: '',
    });
    expect(isDirty(pipeline, draftFor(pipeline, 'source'))).toBe(false);
  });

  it('holds the destination topic and producer acknowledgements', () => {
    const pipeline = everyType();
    expect(draftFor(pipeline, 'destination')).toEqual({
      role: 'destination',
      topic: 'billing.v1',
      acks: 'all',
      connection: '',
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

// #24: a Kafka block may name a connection, and the form keeps that name
// when another field is saved.
describe('a block that names a connection', () => {
  it('keeps the connection when another field is saved', () => {
    const pipeline = everyType();
    pipeline.source = { ...pipeline.source, connection: 'eu-cluster' };
    pipeline.destination = { ...pipeline.destination, connection: 'billing' };
    const source = saveDraft(
      pipeline,
      sourceDraft(pipeline, { topic: 'orders.v2' }),
    );
    expect(source.ok).toBe(true);
    if (!source.ok) return;
    expect(source.pipeline.source).toMatchObject({
      topic: 'orders.v2',
      connection: 'eu-cluster',
    });
    const destination = saveDraft(
      source.pipeline,
      destinationDraft(source.pipeline, { acks: '1' }),
    );
    expect(destination.ok).toBe(true);
    if (!destination.ok) return;
    expect(destination.pipeline.destination).toMatchObject({
      acks: '1',
      connection: 'billing',
    });
    expect(destination.pipeline.source.connection).toBe('eu-cluster');
  });

  it('saves a block with no connection without adding one', () => {
    const pipeline = everyType();
    const saved = saveDraft(
      pipeline,
      sourceDraft(pipeline, { topic: 'orders.v2' }),
    );
    expect(saved.ok && 'connection' in saved.pipeline.source).toBe(false);
  });
});

// #12: the connection select, filled from the names #24's agent lists.
describe('choosing a connection', () => {
  const named = (): Pipeline => {
    const pipeline = everyType();
    pipeline.source = { ...pipeline.source, connection: 'eu-cluster' };
    pipeline.destination = { ...pipeline.destination, connection: 'billing' };
    return pipeline;
  };

  it('starts from the connection the block names', () => {
    const pipeline = named();
    expect(draftFor(pipeline, 'source')).toMatchObject({
      connection: 'eu-cluster',
    });
    expect(draftFor(pipeline, 'destination')).toMatchObject({
      connection: 'billing',
    });
  });

  it('saves the chosen name to the block, through updateKafkaBlock', () => {
    const pipeline = everyType();
    const draft = sourceDraft(pipeline, { connection: 'eu-cluster' });
    expect(isDirty(pipeline, draft)).toBe(true);
    expect(checkDraft(pipeline, draft).valid).toBe(true);
    const source = saveDraft(pipeline, draft);
    expect(source.ok).toBe(true);
    if (!source.ok) return;
    expect(source.pipeline.source.connection).toBe('eu-cluster');
    expect('connection' in source.pipeline.destination).toBe(false);
    // The pipeline it was given is not changed.
    expect('connection' in pipeline.source).toBe(false);

    const destination = saveDraft(
      source.pipeline,
      destinationDraft(source.pipeline, { connection: 'billing' }),
    );
    expect(destination.ok).toBe(true);
    if (!destination.ok) return;
    expect(destination.pipeline.destination.connection).toBe('billing');
    expect(destination.pipeline.source.connection).toBe('eu-cluster');
    // The same check commit runs accepts it.
    expect(pipelineZodSchema.safeParse(destination.pipeline).success).toBe(
      true,
    );
  });

  it('saves no connection by leaving the key out, never as an empty string', () => {
    const pipeline = named();
    const saved = saveDraft(
      pipeline,
      sourceDraft(pipeline, { connection: '' }),
    );
    expect(saved.ok).toBe(true);
    if (!saved.ok) return;
    expect('connection' in saved.pipeline.source).toBe(false);
    expect(saved.pipeline.destination.connection).toBe('billing');
    expect(
      blockFromDraft(pipeline, destinationDraft(pipeline, { connection: '' })),
    ).not.toHaveProperty('connection');
  });

  it('puts a name the schema refuses on the connection field', () => {
    // A select offers only listed or saved names, which the schema accepts;
    // the check still reports one it does not on the field itself.
    const pipeline = everyType();
    const check = checkDraft(
      pipeline,
      destinationDraft(pipeline, { connection: '-bad name' }),
    );
    expect(check.valid).toBe(false);
    expect(Object.keys(check.fields)).toEqual(['connection']);
    expect(check.form).toEqual([]);
  });

  it('offers every listed name, in the order listed, and no connection', () => {
    const pipeline = everyType();
    const draft = draftFor(pipeline, 'source');
    expect(
      connectionChoices(pipeline, draft, ['billing', 'eu-cluster']),
    ).toEqual({
      options: [
        { name: 'billing', state: 'defined' },
        { name: 'eu-cluster', state: 'defined' },
      ],
      chosen: null,
    });
    expect(connectionChoices(pipeline, draft, [])).toEqual({
      options: [],
      chosen: null,
    });
  });

  it('marks the chosen name as defined when the agent lists it', () => {
    const pipeline = named();
    expect(
      connectionChoices(pipeline, draftFor(pipeline, 'source'), [
        'billing',
        'eu-cluster',
      ]).chosen,
    ).toEqual({ name: 'eu-cluster', state: 'defined' });
  });

  it('offers a saved name the list lacks as dangling, and keeps it on save', () => {
    const pipeline = named();
    const names = ['billing'];
    const draft = draftFor(pipeline, 'source');
    const choices = connectionChoices(pipeline, draft, names);
    expect(choices.chosen).toEqual({ name: 'eu-cluster', state: 'dangling' });
    expect(choices.options).toEqual([
      { name: 'eu-cluster', state: 'dangling' },
      { name: 'billing', state: 'defined' },
    ]);
    // Neither dirty nor invalid: nothing is dropped or rewritten.
    expect(isDirty(pipeline, draft)).toBe(false);
    expect(checkDraft(pipeline, draft).valid).toBe(true);
    const saved = saveDraft(
      pipeline,
      sourceDraft(pipeline, { topic: 'orders.v2' }),
    );
    expect(saved.ok && saved.pipeline.source.connection).toBe('eu-cluster');
  });

  it('still offers the dangling saved name after another is chosen', () => {
    const pipeline = named();
    const draft = sourceDraft(pipeline, { connection: 'billing' });
    const choices = connectionChoices(pipeline, draft, ['billing']);
    expect(choices.chosen).toEqual({ name: 'billing', state: 'defined' });
    expect(choices.options.map((choice) => choice.name)).toEqual([
      'eu-cluster',
      'billing',
    ]);
  });

  it('matches a name exactly, so one differing only in case is dangling', () => {
    const pipeline = named();
    expect(
      connectionChoices(pipeline, draftFor(pipeline, 'source'), ['EU-Cluster'])
        .chosen,
    ).toEqual({ name: 'eu-cluster', state: 'dangling' });
  });

  it('calls a name unchecked, not dangling, before any list is read', () => {
    const pipeline = named();
    const choices = connectionChoices(
      pipeline,
      draftFor(pipeline, 'destination'),
      null,
    );
    expect(choices).toEqual({
      options: [{ name: 'billing', state: 'unchecked' }],
      chosen: { name: 'billing', state: 'unchecked' },
    });
  });
});
