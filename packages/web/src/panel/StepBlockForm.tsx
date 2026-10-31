// The side panel's configuration form for the selected filter, transform or
// validate step (#13), built the way the Kafka form (#12) is. It shows what
// step-form.ts says, and hands every change out: the shell keeps what was
// typed, per block, so selecting another block does not lose it, and saves
// it through the same validated path the graph's edits take.
//
// The form has no rules of its own. Each field's error is what the schema in
// @kpv/schema reports for it, shown next to the field as soon as it is
// typed. Above the fields is what the step will do, in plain words, derived
// by step-summary.ts from what the form holds, never stored.

import type { ChangeEvent, ReactNode } from 'react';

import type { Pipeline } from '@kpv/schema';

import { blockKindLabel } from '../blocks/BlockNode.tsx';
import type {
  FilterDraft,
  MappingDraft,
  RuleDraft,
  StepDraft,
  StepDraftCheck,
  TransformDraft,
  ValidateDraft,
} from './step-form.ts';
import {
  checkStepDraft,
  draftFor,
  isDirty,
  stepChoices,
  stepFieldDescription,
  stepFor,
  stepFromDraft,
} from './step-form.ts';
import { summarizeStep } from './step-summary.ts';

/** What each invalid-message action means, in words. */
const onInvalidMeaning: Record<string, string> = {
  drop: 'drop the message',
  deadLetter: 'send it to a dead-letter topic',
  fail: 'fail the pipeline',
};

/** A value with its meaning, the way the Kafka form shows acks. */
const said = (value: string, meaning: Record<string, string>): string =>
  meaning[value] === undefined ? value : `${value} (${meaning[value]})`;

/** The form's whole state, handed in by the caller each render. */
interface FieldKit {
  prefix: string;
  check: StepDraftCheck;
}

/** The DOM id of a field, from its place in the configuration. */
const fieldId = (kit: FieldKit, key: string): string =>
  `${kit.prefix}-${key.split('.').join('-')}`;

function FieldError(props: { kit: FieldKit; name: string }) {
  const error = props.kit.check.fields[props.name];
  return error === undefined ? null : (
    <p
      className="field-error"
      id={`${fieldId(props.kit, props.name)}-error`}
      data-error-for={props.name}
    >
      {error}
    </p>
  );
}

/** The ids of what describes a field: its hint, and its error when it has one. */
const describedBy = (kit: FieldKit, name: string, hint: boolean) =>
  [
    hint ? `${fieldId(kit, name)}-hint` : '',
    kit.check.fields[name] === undefined ? '' : `${fieldId(kit, name)}-error`,
  ]
    .filter((id) => id !== '')
    .join(' ') || undefined;

const invalid = (kit: FieldKit, name: string) =>
  kit.check.fields[name] === undefined ? undefined : true;

function Hint(props: { kit: FieldKit; name: string; children: ReactNode }) {
  return (
    <p className="field-hint" id={`${fieldId(props.kit, props.name)}-hint`}>
      {props.children}
    </p>
  );
}

function TextField(props: {
  kit: FieldKit;
  name: string;
  label: string;
  value: string;
  hint?: string;
  placeholder?: string;
  onChange: (value: string) => void;
}) {
  const { kit, name } = props;
  const hint = props.hint ?? '';
  return (
    <div className="form-field" data-field={name}>
      <label htmlFor={fieldId(kit, name)}>{props.label}</label>
      <input
        id={fieldId(kit, name)}
        name={name}
        type="text"
        autoComplete="off"
        spellCheck={false}
        value={props.value}
        placeholder={props.placeholder}
        aria-invalid={invalid(kit, name)}
        aria-describedby={describedBy(kit, name, hint !== '')}
        onChange={(event: ChangeEvent<HTMLInputElement>) =>
          props.onChange(event.target.value)
        }
      />
      <FieldError kit={kit} name={name} />
      {hint === '' ? null : (
        <Hint kit={kit} name={name}>
          {hint}
        </Hint>
      )}
    </div>
  );
}

