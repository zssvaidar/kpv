// The filter, transform and validate forms' model (#13), without rendering
// anything.

import { describe, expect, it } from 'vitest';

import type { Pipeline } from '@kpv/schema';
import { pipelineZodSchema } from '@kpv/schema';

import { insertStep } from '../graph/edit.ts';
import { graphFixtures } from '../graph/fixtures.ts';
import type {
  FilterDraft,
  StepDraft,
  TransformDraft,
  ValidateDraft,
} from './step-form.ts';
import {
  checkStepDraft,
  draftFor,
  isDirty,
  saveStepDraft,
  stepChoices,
  stepFor,
  stepFromDraft,
} from './step-form.ts';
import formSource from './step-form.ts?raw';
import componentSource from './StepBlockForm.tsx?raw';

const everyType = (): Pipeline =>
  structuredClone(graphFixtures['every-block-type']);

/** The form as it starts for a step of the fixture, with some fields typed. */
function typed(
  pipeline: Pipeline,
  id: 'only-paid',
  change: Partial<FilterDraft>,
): FilterDraft;
function typed(
  pipeline: Pipeline,
  id: 'to-billing-shape',
  change: Partial<TransformDraft>,
): TransformDraft;
function typed(
  pipeline: Pipeline,
  id: 'require-amount',
  change: Partial<ValidateDraft>,
): ValidateDraft;
function typed(
  pipeline: Pipeline,
  id: string,
  change: Partial<StepDraft>,
): StepDraft {
  const step = stepFor(pipeline, id);
  if (step === null) throw new Error(`no step ${id}`);
  return { ...draftFor(step), ...change } as StepDraft;
}

describe('the form as it starts', () => {
  it('holds each configurable step of the fixture as it is', () => {
    const pipeline = everyType();
    const filter = stepFor(pipeline, 'only-paid');
    const transform = stepFor(pipeline, 'to-billing-shape');
    const validate = stepFor(pipeline, 'require-amount');
    if (filter === null || transform === null || validate === null) {
      throw new Error('fixture changed');
    }
    expect(draftFor(filter)).toEqual({
      type: 'filter',
      id: 'only-paid',
      expression: "status == 'paid'",
      keepOnMatch: '',
    });
    expect(draftFor(transform)).toEqual({
      type: 'transform',
      id: 'to-billing-shape',
      mappings: [
        { targetField: 'amount', expression: 'order.total' },
        { targetField: 'customer', expression: 'order.customer.id' },
      ],
      dropUnmappedFields: 'true',
    });
    expect(draftFor(validate)).toEqual({
      type: 'validate',
      id: 'require-amount',
      rules: [{ field: 'amount', required: 'true', fieldType: 'number' }],
      onInvalid: 'deadLetter',
      deadLetterTopic: 'billing.invalid.v1',
    });
    for (const step of [filter, transform, validate]) {
      expect(isDirty(pipeline, draftFor(step))).toBe(false);
      // Unchanged, the step the form describes is the step as saved.
      expect(stepFromDraft(pipeline, draftFor(step))).toEqual(step);
    }
  });

  it('is not offered for custom code, which is #14', () => {
    expect(stepFor(everyType(), 'apply-discounts')).toBeNull();
    expect(stepFor(everyType(), 'nope')).toBeNull();
  });

  it('offers the choices and defaults the schema has, not its own', () => {
    expect(stepChoices.onInvalid.options).toEqual([
      'drop',
      'deadLetter',
      'fail',
    ]);
    expect(stepChoices.onInvalid.startsWith).toBe('drop');
    expect(stepChoices.onInvalid).not.toHaveProperty('defaultValue');
    expect(stepChoices.fieldType.options).toEqual([
      'string',
      'number',
      'boolean',
      'object',
      'array',
    ]);
    expect(stepChoices.keepOnMatch.defaultValue).toBe(true);
    expect(stepChoices.dropUnmappedFields.defaultValue).toBe(false);
    expect(stepChoices.required.defaultValue).toBe(true);
  });

  it('the onInvalid a form starts with is what a newly inserted validate step holds', () => {
    const inserted = insertStep(
      graphFixtures['zero-processing-steps'],
      { before: { role: 'source' }, after: { role: 'destination' } },
      'validate',
    );
    const step = inserted.pipeline.processingSteps?.[0];
    expect(step?.type).toBe('validate');
    if (step?.type === 'validate') {
      expect(step.configuration.onInvalid).toBe(
        stepChoices.onInvalid.startsWith,
      );
    }
  });
});

