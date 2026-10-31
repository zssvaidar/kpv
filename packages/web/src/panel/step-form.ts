// The filter, transform and validate configuration panels' model (#13): what
// the form holds while someone types, how that becomes a processing step,
// and what the schema says about it. It follows the Kafka form's model
// (kafka-form.ts) and words what the schema reports the same way.
//
// The form never decides on its own what is valid. Every check is the
// generated Zod schema in @kpv/schema, run on the whole pipeline with the
// typed step in place, by the same function commit runs when it saves
// (graph/edit.ts). What is here is only the mapping between text fields and
// the step, and where each thing the schema reports belongs in the form.
//
// Nothing here imports React, so all of it is tested without rendering.

import type {
  FilterStep,
  Pipeline,
  ProcessingStep,
  TransformStep,
  ValidateStep,
} from '@kpv/schema';
import {
  expressionSchema,
  fieldPathSchema,
  filterStepSchema,
  transformStepSchema,
  validateStepSchema,
} from '@kpv/schema';

import type { EditResult, SchemaIssue } from '../graph/edit.ts';
import {
  newStepConfiguration,
  schemaIssues,
  updateStep,
  withStep,
} from '../graph/edit.ts';
import { describeIssue } from './kafka-form.ts';

/** The processing steps these panels configure; custom code is #14. */
export type ConfigurableStep = FilterStep | TransformStep | ValidateStep;

/** Whether a step is one of them. */
export const isConfigurable = (
  step: ProcessingStep,
): step is ConfigurableStep =>
  step.type === 'filter' ||
  step.type === 'transform' ||
  step.type === 'validate';

/**
 * What the filter form holds: each field as typed. A choice left at '' is
 * left out of the step, so the schema's default applies, the way the Kafka
 * form leaves out a blank optional field.
 */
export interface FilterDraft {
  type: 'filter';
  id: string;
  expression: string;
  /** '', 'true' or 'false'. */
  keepOnMatch: string;
}

/** One row of the transform form. */
export interface MappingDraft {
  targetField: string;
  expression: string;
}

export interface TransformDraft {
  type: 'transform';
  id: string;
  mappings: MappingDraft[];
  /** '', 'true' or 'false'. */
  dropUnmappedFields: string;
}

/** One row of the validate form. */
export interface RuleDraft {
  field: string;
  /** '', 'true' or 'false'. */
  required: string;
  /** '' for any type, or one the schema allows. */
  fieldType: string;
}

export interface ValidateDraft {
  type: 'validate';
  id: string;
  rules: RuleDraft[];
  onInvalid: string;
  /** '' leaves it out. */
  deadLetterTopic: string;
}

export type StepDraft = FilterDraft | TransformDraft | ValidateDraft;

// The schema's own pieces, read from the generated Zod rather than restated.
const filterConfiguration = filterStepSchema.shape.configuration.shape;
const transformConfiguration = transformStepSchema.shape.configuration.shape;
const validateConfiguration = validateStepSchema.shape.configuration.shape;
const ruleShape = validateConfiguration.rules.element.shape;

/**
 * The values the schema allows for each choice, and what applies when it is
 * left unset. onInvalid is different: it is required and has no default,
 * because each action trades data loss against availability and the
 * pipeline's author chooses one. The form never calls any action a default;
 * startsWith is only the value a newly inserted validate step holds
 * (newStepConfiguration in graph/edit.ts).
 */
export const stepChoices = {
  keepOnMatch: {
    defaultValue: filterConfiguration.keepOnMatch.def.defaultValue,
  },
  dropUnmappedFields: {
    defaultValue: transformConfiguration.dropUnmappedFields.def.defaultValue,
  },
  required: {
    defaultValue: ruleShape.required.def.defaultValue,
  },
  fieldType: {
    options: ruleShape.fieldType.unwrap().options as readonly string[],
  },
  onInvalid: {
    options: validateConfiguration.onInvalid.options as readonly string[],
    startsWith: newStepConfiguration.validate.onInvalid as string,
  },
} as const;

