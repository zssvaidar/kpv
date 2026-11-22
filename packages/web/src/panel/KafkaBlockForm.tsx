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
// connection kept outside the pipeline (#24). The block names one with a
// select of the names the agent lists, never typed text; a name the block
// holds that the list lacks is offered as it is and said to be dangling, so
// it is kept unless someone chooses otherwise.

import type { ChangeEvent } from 'react';

import type { Pipeline } from '@kpv/schema';

import { blockKindLabel } from '../blocks/BlockNode.tsx';
import { describeRequestProblem } from '../connection/describe.ts';
import { blockForNode } from '../graph/layout.ts';
import type { ConnectionNamesState } from './connection-names.ts';
import type { KafkaDraft, KafkaField, KafkaRole } from './kafka-form.ts';
import {
  checkDraft,
  connectionChoices,
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
  connection: 'Connection',
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
  /** The connection names the agent last listed, for the select. */
  connections: ConnectionNamesState;
  /** Reads the connection names again. */
  onReloadConnections?: () => void;
}) {
  const { pipeline, role, onDraft, onSave, connections } = props;
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

  const connectionField = () => {
    const { options, chosen } = connectionChoices(
      pipeline,
      draft,
      connections.names,
    );
    const said = (choice: (typeof options)[number]) =>
      choice.state === 'dangling'
        ? `${choice.name} (not defined)`
        : choice.state === 'unchecked'
          ? `${choice.name} (not checked)`
          : choice.name;
    const reading =
      connections.problem !== null
        ? `The connection names could not be read: ${describeRequestProblem(connections.problem).label}.`
        : connections.failure !== null
          ? `The connection names could not be read: ${connections.failure.message}`
          : connections.loading && connections.names === null
            ? 'Reading the connection names.'
            : connections.names === null
              ? 'The connection names have not been read.'
              : null;
    const notice =
      chosen?.state === 'dangling' ? (
        <p
          className="field-warning"
          id={`${prefix}-connection-dangling`}
          data-dangling={chosen.name}
        >
          No Kafka connection named <code>{chosen.name}</code> exists. The block
          keeps this name as it is until you choose another.
        </p>
      ) : chosen?.state === 'unchecked' ? (
        <p
          className="field-hint"
          id={`${prefix}-connection-dangling`}
          data-unchecked={chosen.name}
        >
          Whether a connection named <code>{chosen.name}</code> exists is not
          known until the names are read. The block keeps this name.
        </p>
      ) : null;
    const describedBy = [
      `${prefix}-connection-hint`,
      notice === null ? '' : `${prefix}-connection-dangling`,
      check.fields.connection === undefined ? '' : `${prefix}-connection-error`,
    ]
      .filter((id) => id !== '')
      .join(' ');
    const none = connections.names !== null && connections.names.length === 0;
    return (
      <div className="form-field" data-field="connection">
        <label htmlFor={`${prefix}-connection`}>{fieldLabel.connection}</label>
        <select
          id={`${prefix}-connection`}
          name="connection"
          value={draft.connection}
          aria-invalid={
            check.fields.connection === undefined ? undefined : true
          }
          aria-describedby={describedBy}
          onChange={change('connection')}
        >
          <option value="">No connection</option>
          {options.map((choice) => (
            <option
              key={choice.name}
              value={choice.name}
              data-state={choice.state}
            >
              {said(choice)}
            </option>
          ))}
        </select>
        {notice}
        {errorFor('connection')}
        <p
          className="field-hint"
          id={`${prefix}-connection-hint`}
          data-connections={none ? 'none' : undefined}
        >
          {none
            ? 'No Kafka connections are defined yet. Define one from Kafka connections in the status bar, then choose it here.'
            : 'Choose one of the Kafka connections defined from Kafka connections in the status bar, or no connection.'}
        </p>
        {reading === null ? null : (
          <p className="field-hint" role="status" data-names-problem="">
            {reading}{' '}
            {props.onReloadConnections === undefined ||
            connections.loading ? null : (
              <button type="button" onClick={props.onReloadConnections}>
                Read the names again
              </button>
            )}
          </p>
        )}
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
        {connectionField()}
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
 * here (#12), or a filter, transform or validate step (#13), and a
 * custom-code block whose file holds unsaved code (#14).
 */
export function UnsavedElsewhere(props: {
  pipeline: Pipeline;
  /** The node ids of those blocks, as layout.ts names them. */
  nodes: readonly string[];
  /** The custom-code blocks whose file holds unsaved code (#14). */
  codeNodes?: readonly string[];
  onShow: (node: string) => void;
}) {
  const placedOf = (kind: 'form' | 'code') => (node: string) => {
    const placed = blockForNode(props.pipeline, node);
    return placed === null ? [] : [{ node, placed, kind }];
  };
  const shown = [
    ...props.nodes.flatMap(placedOf('form')),
    ...(props.codeNodes ?? []).flatMap(placedOf('code')),
  ];
  if (shown.length === 0) return null;
  return (
    <div className="unsaved-elsewhere" role="status">
      {shown.map(({ node, placed, kind }) => (
        <p key={`${kind}:${node}`} data-unsaved={node} data-kind={kind}>
          The {blockKindLabel[placed.block.type]}
          {placed.role === 'step' ? (
            <>
              {' '}
              step <code>{placed.block.id}</code>
            </>
          ) : null}{' '}
          {kind === 'code'
            ? 'has unsaved code, kept until you save it or reload its file.'
            : 'has unsaved changes, kept until you save or discard them.'}{' '}
          <button type="button" onClick={() => props.onShow(node)}>
            Show
          </button>
        </p>
      ))}
    </div>
  );
}
