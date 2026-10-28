// The side panel's configuration form for the selected Kafka source or
// destination (#12). It shows what kafka-form.ts says, and hands every
// change out: the shell keeps what was typed, per block, so selecting
// another block does not lose it, and saves it through the same validated
// path the graph's edits take.
//
// The form has no rules of its own. Each field's error is what the schema in
// @kpv/schema reports for it, worded by kafka-form.ts, shown next to the
// field as soon as it is typed. Nothing here asks for or shows a credential:
// the cluster a block talks to, and how it authenticates, is a named Kafka
// connection kept outside the pipeline (#24).

import type { ChangeEvent } from 'react';

import type { Pipeline } from '@kpv/schema';

import { blockKindLabel } from '../blocks/BlockNode.tsx';
import { blockForNode } from '../graph/layout.ts';
import type { KafkaDraft, KafkaField, KafkaRole } from './kafka-form.ts';
import {
  checkDraft,
  draftFor,
  fieldDescription,
  isDirty,
  kafkaBlock,
  kafkaChoices,
} from './kafka-form.ts';

const fieldLabel: Record<KafkaField, string> = {
  topic: 'Topic',
  consumerGroupId: 'Consumer group',
  autoOffsetReset: 'Offset reset',
  acks: 'Producer acknowledgements',
};

/** What each acks value means, in words, next to Kafka's own value. */
const acksMeaning: Record<string, string> = {
  '0': "don't wait",
  '1': 'the leader',
  all: 'all in-sync replicas',
};

/** What each offset reset value means, in words. */
const offsetMeaning: Record<string, string> = {
  earliest: 'from the oldest message',
  latest: 'from new messages only',
};