/** The schema's own description of each field, for the hint under it. */
export const stepFieldDescription = {
  expression: expressionSchema.description ?? '',
  keepOnMatch: filterConfiguration.keepOnMatch.unwrap().description ?? '',
  mappings: transformConfiguration.mappings.description ?? '',
  targetField: fieldPathSchema.description ?? '',
  dropUnmappedFields:
    transformConfiguration.dropUnmappedFields.unwrap().description ?? '',
  rules: validateConfiguration.rules.description ?? '',
  required: ruleShape.required.unwrap().description ?? '',
  fieldType: ruleShape.fieldType.unwrap().description ?? '',
  onInvalid: validateConfiguration.onInvalid.description ?? '',
} as const;

const steps = (pipeline: Pipeline): readonly ProcessingStep[] =>
  pipeline.processingSteps ?? [];

/** The configurable step with this id, or `null` when there is none. */
export function stepFor(
  pipeline: Pipeline,
  id: string,
): ConfigurableStep | null {
  const step = steps(pipeline).find((each) => each.id === id);
  return step !== undefined && isConfigurable(step) ? step : null;
}

const boolText = (value: boolean | undefined): string =>
  value === undefined ? '' : String(value);

/** The form as it starts for this step: what the step holds now. */
export function draftFor(step: ConfigurableStep): StepDraft {
  switch (step.type) {
    case 'filter':
      return {
        type: 'filter',
        id: step.id,
        expression: step.configuration.expression,
        keepOnMatch: boolText(step.configuration.keepOnMatch),
      };
    case 'transform':
      return {
        type: 'transform',
        id: step.id,
        mappings: step.configuration.mappings.map((mapping) => ({
          targetField: mapping.targetField,
          expression: mapping.expression,
        })),
        dropUnmappedFields: boolText(step.configuration.dropUnmappedFields),
      };
    case 'validate':
      return {
        type: 'validate',
        id: step.id,
        rules: step.configuration.rules.map((rule) => ({
          field: rule.field,
          required: boolText(rule.required),
          fieldType: rule.fieldType ?? '',
        })),
        onInvalid: step.configuration.onInvalid,
        deadLetterTopic: step.configuration.deadLetterTopic ?? '',
      };
  }
}

/** The same value, whatever order its keys came in. */
export const same = (a: unknown, b: unknown): boolean => {
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object') return false;
  if (a === null || b === null) return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const left = a as Record<string, unknown>;
  const right = b as Record<string, unknown>;
  const keys = Object.keys(left);
  return (
    keys.length === Object.keys(right).length &&
    keys.every((key) => same(left[key], right[key]))
  );
};

/** Whether the form holds anything the pipeline does not. */
export function isDirty(pipeline: Pipeline, draft: StepDraft): boolean {
  const step = stepFor(pipeline, draft.id);
  return step === null || !same(draftFor(step), draft);
}

/**
 * Sets a property to the value a choice stands for, or leaves it out when it
 * is blank. 'true' and 'false' are the booleans; anything else is put in as
 * typed, for the schema to say what is wrong with it.
 */
const setChoice = (
  record: Record<string, unknown>,
  key: string,
  value: string,
) => {
  if (value === '') return;
  record[key] = value === 'true' ? true : value === 'false' ? false : value;
};

/**
 * The step the form describes: the pipeline's current step with the typed
 * configuration put in, so whatever the form does not show (its custom code
 * reference) is kept as it is. Not checked: checkStepDraft does that.
 */
