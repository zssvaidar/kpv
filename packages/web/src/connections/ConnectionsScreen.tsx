// The connection screen (#24): its own screen in the graph area, reached
// from the shell, never the block side panel. It lists every stored Kafka
// connection by name with its protocol and mechanism, and creates, edits and
// deletes one.
//
// The form has no rules of its own: each field's error is what
// connectionIssues from @kpv/schema says (form.ts), or what the agent refused
// on that field, shown next to it with aria-invalid and aria-describedby. It
// shows only the fields the chosen protocol allows. The password and the
// client key are never prefilled: a stored one shows as "set" or "not set",
// leaving the input empty keeps it, and removing it is its own button.

import type { ChangeEvent } from 'react';

import type { ConnectionsError, UncheckedReason } from '@kpv/schema';

import { describeRequestProblem } from '../connection/describe.ts';
import type { ConnectionsActions, ConnectionsState } from './controller.ts';
import {
  connectionRequest,
  credentialState,
  draftIssues,
  droppedOnSave,
  saslMechanisms,
  securityProtocols,
  visibleGroups,
  type ConnectionDraft,
  type ConnectionField,
  type CredentialField,
} from './form.ts';

const fieldLabel: Record<ConnectionField, string> = {
  name: 'Name',
  bootstrapServers: 'Bootstrap servers',
  securityProtocol: 'Security protocol',
  saslMechanism: 'SASL mechanism',
  saslUsername: 'SASL username',
  saslPassword: 'SASL password',
  tlsCaCertificate: 'CA certificate (PEM)',
  tlsClientCertificate: 'Client certificate (PEM)',
  tlsClientKey: 'Client private key (PEM)',
};

/** Why a place could not be checked for references, in words. */
export const uncheckedWords: Record<UncheckedReason, string> = {
  parse: 'its pipeline.yaml is not valid YAML',
  schema: 'its pipeline.yaml is not a valid pipeline',
  tooLarge: 'its pipeline.yaml is too large to read',
  link: 'its pipeline.yaml is a symbolic link, which is not read',
  notRegularFile: 'its pipeline.yaml is not a regular file',
  unreadable: 'the agent may not read it',
  rootUnavailable: 'the project directory is unavailable',
  truncated: 'the project directory holds too many entries to check them all',
};

const prefix = 'connection';

export function ConnectionsView(props: {
  state: ConnectionsState;
  actions: ConnectionsActions;
  /** Back to the pipelines. */
  onClose: () => void;
}) {
  const { state, actions, onClose } = props;
  return (
    <section className="connections" aria-labelledby="connections-title">
      <button type="button" className="back-to-pipelines" onClick={onClose}>
        Back to the pipelines
      </button>
      <h1 id="connections-title">Kafka connections</h1>
      <p className="muted">
        Connections are kept by the agent in its own configuration directory,
        never in a pipeline file. A pipeline names one; its credentials never
        come back to this page.
      </p>
      {state.problem === null ? null : (
        <p className="list-problem" role="alert">
          {describeRequestProblem(state.problem).label}.{' '}
          {describeRequestProblem(state.problem).detail}
        </p>
      )}
      {state.failure === null ? null : (
        <p
          className="list-problem"
          role="alert"
          data-error={state.failure.error}
        >
          {state.failure.message}
        </p>
      )}
      {state.report === null ? null : (
        <ReferenceReport state={state} actions={actions} />
      )}
      <ConnectionTable state={state} actions={actions} />
      {state.draft === null ? (
        <button
          type="button"
          onClick={() => {
            actions.startCreate();
          }}
        >
          New connection
        </button>
      ) : (
        <ConnectionForm state={state} draft={state.draft} actions={actions} />
      )}
    </section>
  );
}