export function KafkaBlockForm(props: {
  pipeline: Pipeline;
  role: KafkaRole;
  /** What was typed and not yet saved, or `null` for the block as saved. */
  draft: KafkaDraft | null;
  /** The form changed; `null` when it matches the pipeline again. */
  onDraft: (role: KafkaRole, draft: KafkaDraft | null) => void;
  onSave: (draft: KafkaDraft) => void;
}) {
  const { pipeline, role, onDraft, onSave } = props;
  const draft =
    props.draft !== null && props.draft.role === role
      ? props.draft
      : draftFor(pipeline, role);
  const dirty = isDirty(pipeline, draft);
  const check = checkDraft(pipeline, draft);
  const block = kafkaBlock(pipeline, role);
  const prefix = `kafka-${role}`;

  const change =
    (field: KafkaField) =>
    (event: ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
      const next = { ...draft, [field]: event.target.value };
      onDraft(role, isDirty(pipeline, next) ? next : null);
    };

  const value = (field: KafkaField): string =>
    (draft as unknown as Record<string, string>)[field] ?? '';

  // The ids of what describes a field: its hint, and its error when it has one.
  const describedBy = (field: KafkaField, hint: boolean): string =>
    [
      hint ? `${prefix}-${field}-hint` : '',
      check.fields[field] === undefined ? '' : `${prefix}-${field}-error`,
    ]
      .filter((id) => id !== '')
      .join(' ');

  const errorFor = (field: KafkaField) =>
    check.fields[field] === undefined ? null : (
      <p
        className="field-error"
        id={`${prefix}-${field}-error`}
        data-error-for={field}
      >
        {check.fields[field]}
      </p>
    );

  const textField = (field: KafkaField, hint: string, placeholder?: string) => (
    <div className="form-field" data-field={field}>
      <label htmlFor={`${prefix}-${field}`}>{fieldLabel[field]}</label>
      <input
        id={`${prefix}-${field}`}
        name={field}
        type="text"
        autoComplete="off"
        spellCheck={false}
        value={value(field)}
        placeholder={placeholder}
        aria-invalid={check.fields[field] === undefined ? undefined : true}
        aria-describedby={describedBy(field, hint !== '') || undefined}
        onChange={change(field)}
      />
      {errorFor(field)}
      {hint === '' ? null : (
        <p className="field-hint" id={`${prefix}-${field}-hint`}>
          {hint}
        </p>
      )}
    </div>
  );

  const choiceField = (
    field: 'autoOffsetReset' | 'acks',
    meaning: Record<string, string>,
  ) => {
    const { options, defaultValue } = kafkaChoices[field];
    const said = (option: string) =>
      meaning[option] === undefined ? option : `${option} (${meaning[option]})`;
    return (
      <div className="form-field" data-field={field}>
        <label htmlFor={`${prefix}-${field}`}>{fieldLabel[field]}</label>
        <select
          id={`${prefix}-${field}`}
          name={field}
          value={value(field)}
          aria-invalid={check.fields[field] === undefined ? undefined : true}
          aria-describedby={describedBy(field, true)}
          onChange={change(field)}
        >
          <option value="">Default: {said(defaultValue)}</option>
          {options.map((option) => (
            <option key={option} value={option}>
              {said(option)}
            </option>
          ))}
        </select>
        {errorFor(field)}
        <p className="field-hint" id={`${prefix}-${field}-hint`}>
          {fieldDescription[field]}
        </p>
      </div>
    );
  };

  const groupBlank = draft.role === 'source' && draft.consumerGroupId === '';

  return (
    <form
      className="kafka-form"
      aria-label={`Configure the ${blockKindLabel[block.type]}`}
      data-role={role}
      data-dirty={dirty ? 'true' : 'false'}
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        if (dirty && check.valid) onSave(draft);
      }}
    >
      <h2>
        {blockKindLabel[block.type]} <code>{block.id}</code>
      </h2>
      {textField('topic', fieldDescription.topic)}
      {draft.role === 'source' ? (
        <>
          {textField(
            'consumerGroupId',
            groupBlank
              ? 'Left blank: the agent generates a consumer group for this pipeline, and that is what it joins. Nothing is saved for this field.'
              : fieldDescription.consumerGroupId,
            'Generated per pipeline',
          )}
          {choiceField('autoOffsetReset', offsetMeaning)}
        </>
      ) : (
        choiceField('acks', acksMeaning)
      )}
      <section className="kafka-connection" aria-label="Kafka connection">
        <h3>Kafka connection</h3>
        <p>
          The cluster this block uses, and the credentials it signs in with,
          belong to a named Kafka connection kept outside the pipeline. They are
          chosen by name and never entered or shown here.
        </p>
        <p className="muted" data-connections="none">
          No Kafka connections are defined yet, so there is none to choose.
        </p>
      </section>
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
          onClick={() => onDraft(role, null)}
        >
          Discard changes
        </button>
      </div>
    </form>
  );
}

/**
 * Says which blocks hold unsaved changes when another block is selected, so
 * moving away from a form never hides that it was left dirty: a Kafka block
 * here (#12), or a filter, transform or validate step (#13).
 */
export function UnsavedElsewhere(props: {
  pipeline: Pipeline;
  /** The node ids of those blocks, as layout.ts names them. */
  nodes: readonly string[];
  onShow: (node: string) => void;
}) {
  const shown = props.nodes.flatMap((node) => {
    const placed = blockForNode(props.pipeline, node);
    return placed === null ? [] : [{ node, placed }];
  });
  if (shown.length === 0) return null;
  return (
    <div className="unsaved-elsewhere" role="status">
      {shown.map(({ node, placed }) => (
        <p key={node} data-unsaved={node}>
          The {blockKindLabel[placed.block.type]}
          {placed.role === 'step' ? (
            <>
              {' '}
              step <code>{placed.block.id}</code>
            </>
          ) : null}{' '}
          has unsaved changes, kept until you save or discard them.{' '}
          <button type="button" onClick={() => props.onShow(node)}>
            Show
          </button>
        </p>
      ))}
    </div>
  );
}