export function stepFromDraft(
  pipeline: Pipeline,
  draft: StepDraft,
): ConfigurableStep {
  const base = stepFor(pipeline, draft.id) ?? { id: draft.id };
  switch (draft.type) {
    case 'filter': {
      const configuration: Record<string, unknown> = {
        expression: draft.expression,
      };
      setChoice(configuration, 'keepOnMatch', draft.keepOnMatch);
      return {
        ...base,
        type: 'filter',
        configuration,
      } as unknown as FilterStep;
    }
    case 'transform': {
      const configuration: Record<string, unknown> = {
        mappings: draft.mappings.map((mapping) => ({
          targetField: mapping.targetField,
          expression: mapping.expression,
        })),
      };
      setChoice(configuration, 'dropUnmappedFields', draft.dropUnmappedFields);
      return {
        ...base,
        type: 'transform',
        configuration,
      } as unknown as TransformStep;
    }
    case 'validate': {
      const configuration: Record<string, unknown> = {
        rules: draft.rules.map((rule) => {
          const out: Record<string, unknown> = { field: rule.field };
          setChoice(out, 'required', rule.required);
          if (rule.fieldType !== '') out.fieldType = rule.fieldType;
          return out;
        }),
        onInvalid: draft.onInvalid,
      };
      if (draft.deadLetterTopic !== '') {
        configuration.deadLetterTopic = draft.deadLetterTopic;
      }
      return {
        ...base,
        type: 'validate',
        configuration,
      } as unknown as ValidateStep;
    }
  }
}

/**
 * What the schema says about the form. A field is named by its place in the
 * configuration, as the schema reports it: 'expression', 'mappings' (the
 * list itself), 'mappings.0.targetField', 'rules.1.fieldType' and so on.
 */
export interface StepDraftCheck {
  /** The first problem with each field that has one. */
  fields: Record<string, string>;
  /** Problems the schema found anywhere else, which no field can show. */
  form: string[];
  /** Whether the form can be saved as it is. */
  valid: boolean;
}

/** What a list of rows is a list of, for "needs at least one ...". */
const rowNoun: Record<string, string> = { mappings: 'mapping', rules: 'rule' };

/** A schema issue in words, from what Zod reported. */
export function describeStepIssue(issue: SchemaIssue): string {
  const last = issue.path[issue.path.length - 1];
  if (issue.code === 'too_small' && issue.detail.origin === 'array') {
    const noun = typeof last === 'string' ? (rowNoun[last] ?? 'item') : 'item';
    const minimum = Number(issue.detail.minimum);
    return `Needs at least ${String(minimum)} ${noun}${minimum === 1 ? '' : 's'}.`;
  }
  return describeIssue(issue);
}

/**
 * Checks the form against the schema, on the whole pipeline with the typed
 * step in place, which is exactly what saving it would commit.
 */
export function checkStepDraft(
  pipeline: Pipeline,
  draft: StepDraft,
): StepDraftCheck {
  const next = withStep(pipeline, stepFromDraft(pipeline, draft));
  if (next === null) {
    return {
      fields: {},
      form: [`There is no step "${draft.id}" in this pipeline any more.`],
      valid: false,
    };
  }
  const index = steps(pipeline).findIndex((step) => step.id === draft.id);
  const fields: Record<string, string> = {};
  const form: string[] = [];
  for (const issue of schemaIssues(next)) {
    const [list, at, part, ...rest] = issue.path;
    if (
      list === 'processingSteps' &&
      at === index &&
      part === 'configuration' &&
      rest.length > 0
    ) {
      const key = rest.map(String).join('.');
      fields[key] ??= describeStepIssue(issue);
    } else {
      const where = issue.path.map(String).join('.');
      form.push(
        `${where === '' ? '' : `${where}: `}${describeStepIssue(issue)}`,
      );
    }
  }
  return {
    fields,
    form,
    valid: Object.keys(fields).length + form.length === 0,
  };
}

/**
 * Saves the form: the step through updateStep, the same validated path
 * every graph edit takes. A form the schema refuses comes back as a
 * rejection carrying the pipeline unchanged.
 */
export function saveStepDraft(
  pipeline: Pipeline,
  draft: StepDraft,
): EditResult {
  return updateStep(pipeline, stepFromDraft(pipeline, draft));
}
