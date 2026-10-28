// The filter, transform and validate forms rendered (#13): their fields, the
// summary above them, an error next to the field it is about, and the
// invalid-message action offering only what the schema allows.

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import type { Pipeline } from '@kpv/schema';

import { insertStep } from '../graph/edit.ts';
import { graphFixtures } from '../graph/fixtures.ts';
import type { StepDraft } from './step-form.ts';
import { draftFor, stepChoices, stepFor } from './step-form.ts';
import { StepBlockForm } from './StepBlockForm.tsx';

const everyType = (): Pipeline => graphFixtures['every-block-type'];

const render = (
  stepId: string,
  draft: StepDraft | null = null,
  pipeline: Pipeline = everyType(),
) =>
  renderToStaticMarkup(
    <StepBlockForm
      pipeline={pipeline}
      stepId={stepId}
      draft={draft}
      onDraft={() => undefined}
      onSave={() => undefined}
    />,
  );

const typed = (stepId: string, change: object): StepDraft => {
  const step = stepFor(everyType(), stepId);
  if (step === null) throw new Error(`no step ${stepId}`);
  return { ...draftFor(step), ...change };
};

/** The summary section's markup. */
const summaryOf = (html: string): string => {
  const match = /<section class="step-summary"[\s\S]*?<\/section>/.exec(html);
  expect(match).not.toBeNull();
  return match?.[0] ?? '';
};

/** The markup of one field, from its wrapper to the next one. */
const field = (html: string, name: string): string => {
  const start = html.indexOf(`data-field="${name}"`);
  expect(start).toBeGreaterThan(-1);
  const end = html.indexOf('data-field=', start + 1);
  return html.slice(start, end === -1 ? undefined : end);
};