function ChoiceField(props: {
  kit: FieldKit;
  name: string;
  label: string;
  value: string;
  /** The first option, when there is one: what leaving it unset means. */
  unset?: string;
  options: readonly { value: string; label: string }[];
  hint: string;
  onChange: (value: string) => void;
}) {
  const { kit, name } = props;
  return (
    <div className="form-field" data-field={name}>
      <label htmlFor={fieldId(kit, name)}>{props.label}</label>
      <select
        id={fieldId(kit, name)}
        name={name}
        value={props.value}
        aria-invalid={invalid(kit, name)}
        aria-describedby={describedBy(kit, name, props.hint !== '')}
        onChange={(event: ChangeEvent<HTMLSelectElement>) =>
          props.onChange(event.target.value)
        }
      >
        {props.unset === undefined ? null : (
          <option value="">{props.unset}</option>
        )}
        {props.options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
      <FieldError kit={kit} name={name} />
      {props.hint === '' ? null : (
        <Hint kit={kit} name={name}>
          {props.hint}
        </Hint>
      )}
    </div>
  );
}

/** A yes/no choice whose blank is the schema's default. */
const booleanOptions = (yes: string, no: string) => [
  { value: 'true', label: yes },
  { value: 'false', label: no },
];

function FilterFields(props: {
  kit: FieldKit;
  draft: FilterDraft;
  change: (draft: StepDraft) => void;
}) {
  const { kit, draft, change } = props;
  const keepDefault = stepChoices.keepOnMatch.defaultValue;
  return (
    <>
      <TextField
        kit={kit}
        name="expression"
        label="Condition"
        value={draft.expression}
        hint={stepFieldDescription.expression}
        placeholder="status == 'paid'"
        onChange={(expression) => change({ ...draft, expression })}
      />
      <ChoiceField
        kit={kit}
        name="keepOnMatch"
        label="Messages that match"
        value={draft.keepOnMatch}
        unset={`Default: ${keepDefault ? 'keep them' : 'drop them'}`}
        options={booleanOptions('Keep them', 'Drop them')}
        hint={stepFieldDescription.keepOnMatch}
        onChange={(keepOnMatch) => change({ ...draft, keepOnMatch })}
      />
    </>
  );
}

function TransformFields(props: {
  kit: FieldKit;
  draft: TransformDraft;
  change: (draft: StepDraft) => void;
}) {
  const { kit, draft, change } = props;
  const setRow = (index: number, row: MappingDraft) =>
    change({
      ...draft,
      mappings: draft.mappings.map((each, i) => (i === index ? row : each)),
    });
  const dropDefault = stepChoices.dropUnmappedFields.defaultValue;
  return (
    <>
      <fieldset className="form-rows" data-field="mappings">
        <legend>Field mappings</legend>
        <p className="field-hint">{stepFieldDescription.mappings}</p>
        {draft.mappings.map((row, index) => (
          // A row has no id of its own; its place is what the schema names.
          <div className="form-row" key={index} data-row={index}>
            <TextField
              kit={kit}
              name={`mappings.${String(index)}.targetField`}
              label={`Target field ${String(index + 1)}`}
              value={row.targetField}
              placeholder="order.total"
              onChange={(targetField) => setRow(index, { ...row, targetField })}
            />
            <TextField
              kit={kit}
              name={`mappings.${String(index)}.expression`}
              label={`Expression ${String(index + 1)}`}
              value={row.expression}
              onChange={(expression) => setRow(index, { ...row, expression })}
            />
            <button
              type="button"
              data-action="remove-mapping"
              onClick={() =>
                change({
                  ...draft,
                  mappings: draft.mappings.filter((_, i) => i !== index),
                })
              }
            >
              Remove mapping {index + 1}
            </button>
          </div>
        ))}
        <FieldError kit={kit} name="mappings" />
        <button
          type="button"
          data-action="add-mapping"
          onClick={() =>
            change({
              ...draft,
              mappings: [
                ...draft.mappings,
                { targetField: '', expression: '' },
              ],
            })
          }
        >
          Add mapping
        </button>
      </fieldset>
      <ChoiceField
        kit={kit}
        name="dropUnmappedFields"
        label="Fields not mapped"
        value={draft.dropUnmappedFields}
        unset={`Default: ${dropDefault ? 'drop them' : 'keep them'}`}
        options={booleanOptions('Drop them', 'Keep them')}
        hint={stepFieldDescription.dropUnmappedFields}
        onChange={(dropUnmappedFields) =>
          change({ ...draft, dropUnmappedFields })
        }
      />
    </>
  );
}

function ValidateFields(props: {
  kit: FieldKit;
  draft: ValidateDraft;
  change: (draft: StepDraft) => void;
}) {
  const { kit, draft, change } = props;
  const setRow = (index: number, row: RuleDraft) =>
    change({
      ...draft,
      rules: draft.rules.map((each, i) => (i === index ? row : each)),
    });
  const requiredDefault = stepChoices.required.defaultValue;
  const { options, startsWith } = stepChoices.onInvalid;
  return (
    <>
      <fieldset className="form-rows" data-field="rules">
        <legend>Rules</legend>
        <p className="field-hint">{stepFieldDescription.rules}</p>
        {draft.rules.map((row, index) => {
          const at = `rules.${String(index)}`;
          return (
            <div className="form-row" key={index} data-row={index}>
              <TextField
                kit={kit}
                name={`${at}.field`}
                label={`Field ${String(index + 1)}`}
                value={row.field}
                placeholder="amount"
                onChange={(field) => setRow(index, { ...row, field })}
              />
              <ChoiceField
                kit={kit}
                name={`${at}.required`}
                label="Presence"
                value={row.required}
                unset={`Default: ${requiredDefault ? 'must be present' : 'may be missing'}`}
                options={booleanOptions('Must be present', 'May be missing')}
                hint=""
                onChange={(required) => setRow(index, { ...row, required })}
              />
              <ChoiceField
                kit={kit}
                name={`${at}.fieldType`}
                label="Type"
                value={row.fieldType}
                unset="Any type"
                options={stepChoices.fieldType.options.map((type) => ({
                  value: type,
                  label: type,
                }))}
                hint=""
                onChange={(fieldType) => setRow(index, { ...row, fieldType })}
              />
              <button
                type="button"
                data-action="remove-rule"
                onClick={() =>
                  change({
                    ...draft,
                    rules: draft.rules.filter((_, i) => i !== index),
                  })
                }
              >
                Remove rule {index + 1}
              </button>
            </div>
          );
        })}
        <FieldError kit={kit} name="rules" />
        <button
          type="button"
          data-action="add-rule"
          onClick={() =>
            change({
              ...draft,
              rules: [
                ...draft.rules,
                { field: '', required: '', fieldType: '' },
              ],
            })
          }
        >
          Add rule
        </button>
      </fieldset>
      <ChoiceField
        kit={kit}
        name="onInvalid"
        label="When a message breaks a rule"
        value={draft.onInvalid}
        options={options.map((value) => ({
          value,
          label: said(value, onInvalidMeaning),
        }))}
        hint={`${stepFieldDescription.onInvalid} A new validate step starts with ${startsWith}.`}
        onChange={(onInvalid) => change({ ...draft, onInvalid })}
      />
      <TextField
        kit={kit}
        name="deadLetterTopic"
        label="Dead-letter topic"
        value={draft.deadLetterTopic}
        hint="Where a message that breaks a rule goes when the action is deadLetter. Left blank, nothing is saved for this field."
        placeholder="None"
        onChange={(deadLetterTopic) => change({ ...draft, deadLetterTopic })}
      />
    </>
  );
}

export function StepBlockForm(props: {
  pipeline: Pipeline;
  stepId: string;
  /** What was typed and not yet saved, or `null` for the step as saved. */
  draft: StepDraft | null;
  /** The form changed; `null` when it matches the pipeline again. */
  onDraft: (stepId: string, draft: StepDraft | null) => void;
  onSave: (draft: StepDraft) => void;
}) {
  const { pipeline, stepId, onDraft, onSave } = props;
  const step = stepFor(pipeline, stepId);
  if (step === null) return null;
  const draft =
    props.draft !== null &&
    props.draft.id === stepId &&
    props.draft.type === step.type
      ? props.draft
      : draftFor(step);
  const dirty = isDirty(pipeline, draft);
  const check = checkStepDraft(pipeline, draft);
  const kit: FieldKit = { prefix: `step-${stepId}`, check };
  const summary = summarizeStep(stepFromDraft(pipeline, draft));
  const change = (next: StepDraft) =>
    onDraft(stepId, isDirty(pipeline, next) ? next : null);

  return (
    <form
      className="step-form"
      aria-label={`Configure the ${blockKindLabel[step.type].toLowerCase()} step`}
      data-step={stepId}
      data-type={step.type}
      data-dirty={dirty ? 'true' : 'false'}
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        if (dirty && check.valid) onSave(draft);
      }}
    >
      <h2>
        {blockKindLabel[step.type]} <code>{step.id}</code>
      </h2>
      <section
        className="step-summary"
        aria-label="Summary"
        data-summary-state={summary.state}
        data-drops-every-field={summary.dropsEveryField ? 'true' : 'false'}
      >
        <h3>
          {dirty ? 'What this step will do once saved' : 'What this step does'}
        </h3>
        {summary.sentences.map((sentence, index) => (
          <p key={index}>{sentence}</p>
        ))}
      </section>
      {draft.type === 'filter' ? (
        <FilterFields kit={kit} draft={draft} change={change} />
      ) : draft.type === 'transform' ? (
        <TransformFields kit={kit} draft={draft} change={change} />
      ) : (
        <ValidateFields kit={kit} draft={draft} change={change} />
      )}
      {check.form.length === 0 ? null : (
        <ul className="form-errors">
          {check.form.map((problem) => (
            <li key={problem}>{problem}</li>
          ))}
        </ul>
      )}
      <p className="form-status" role="status" aria-live="polite">
        {!dirty
          ? 'Saved.'
          : check.valid
            ? 'Unsaved changes.'
            : 'Unsaved changes. Fix the fields marked above to save; what you typed is kept.'}
      </p>
      <div className="form-actions">
        <button type="submit" disabled={!dirty || !check.valid}>
          Save
        </button>
        <button
          type="button"
          disabled={!dirty}
          onClick={() => onDraft(stepId, null)}
        >
          Discard changes
        </button>
      </div>
    </form>
  );
}
