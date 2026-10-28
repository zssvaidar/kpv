// What a filter, transform or validate step will do, in plain words (#13).
//
// A summary is derived from the step's configuration every time it is shown
// and is never stored with it. It says what the configuration states and no
// more: the expressions are shown as written, because running them is #29.
// A step with nothing set says so, and so does one still holding the
// placeholder every new step of its type starts with (newStepConfiguration
// in graph/edit.ts), rather than describing it as though someone chose it.
//
// Nothing here imports React, so all of it is tested without rendering.

import { newStepConfiguration } from '../graph/edit.ts';
import type { ConfigurableStep } from './step-form.ts';
import { same, stepChoices } from './step-form.ts';

/**
 * Whether the step says anything yet: nothing set, the placeholder it was
 * inserted with, or a configuration someone gave it.
 */
export type SummaryState = 'empty' | 'placeholder' | 'configured';

export interface StepSummary {
  state: SummaryState;
  /** The summary, one sentence each. */
  sentences: string[];
  /**
   * A transform that emits only its mapped fields, which drops every field
   * of the incoming message. Allowed, and said plainly.
   */
  dropsEveryField: boolean;
}

/** A name or expression as it was written, quoted in the sentence. */
const q = (text: string): string => `“${text}”`;

const blank = (text: string | undefined): boolean =>
  text === undefined || text.trim() === '';

const plural = (n: number, one: string, many: string): string =>
  `${String(n)} ${n === 1 ? one : many}`;

const summary = (
  state: SummaryState,
  sentences: string[],
  dropsEveryField = false,
): StepSummary => ({ state, sentences, dropsEveryField });

function filterSummary(configuration: {
  expression?: string;
  keepOnMatch?: boolean;
}): StepSummary {
  const { expression } = configuration;
  if (blank(expression)) {
    return summary('empty', [
      'Not configured: no condition is set, so this filter decides nothing yet.',
    ]);
  }
  if (same(configuration, newStepConfiguration.filter)) {
    return summary('placeholder', [
      `Not configured yet: this is the condition every new filter starts with, ${q(newStepConfiguration.filter.expression)}.`,
      'As it stands, it keeps every message.',
    ]);
  }
  const keep =
    configuration.keepOnMatch ?? stepChoices.keepOnMatch.defaultValue;
  return summary('configured', [
    keep
      ? `Keeps a message only when ${q(expression ?? '')} holds; every other message is dropped.`
      : `Drops every message for which ${q(expression ?? '')} holds; every other message continues.`,
  ]);
}

function transformSummary(configuration: {
  mappings?: readonly { targetField?: string; expression?: string }[];
  dropUnmappedFields?: boolean;
}): StepSummary {
  const mappings = configuration.mappings ?? [];
  const complete = mappings.filter(
    (mapping) => !blank(mapping.targetField) && !blank(mapping.expression),
  );
  const unfinished = mappings.length - complete.length;
  const dropUnmapped =
    configuration.dropUnmappedFields ??
    stepChoices.dropUnmappedFields.defaultValue;
  const unfinishedNote =
    unfinished === 0
      ? []
      : [
          `${plural(unfinished, 'mapping is', 'mappings are')} not finished and left out of this summary.`,
        ];
  if (complete.length === 0) {
    return summary(
      'empty',
      [
        'Not configured: no field mapping is set.',
        ...unfinishedNote,
        ...(dropUnmapped
          ? [
              'Unmapped fields are set to be dropped, so as it stands the output would have no fields at all.',
            ]
          : []),
      ],
      dropUnmapped,
    );
  }
  if (same(configuration, newStepConfiguration.transform)) {
    const [placeholder] = newStepConfiguration.transform.mappings;
    return summary('placeholder', [
      `Not configured yet: this is the mapping every new transform starts with, which sets ${q(placeholder.targetField)} from ${q(placeholder.expression)}.`,
      'As it stands, every other field passes through unchanged.',
    ]);
  }
  const names = complete.map((mapping) => q(mapping.targetField ?? ''));
  const sets = complete
    .map(
      (mapping) =>
        `${q(mapping.targetField ?? '')} from ${q(mapping.expression ?? '')}`,
    )
    .join('; ');
  return summary(
    'configured',
    [
      `Sets ${plural(complete.length, 'field', 'fields')}: ${sets}.`,
      dropUnmapped
        ? `Drops every incoming field: the output holds only ${
            complete.length === 1
              ? 'the one mapped field'
              : `the ${String(complete.length)} mapped fields`
          }, ${names.join(', ')}, and nothing else from the message.`
        : 'Every other field of the message passes through unchanged.',
      ...unfinishedNote,
    ],
    dropUnmapped,
  );
}

