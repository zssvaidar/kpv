// The plain-language summaries of the filter, transform and validate steps
// (#13): several configurations of each, the empty and not-yet-configured
// ones, and a transform that drops every incoming field.

import { describe, expect, it } from 'vitest';

import type { FilterStep, TransformStep, ValidateStep } from '@kpv/schema';

import { newStep, newStepConfiguration } from '../graph/edit.ts';
import type { ConfigurableStep } from './step-form.ts';
import { summarizeStep } from './step-summary.ts';
import summarySource from './step-summary.ts?raw';

const filter = (configuration: object): FilterStep =>
  ({ id: 'f', type: 'filter', configuration }) as FilterStep;
const transform = (configuration: object): TransformStep =>
  ({ id: 't', type: 'transform', configuration }) as TransformStep;
const validate = (configuration: object): ValidateStep =>
  ({ id: 'v', type: 'validate', configuration }) as ValidateStep;

const text = (step: ConfigurableStep) =>
  summarizeStep(step).sentences.join(' ');

describe('a filter', () => {
  it('keeps matching messages, by default and when said so', () => {
    for (const keepOnMatch of [undefined, true]) {
      const summary = summarizeStep(
        filter({ expression: "status == 'paid'", keepOnMatch }),
      );
      expect(summary.state).toBe('configured');
      expect(summary.sentences).toEqual([
        "Keeps a message only when “status == 'paid'” holds; every other message is dropped.",
      ]);
    }
  });

  it('drops matching messages', () => {
    expect(text(filter({ expression: 'total < 0', keepOnMatch: false }))).toBe(
      'Drops every message for which “total < 0” holds; every other message continues.',
    );
  });

  it('says it is not configured when no condition is set', () => {
    for (const expression of ['', '   ']) {
      const summary = summarizeStep(filter({ expression }));
      expect(summary.state).toBe('empty');
      expect(summary.sentences[0]).toMatch(/^Not configured: no condition/);
      expect(text(filter({ expression }))).not.toMatch(/Keeps|Drops/);
    }
  });

  it('says it is not configured yet while it holds the placeholder it was inserted with', () => {
    const summary = summarizeStep(newStep('filter', 'f') as FilterStep);
    expect(summary.state).toBe('placeholder');
    expect(summary.sentences[0]).toMatch(/^Not configured yet/);
    expect(summary.sentences[1]).toBe('As it stands, it keeps every message.');
    // Once someone changes it, it is described as configured.
    expect(
      summarizeStep(filter({ expression: 'true', keepOnMatch: false })).state,
    ).toBe('configured');
  });
});

describe('a transform', () => {
  it('merges its mappings into the message by default', () => {
    const summary = summarizeStep(
      transform({
        mappings: [{ targetField: 'amount', expression: 'order.total' }],
      }),
    );
    expect(summary.state).toBe('configured');
    expect(summary.dropsEveryField).toBe(false);
    expect(summary.sentences).toEqual([
      'Sets 1 field: “amount” from “order.total”.',
      'Every other field of the message passes through unchanged.',
    ]);
  });

  it('says plainly that it drops every incoming field when only mapped fields are emitted', () => {
    const summary = summarizeStep(
      transform({
        mappings: [
          { targetField: 'amount', expression: 'order.total' },
          { targetField: 'customer', expression: 'order.customer.id' },
        ],
        dropUnmappedFields: true,
      }),
    );
    expect(summary.dropsEveryField).toBe(true);
    expect(summary.sentences).toEqual([
      'Sets 2 fields: “amount” from “order.total”; “customer” from “order.customer.id”.',
      'Drops every incoming field: the output holds only the 2 mapped fields, “amount”, “customer”, and nothing else from the message.',
    ]);
    const one = summarizeStep(
      transform({
        mappings: [{ targetField: 'id', expression: 'order.id' }],
        dropUnmappedFields: true,
      }),
    );
    expect(one.sentences[1]).toBe(
      'Drops every incoming field: the output holds only the one mapped field, “id”, and nothing else from the message.',
    );
  });

  it('leaves unfinished mappings out and says so', () => {
    expect(
      text(
        transform({
          mappings: [
            { targetField: 'amount', expression: 'order.total' },
            { targetField: 'x', expression: '' },
          ],
          dropUnmappedFields: false,
        }),
      ),
    ).toBe(
      'Sets 1 field: “amount” from “order.total”. Every other field of the message passes through unchanged. 1 mapping is not finished and left out of this summary.',
    );
  });

  it('says it is not configured when no mapping is set', () => {
    const summary = summarizeStep(transform({ mappings: [] }));
    expect(summary.state).toBe('empty');
    expect(summary.sentences).toEqual([
      'Not configured: no field mapping is set.',
    ]);

    const dropping = summarizeStep(
      transform({
        mappings: [{ targetField: '', expression: '' }],
        dropUnmappedFields: true,
      }),
    );
    expect(dropping.state).toBe('empty');
    expect(dropping.dropsEveryField).toBe(true);
    expect(dropping.sentences.join(' ')).toContain(
      'the output would have no fields at all',
    );
  });

  it('says it is not configured yet while it holds the placeholder', () => {
    const summary = summarizeStep(newStep('transform', 't') as TransformStep);
    expect(summary.state).toBe('placeholder');
    expect(summary.sentences[0]).toMatch(/^Not configured yet/);
    expect(summary.sentences[0]).toContain('“value” from “value”');
  });
});