// What a person reads: React escapes quotes in text.
const text = (html: string) =>
  html
    .replace(/<[^>]*>/g, ' ')
    .replace(/&quot;/g, '"')
    .replace(/&#x27;/g, "'")
    .replace(/\s+/g, ' ');

describe('the filter form', () => {
  it('shows the condition, what happens to a match, and the summary', () => {
    const html = render('only-paid');
    expect(html).toContain('id="step-only-paid-expression"');
    expect(html).toContain('value="status == &#x27;paid&#x27;"');
    expect(html).toMatch(/<select id="step-only-paid-keepOnMatch"/);
    expect(html).toContain('Default: keep them');
    expect(html).toContain('data-summary-state="configured"');
    expect(text(summaryOf(html))).toContain(
      "What this step does Keeps a message only when “status == 'paid'” holds",
    );
  });

  it('summarizes what was typed, before it is saved', () => {
    const html = render(
      'only-paid',
      typed('only-paid', { keepOnMatch: 'false' }),
    );
    expect(text(summaryOf(html))).toContain(
      "What this step will do once saved Drops every message for which “status == 'paid'” holds",
    );
    expect(html).toMatch(/<button type="submit">Save/);
  });

  it('shows an inline error and does not save a condition the schema rejects', () => {
    const html = render('only-paid', typed('only-paid', { expression: '' }));
    const expression = field(html, 'expression');
    expect(expression).toContain('aria-invalid="true"');
    expect(expression).toContain(
      'aria-describedby="step-only-paid-expression-hint step-only-paid-expression-error"',
    );
    expect(expression).toMatch(/class="field-error"[^>]*>Required\.</);
    expect(html).toMatch(/<button type="submit" disabled="">Save/);
    expect(html).toContain('what you typed is kept');
    expect(html).toContain('data-summary-state="empty"');
    expect(text(summaryOf(html))).toContain(
      'Not configured: no condition is set',
    );
  });
});

describe('the transform form', () => {
  it('shows one row per mapping and the unmapped-field choice', () => {
    const html = render('to-billing-shape');
    expect(html).toContain('id="step-to-billing-shape-mappings-0-targetField"');
    expect(html).toContain('value="order.customer.id"');
    expect(html).toContain('data-action="add-mapping"');
    expect(html.match(/data-action="remove-mapping"/g)).toHaveLength(2);
    expect(html).toMatch(/<option value="true" selected="">Drop them/);
  });

  it('allows a mapping that drops every incoming field, and says so plainly', () => {
    const html = render('to-billing-shape');
    expect(html).toContain('data-drops-every-field="true"');
    expect(text(summaryOf(html))).toContain(
      'Drops every incoming field: the output holds only the 2 mapped fields, “amount”, “customer”, and nothing else from the message.',
    );
    // Allowed: nothing marks it as an error, and once typed it saves.
    expect(html).not.toContain('field-error');
    const typedDrop = render(
      'to-billing-shape',
      typed('to-billing-shape', {
        mappings: [{ targetField: 'id', expression: 'order.id' }],
      }),
    );
    expect(typedDrop).not.toContain('field-error');
    expect(typedDrop).toMatch(/<button type="submit">Save/);
  });

  it('puts the error on the row it is about, and on the list when it is empty', () => {
    const blankRow = render(
      'to-billing-shape',
      typed('to-billing-shape', {
        mappings: [
          { targetField: 'amount', expression: 'order.total' },
          { targetField: '', expression: 'x' },
        ],
      }),
    );
    expect(field(blankRow, 'mappings.1.targetField')).toContain('field-error');
    expect(field(blankRow, 'mappings.0.targetField')).not.toContain(
      'field-error',
    );
    expect(field(blankRow, 'mappings.1.expression')).not.toContain(
      'field-error',
    );

    const none = render(
      'to-billing-shape',
      typed('to-billing-shape', { mappings: [] }),
    );
    expect(none).toMatch(
      /data-error-for="mappings"[^>]*>Needs at least 1 mapping\./,
    );
    expect(none).toMatch(/<button type="submit" disabled="">Save/);
    expect(none).toContain('data-summary-state="empty"');
  });
});

describe('the validate form', () => {
  it('shows one row per rule, the action and the dead-letter topic', () => {
    const html = render('require-amount');
    expect(html).toContain('id="step-require-amount-rules-0-field"');
    expect(html).toMatch(/<option value="number" selected="">number/);
    expect(html).toContain('value="billing.invalid.v1"');
    expect(text(summaryOf(html))).toContain(
      'sent to the dead-letter topic “billing.invalid.v1”',
    );
  });

  it('offers only the actions the schema allows, with the default visible', () => {
    const html = render('require-amount');
    const select = field(html, 'onInvalid');
    const values = [...select.matchAll(/<option value="([^"]*)"/g)].map(
      (match) => match[1],
    );
    expect(values).toEqual([...stepChoices.onInvalid.options]);
    expect(values).toEqual(['drop', 'deadLetter', 'fail']);
    // No blank choice: the action is required.
    expect(values).not.toContain('');
    expect(select).toContain('drop (drop the message), the default');
    expect(select).toContain(
      'The default, which a new validate step starts with, is drop.',
    );
    expect(select).toMatch(/<option value="deadLetter" selected="">/);
  });

  it('a newly inserted validate step shows the default picked and says it is not configured yet', () => {
    const inserted = insertStep(
      graphFixtures['zero-processing-steps'],
      { before: { role: 'source' }, after: { role: 'destination' } },
      'validate',
    );
    const id = inserted.pipeline.processingSteps?.[0]?.id ?? '';
    const html = render(id, null, inserted.pipeline);
    expect(field(html, 'onInvalid')).toMatch(
      /<option value="drop" selected="">/,
    );
    expect(html).toContain('data-summary-state="placeholder"');
    expect(text(summaryOf(html))).toContain('Not configured yet');
  });

  it('shows an inline error and does not save a topic the schema rejects', () => {
    const html = render(
      'require-amount',
      typed('require-amount', { deadLetterTopic: 'not a topic' }),
    );
    expect(field(html, 'deadLetterTopic')).toMatch(
      /class="field-error"[^>]*>[^<]*does not allow/,
    );
    expect(html).toMatch(/<button type="submit" disabled="">Save/);
  });
});

describe('every step form', () => {
  it('is not shown for custom code or a step that is not there', () => {
    expect(render('apply-discounts')).toBe('');
    expect(render('nope')).toBe('');
  });
});