function ConnectionTable(props: {
  state: ConnectionsState;
  actions: ConnectionsActions;
}) {
  const { state, actions } = props;
  if (state.list === null) {
    return state.loading ? <p className="muted">Reading connections…</p> : null;
  }
  if (state.list.length === 0) {
    return <p className="muted">No connections yet.</p>;
  }
  return (
    <table className="connection-list">
      <thead>
        <tr>
          <th scope="col">Name</th>
          <th scope="col">Protocol</th>
          <th scope="col">Mechanism</th>
          <th scope="col">
            <span className="visually-hidden">Actions</span>
          </th>
        </tr>
      </thead>
      <tbody>
        {state.list.map((connection) => (
          <tr key={connection.name} data-connection={connection.name}>
            <th scope="row">{connection.name}</th>
            <td>{connection.securityProtocol}</td>
            <td>{connection.saslMechanism ?? '—'}</td>
            <td>
              <button
                type="button"
                disabled={state.saving}
                onClick={() => {
                  actions.startEdit(connection.name);
                }}
              >
                Edit
              </button>
              <button
                type="button"
                disabled={state.saving}
                onClick={() => {
                  actions.remove(connection.name);
                }}
              >
                Delete
              </button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function ReferenceReport(props: {
  state: ConnectionsState;
  actions: ConnectionsActions;
}) {
  const report = props.state.report;
  if (report === null) return null;
  const error: ConnectionsError = report.error;
  const references = error.references ?? [];
  const unchecked = error.unchecked ?? [];
  const where = (root: string, directory?: string) =>
    directory === undefined ? root : `${root}/${directory}`;
  return (
    <div
      className="reference-report"
      role="alertdialog"
      aria-labelledby="report-title"
    >
      <h2 id="report-title">
        {report.action === 'delete'
          ? `Delete ${report.name}?`
          : `Rename ${report.name}?`}
      </h2>
      {references.length === 0 ? null : (
        <>
          <p>These pipelines name it, and will keep the old name:</p>
          <ul>
            {references.map((reference) => (
              <li
                key={`${reference.root}\u0000${reference.directory ?? ''}\u0000${reference.block}`}
                data-reference={reference.block}
              >
                {reference.pipelineId} (
                {where(reference.root, reference.directory)}
                ), its {reference.block}
              </li>
            ))}
          </ul>
        </>
      )}
      {unchecked.length === 0 ? null : (
        <>
          <p>These could not be checked, so they may name it too:</p>
          <ul>
            {unchecked.map((place) => (
              <li
                key={`${place.root}\u0000${place.directory ?? ''}`}
                data-unchecked={place.reason}
              >
                {where(place.root, place.directory)}:{' '}
                {uncheckedWords[place.reason]}
              </li>
            ))}
          </ul>
        </>
      )}
      {report.retype.length === 0 ? null : (
        <p role="note">
          What you typed into{' '}
          {report.retype.map((field) => fieldLabel[field]).join(' and ')} was
          cleared when it was sent; type it again before going ahead, or it
          stays as stored.
        </p>
      )}
      <button
        type="button"
        onClick={() => {
          props.actions.confirm();
        }}
      >
        {report.action === 'delete' ? 'Delete anyway' : 'Rename anyway'}
      </button>
      <button
        type="button"
        onClick={() => {
          props.actions.cancelReport();
        }}
      >
        Keep it
      </button>
    </div>
  );
}

function ConnectionForm(props: {
  state: ConnectionsState;
  draft: ConnectionDraft;
  actions: ConnectionsActions;
}) {
  const { state, draft, actions } = props;
  const issues = draftIssues(draft);
  const errors: Partial<Record<ConnectionField, string>> = {
    ...issues,
    ...state.refusal,
  };
  const visible = visibleGroups(draft);
  const dropped = droppedOnSave(draft);
  const canSave =
    connectionRequest(draft) !== null &&
    Object.keys(state.refusal).length === 0 &&
    !state.saving;

  const change =
    (field: ConnectionField) =>
    (
      event: ChangeEvent<
        HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement
      >,
    ) => {
      actions.edit(field, event.target.value);
    };

  const describedBy = (field: ConnectionField, hint: boolean) =>
    [
      hint ? `${prefix}-${field}-hint` : '',
      errors[field] === undefined ? '' : `${prefix}-${field}-error`,
    ]
      .filter((id) => id !== '')
      .join(' ') || undefined;

  const errorFor = (field: ConnectionField) =>
    errors[field] === undefined ? null : (
      <p
        className="field-error"
        id={`${prefix}-${field}-error`}
        data-error-for={field}
      >
        {errors[field]}
      </p>
    );

  const hintFor = (field: ConnectionField, hint: string) => (
    <p className="field-hint" id={`${prefix}-${field}-hint`}>
      {hint}
    </p>
  );

  const textField = (field: ConnectionField, hint = '') => (
    <div className="form-field" data-field={field}>
      <label htmlFor={`${prefix}-${field}`}>{fieldLabel[field]}</label>
      <input
        id={`${prefix}-${field}`}
        name={field}
        type="text"
        autoComplete="off"
        spellCheck={false}
        value={draft[field]}
        aria-invalid={errors[field] === undefined ? undefined : true}
        aria-describedby={describedBy(field, hint !== '')}
        onChange={change(field)}
      />
      {errorFor(field)}
      {hint === '' ? null : hintFor(field, hint)}
    </div>
  );

  const pemField = (field: 'tlsCaCertificate' | 'tlsClientCertificate') => (
    <div className="form-field" data-field={field}>
      <label htmlFor={`${prefix}-${field}`}>{fieldLabel[field]}</label>
      <textarea
        id={`${prefix}-${field}`}
        name={field}
        rows={4}
        spellCheck={false}
        value={draft[field]}
        aria-invalid={errors[field] === undefined ? undefined : true}
        aria-describedby={describedBy(field, false)}
        onChange={change(field)}
      />
      {errorFor(field)}
    </div>
  );

  // A credential input is never prefilled, never shows what is stored, and
  // asks the browser not to fill it in.
  const credentialField = (field: CredentialField) => {
    const stored =
      draft.editing !== null &&
      (field === 'saslPassword'
        ? draft.editing.saslPasswordSet
        : draft.editing.tlsClientKeySet);
    const now = credentialState(draft, field);
    const hint =
      now === 'removed'
        ? 'Stored: set. It will be removed when you save.'
        : stored
          ? 'Stored: set. Leave this empty to keep it.'
          : 'Stored: not set.';
    const input =
      field === 'saslPassword' ? (
        <input
          id={`${prefix}-${field}`}
          name={field}
          type="password"
          autoComplete="new-password"
          value={draft[field]}
          aria-invalid={errors[field] === undefined ? undefined : true}
          aria-describedby={describedBy(field, true)}
          onChange={change(field)}
        />
      ) : (
        <textarea
          id={`${prefix}-${field}`}
          name={field}
          rows={4}
          spellCheck={false}
          autoComplete="new-password"
          value={draft[field]}
          aria-invalid={errors[field] === undefined ? undefined : true}
          aria-describedby={describedBy(field, true)}
          onChange={change(field)}
        />
      );
    return (
      <div
        className="form-field"
        data-field={field}
        data-stored={stored ? 'set' : 'not set'}
      >
        <label htmlFor={`${prefix}-${field}`}>{fieldLabel[field]}</label>
        {input}
        {errorFor(field)}
        {hintFor(field, hint)}
        {stored ? (
          <button
            type="button"
            onClick={() => {
              actions.toggleRemove(field);
            }}
          >
            {now === 'removed'
              ? 'Keep the stored value'
              : 'Remove the stored value'}
          </button>
        ) : null}
      </div>
    );
  };

  return (
    <form
      className="connection-form"
      aria-labelledby="connection-form-title"
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        actions.save();
      }}
    >
      <h2 id="connection-form-title">
        {draft.editing === null
          ? 'New connection'
          : `Edit ${draft.editing.name}`}
      </h2>
      {textField('name')}
      <div className="form-field" data-field="bootstrapServers">
        <label htmlFor={`${prefix}-bootstrapServers`}>
          {fieldLabel.bootstrapServers}
        </label>
        <textarea
          id={`${prefix}-bootstrapServers`}
          name="bootstrapServers"
          rows={3}
          spellCheck={false}
          value={draft.bootstrapServers}
          aria-invalid={
            errors.bootstrapServers === undefined ? undefined : true
          }
          aria-describedby={describedBy('bootstrapServers', true)}
          onChange={change('bootstrapServers')}
        />
        {errorFor('bootstrapServers')}
        {hintFor(
          'bootstrapServers',
          'One host:port per line; an IPv6 host in brackets, as [::1]:9092.',
        )}
      </div>
      <div className="form-field" data-field="securityProtocol">
        <label htmlFor={`${prefix}-securityProtocol`}>
          {fieldLabel.securityProtocol}
        </label>
        <select
          id={`${prefix}-securityProtocol`}
          name="securityProtocol"
          value={draft.securityProtocol}
          aria-invalid={
            errors.securityProtocol === undefined ? undefined : true
          }
          aria-describedby={describedBy('securityProtocol', false)}
          onChange={change('securityProtocol')}
        >
          <option value="">Choose a protocol</option>
          {securityProtocols.map((protocol) => (
            <option key={protocol} value={protocol}>
              {protocol}
            </option>
          ))}
        </select>
        {errorFor('securityProtocol')}
      </div>
      {visible.sasl ? (
        <fieldset data-group="sasl">
          <legend>SASL</legend>
          <div className="form-field" data-field="saslMechanism">
            <label htmlFor={`${prefix}-saslMechanism`}>
              {fieldLabel.saslMechanism}
            </label>
            <select
              id={`${prefix}-saslMechanism`}
              name="saslMechanism"
              value={draft.saslMechanism}
              aria-invalid={
                errors.saslMechanism === undefined ? undefined : true
              }
              aria-describedby={describedBy('saslMechanism', false)}
              onChange={change('saslMechanism')}
            >
              <option value="">Choose a mechanism</option>
              {saslMechanisms.map((mechanism) => (
                <option key={mechanism} value={mechanism}>
                  {mechanism}
                </option>
              ))}
            </select>
            {errorFor('saslMechanism')}
          </div>
          {textField('saslUsername')}
          {credentialField('saslPassword')}
        </fieldset>
      ) : null}
      {visible.tls ? (
        <fieldset data-group="tls">
          <legend>TLS</legend>
          {pemField('tlsCaCertificate')}
          {pemField('tlsClientCertificate')}
          {credentialField('tlsClientKey')}
        </fieldset>
      ) : null}
      {dropped.length === 0 ? null : (
        <p className="edit-notice" role="status" data-dropped>
          Saving with {draft.securityProtocol} removes {dropped.join(', ')}.
          Switching back later means typing them again.
        </p>
      )}
      <button type="submit" disabled={!canSave}>
        {state.saving ? 'Saving…' : 'Save'}
      </button>
      <button
        type="button"
        onClick={() => {
          actions.cancel();
        }}
      >
        Cancel
      </button>
    </form>
  );
}