describe('a validate step', () => {
  it('describes each rule and the dead-letter topic', () => {
    const summary = summarizeStep(
      validate({
        rules: [{ field: 'amount', required: true, fieldType: 'number' }],
        onInvalid: 'deadLetter',
        deadLetterTopic: 'billing.invalid.v1',
      }),
    );
    expect(summary.state).toBe('configured');
    expect(summary.sentences).toEqual([
      'Checks 1 rule: “amount” must be present and be a number.',
      'A message that breaks a rule is sent to the dead-letter topic “billing.invalid.v1”.',
    ]);
  });

  it('describes every combination of presence and type', () => {
    expect(
      text(
        validate({
          rules: [
            { field: 'id' },
            { field: 'tags', required: false, fieldType: 'array' },
            { field: 'note', required: false },
            { field: 'meta', fieldType: 'object' },
          ],
          onInvalid: 'drop',
        }),
      ),
    ).toBe(
      'Checks 4 rules: “id” must be present; “tags”, when present, must be an array; “note” is optional with no type, so its rule checks nothing; “meta” must be present and be an object. A message that breaks a rule is dropped.',
    );
  });

  it('says what each action does, and when a dead-letter topic is missing or unused', () => {
    const rules = [{ field: 'id' }];
    expect(text(validate({ rules, onInvalid: 'fail' }))).toContain(
      'A message that breaks a rule fails the pipeline, which stops.',
    );
    expect(text(validate({ rules, onInvalid: 'deadLetter' }))).toContain(
      'meant for a dead-letter topic, but none is named.',
    );
    expect(
      text(validate({ rules, onInvalid: 'drop', deadLetterTopic: 'dlq' })),
    ).toContain(
      'The dead-letter topic “dlq” is set but not used with this action.',
    );
  });

  it('says it is not configured when no rule is set', () => {
    const summary = summarizeStep(validate({ rules: [], onInvalid: 'drop' }));
    expect(summary.state).toBe('empty');
    expect(summary.sentences).toEqual([
      'Not configured: no rule is set, so nothing is checked.',
    ]);
    expect(
      summarizeStep(validate({ rules: [{ field: '' }], onInvalid: 'drop' }))
        .sentences[1],
    ).toBe('1 rule has no field yet and is left out of this summary.');
  });

  it('says it is not configured yet while it holds the placeholder', () => {
    const summary = summarizeStep(newStep('validate', 'v') as ValidateStep);
    expect(summary.state).toBe('placeholder');
    expect(summary.sentences[0]).toMatch(/^Not configured yet/);
    expect(summary.sentences[1]).toBe(
      'As it stands, a message without “value” is dropped.',
    );
  });
});

describe('summaries', () => {
  it('are derived from the configuration and never change it', () => {
    const step = transform({
      mappings: [{ targetField: 'a', expression: 'b' }],
      dropUnmappedFields: true,
    });
    const before = structuredClone(step);
    summarizeStep(step);
    expect(step).toEqual(before);
    expect(newStepConfiguration.transform.mappings).toHaveLength(1);
  });

  it('are not stored: no summary text is written into a step', () => {
    expect(summarySource).not.toMatch(/configuration\.\w+\s*=[^=]/);
  });
});