/** "a number", "an object". */
const withArticle = (type: string): string =>
  `${'aeiou'.includes(type.charAt(0)) ? 'an' : 'a'} ${type}`;

function ruleSentence(rule: {
  field?: string;
  required?: boolean;
  fieldType?: string;
}): string {
  const field = q(rule.field ?? '');
  const required = rule.required ?? stepChoices.required.defaultValue;
  const type = rule.fieldType;
  if (required && type !== undefined) {
    return `${field} must be present and be ${withArticle(type)}`;
  }
  if (required) return `${field} must be present`;
  if (type !== undefined) {
    return `${field}, when present, must be ${withArticle(type)}`;
  }
  return `${field} is optional with no type, so its rule checks nothing`;
}

function actionSentences(configuration: {
  onInvalid?: string;
  deadLetterTopic?: string;
}): string[] {
  const topic = configuration.deadLetterTopic;
  const named = !blank(topic);
  switch (configuration.onInvalid) {
    case 'drop':
    case 'fail': {
      const said =
        configuration.onInvalid === 'drop'
          ? 'A message that breaks a rule is dropped.'
          : 'A message that breaks a rule fails the pipeline, which stops.';
      return named
        ? [
            said,
            `The dead-letter topic ${q(topic ?? '')} is set but not used with this action.`,
          ]
        : [said];
    }
    case 'deadLetter':
      return [
        named
          ? `A message that breaks a rule is sent to the dead-letter topic ${q(topic ?? '')}.`
          : 'A message that breaks a rule is meant for a dead-letter topic, but none is named.',
      ];
    default:
      return ['No action is chosen for a message that breaks a rule.'];
  }
}

function validateSummary(configuration: {
  rules?: readonly { field?: string; required?: boolean; fieldType?: string }[];
  onInvalid?: string;
  deadLetterTopic?: string;
}): StepSummary {
  const rules = configuration.rules ?? [];
  const complete = rules.filter((rule) => !blank(rule.field));
  const unfinished = rules.length - complete.length;
  const unfinishedNote =
    unfinished === 0
      ? []
      : [
          `${plural(unfinished, 'rule has', 'rules have')} no field yet and ${unfinished === 1 ? 'is' : 'are'} left out of this summary.`,
        ];
  if (complete.length === 0) {
    return summary('empty', [
      'Not configured: no rule is set, so nothing is checked.',
      ...unfinishedNote,
    ]);
  }
  if (same(configuration, newStepConfiguration.validate)) {
    const [placeholder] = newStepConfiguration.validate.rules;
    return summary('placeholder', [
      `Not configured yet: this is the rule every new validate step starts with, that ${q(placeholder.field)} must be present.`,
      `As it stands, a message without ${q(placeholder.field)} is dropped.`,
    ]);
  }
  return summary('configured', [
    `Checks ${plural(complete.length, 'rule', 'rules')}: ${complete
      .map(ruleSentence)
      .join('; ')}.`,
    ...unfinishedNote,
    ...actionSentences(configuration),
  ]);
}

/** What this step will do, in words, from its configuration alone. */
export function summarizeStep(step: ConfigurableStep): StepSummary {
  switch (step.type) {
    case 'filter':
      return filterSummary(step.configuration);
    case 'transform':
      return transformSummary(step.configuration);
    case 'validate':
      return validateSummary(step.configuration);
  }
}