describe('validation as the user types', () => {
  it('puts each schema error on the field it is about', () => {
    const pipeline = everyType();
    const filter = checkStepDraft(
      pipeline,
      typed(pipeline, 'only-paid', { expression: '' }),
    );
    expect(filter).toEqual({
      fields: { expression: 'Required.' },
      form: [],
      valid: false,
    });

    const transform = checkStepDraft(
      pipeline,
      typed(pipeline, 'to-billing-shape', {
        mappings: [
          { targetField: 'amount', expression: 'order.total' },
          { targetField: '', expression: '' },
        ],
      }),
    );
    expect(transform.fields).toEqual({
      'mappings.1.targetField': 'Required.',
      'mappings.1.expression': 'Required.',
    });

    const none = checkStepDraft(
      pipeline,
      typed(pipeline, 'to-billing-shape', { mappings: [] }),
    );
    expect(none.fields).toEqual({ mappings: 'Needs at least 1 mapping.' });

    const validate = checkStepDraft(
      pipeline,
      typed(pipeline, 'require-amount', {
        rules: [{ field: '', required: '', fieldType: '' }],
        deadLetterTopic: 'not a topic',
      }),
    );
    expect(Object.keys(validate.fields).sort()).toEqual([
      'deadLetterTopic',
      'rules.0.field',
    ]);
    expect(validate.fields.deadLetterTopic).toMatch(/does not allow/);

    const noRules = checkStepDraft(
      pipeline,
      typed(pipeline, 'require-amount', { rules: [] }),
    );
    expect(noRules.fields).toEqual({ rules: 'Needs at least 1 rule.' });

    const action = checkStepDraft(
      pipeline,
      typed(pipeline, 'require-amount', { onInvalid: 'retry' }),
    );
    expect(action.fields).toEqual({
      onInvalid: 'Must be one of drop, deadLetter, fail.',
    });
  });

  it('agrees with the schema on every input it is given', () => {
    const pipeline = everyType();
    const drafts: StepDraft[] = [
      typed(pipeline, 'only-paid', { expression: '' }),
      typed(pipeline, 'only-paid', { expression: ' ' }),
      typed(pipeline, 'only-paid', { keepOnMatch: 'false' }),
      typed(pipeline, 'only-paid', { keepOnMatch: 'maybe' }),
      typed(pipeline, 'to-billing-shape', { mappings: [] }),
      typed(pipeline, 'to-billing-shape', { dropUnmappedFields: '' }),
      typed(pipeline, 'require-amount', { onInvalid: '' }),
      typed(pipeline, 'require-amount', { onInvalid: 'fail' }),
      typed(pipeline, 'require-amount', { deadLetterTopic: '' }),
      typed(pipeline, 'require-amount', { deadLetterTopic: 'x'.repeat(250) }),
      typed(pipeline, 'require-amount', {
        rules: [{ field: 'a', required: 'false', fieldType: 'date' }],
      }),
    ];
    for (const draft of drafts) {
      const steps = (pipeline.processingSteps ?? []).map((step) =>
        step.id === draft.id ? stepFromDraft(pipeline, draft) : step,
      );
      const schemaSays = pipelineZodSchema.safeParse({
        ...pipeline,
        processingSteps: steps,
      }).success;
      expect(checkStepDraft(pipeline, draft).valid).toBe(schemaSays);
    }
  });

  it('says so when the step is no longer in the pipeline', () => {
    const pipeline = everyType();
    const check = checkStepDraft(pipeline, {
      type: 'filter',
      id: 'gone',
      expression: 'true',
      keepOnMatch: '',
    });
    expect(check.valid).toBe(false);
    expect(check.form[0]).toContain('"gone"');
  });
});

describe('a choice left at its default', () => {
  it('is left out of the step, not saved', () => {
    const pipeline = everyType();
    const transform = saveStepDraft(
      pipeline,
      typed(pipeline, 'to-billing-shape', { dropUnmappedFields: '' }),
    );
    expect(transform.ok).toBe(true);
    const saved = transform.pipeline.processingSteps?.[1];
    expect(saved?.configuration).not.toHaveProperty('dropUnmappedFields');

    const validate = saveStepDraft(
      pipeline,
      typed(pipeline, 'require-amount', {
        rules: [{ field: 'amount', required: '', fieldType: '' }],
        onInvalid: 'drop',
        deadLetterTopic: '',
      }),
    );
    expect(validate.ok).toBe(true);
    expect(validate.pipeline.processingSteps?.[2]?.configuration).toEqual({
      rules: [{ field: 'amount' }],
      onInvalid: 'drop',
    });
  });
});

describe('saving', () => {
  it('writes the typed configuration and keeps what the form does not show', () => {
    const pipeline = everyType();
    const steps = pipeline.processingSteps ?? [];
    // A custom code reference on a filter is not shown, and is kept.
    const withReference: Pipeline = {
      ...pipeline,
      processingSteps: steps.map((step) =>
        step.id === 'only-paid'
          ? { ...step, customCode: { path: 'src/paid.go' } }
          : step,
      ),
    };
    const result = saveStepDraft(
      withReference,
      typed(withReference, 'only-paid', {
        expression: 'total > 100',
        keepOnMatch: 'false',
      }),
    );
    expect(result.ok).toBe(true);
    expect(result.pipeline.processingSteps?.[0]).toEqual({
      id: 'only-paid',
      type: 'filter',
      configuration: { expression: 'total > 100', keepOnMatch: false },
      customCode: { path: 'src/paid.go' },
    });
    expect(result.pipeline.processingSteps?.[1]).toBe(
      withReference.processingSteps?.[1],
    );

    const transform = saveStepDraft(
      pipeline,
      typed(pipeline, 'to-billing-shape', {
        mappings: [{ targetField: 'id', expression: 'order.id' }],
        dropUnmappedFields: 'false',
      }),
    );
    expect(transform.pipeline.processingSteps?.[1]?.configuration).toEqual({
      mappings: [{ targetField: 'id', expression: 'order.id' }],
      dropUnmappedFields: false,
    });
  });

  it('refuses an invalid form and changes nothing', () => {
    const pipeline = everyType();
    const draft = typed(pipeline, 'to-billing-shape', { mappings: [] });
    const result = saveStepDraft(pipeline, draft);
    expect(result.ok).toBe(false);
    expect(result.pipeline).toBe(pipeline);
    expect(draft.mappings).toEqual([]);
    expect(isDirty(pipeline, draft)).toBe(true);
  });

  it('a saved form matches the pipeline it made', () => {
    const pipeline = everyType();
    const draft = typed(pipeline, 'require-amount', { onInvalid: 'fail' });
    const result = saveStepDraft(pipeline, draft);
    expect(result.ok).toBe(true);
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

  it('offer no action the schema does not, and invent no configuration shape', () => {
    // The actions are read from the schema, never listed here.
    expect(componentSource).not.toMatch(/'drop',\s*'deadLetter'/);
    expect(formSource).not.toMatch(/'drop',\s*'deadLetter'/);
  });
});
