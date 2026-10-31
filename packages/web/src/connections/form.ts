// The Kafka connection form's model (#24), apart from React, so it is tested
// in Node.
//
//   - What a person types is a ConnectionDraft of plain strings. Whether it
//     is valid is decided by connectionIssues from @kpv/schema, the generated
//     Zod plus the combination rules: nothing here restates a rule. Which
//     fields a protocol shows is protocolAllowsSasl and protocolAllowsTls
//     from there too.
//   - The password and the client key are never prefilled. A stored one is
//     only "set" (the summary's saslPasswordSet, tlsClientKeySet); leaving
//     the input empty keeps it, typing replaces it, and removing is its own
//     action. For the check as you type, a kept credential stands in as
//     present (keptStandIn), and the stand-in is never sent.
//   - A protocol that drops the SASL fields or the TLS fields drops what is
//     stored of them on save, and droppedOnSave says what, before saving.

import type {
  Connection,
  ConnectionSummary,
  ConnectionUpdate,
  ConnectionsError,
  SaslMechanism,
  SecurityProtocol,
} from '@kpv/schema';
import {
  connectionIssues,
  protocolAllowsSasl,
  protocolAllowsTls,
} from '@kpv/schema';

/** The fields of the form, by the name the schema gives each. */
export type ConnectionField =
  | 'name'
  | 'bootstrapServers'
  | 'securityProtocol'
  | 'saslMechanism'
  | 'saslUsername'
  | 'saslPassword'
  | 'tlsCaCertificate'
  | 'tlsClientCertificate'
  | 'tlsClientKey';

/** The two credentials, which are write-only. */
export type CredentialField = 'saslPassword' | 'tlsClientKey';

export const securityProtocols: readonly SecurityProtocol[] = [
  'PLAINTEXT',
  'SSL',
  'SASL_PLAINTEXT',
  'SASL_SSL',
];

export const saslMechanisms: readonly SaslMechanism[] = [
  'PLAIN',
  'SCRAM-SHA-256',
  'SCRAM-SHA-512',
];

/** What the form holds while a person types. */
export interface ConnectionDraft {
  /** The connection being edited, `null` for a new one. */
  editing: ConnectionSummary | null;
  name: string;
  /** One bootstrap server per line. */
  bootstrapServers: string;
  /** '' until one is chosen: nothing is preselected. */
  securityProtocol: SecurityProtocol | '';
  saslMechanism: SaslMechanism | '';
  saslUsername: string;
  tlsCaCertificate: string;
  tlsClientCertificate: string;
  /** What was typed into a credential input, never a stored value. */
  saslPassword: string;
  tlsClientKey: string;
  /** Credentials the person asked to remove from the stored connection. */
  remove: CredentialField[];
}

/** An empty form for a new connection. */
export function emptyConnectionDraft(): ConnectionDraft {
  return {
    editing: null,
    name: '',
    bootstrapServers: '',
    securityProtocol: '',
    saslMechanism: '',
    saslUsername: '',
    tlsCaCertificate: '',
    tlsClientCertificate: '',
    saslPassword: '',
    tlsClientKey: '',
    remove: [],
  };
}

/** The form for a stored connection: everything but the credentials. */
export function draftFromSummary(summary: ConnectionSummary): ConnectionDraft {
  return {
    editing: summary,
    name: summary.name,
    bootstrapServers: summary.bootstrapServers.join('\n'),
    securityProtocol: summary.securityProtocol,
    saslMechanism: summary.saslMechanism ?? '',
    saslUsername: summary.saslUsername ?? '',
    tlsCaCertificate: summary.tlsCaCertificate ?? '',
    tlsClientCertificate: summary.tlsClientCertificate ?? '',
    saslPassword: '',
    tlsClientKey: '',
    remove: [],
  };
}

/** Which groups of fields the chosen protocol shows. */
export function visibleGroups(draft: ConnectionDraft): {
  sasl: boolean;
  tls: boolean;
} {
  if (draft.securityProtocol === '') return { sasl: false, tls: false };
  return {
    sasl: protocolAllowsSasl(draft.securityProtocol),
    tls: protocolAllowsTls(draft.securityProtocol),
  };
}

/** The stand-in for a kept credential in the check as you type. */
const keptStandIn = 'kept';

/** Whether a credential stored in the connection is set. */
const storedSet = (draft: ConnectionDraft, field: CredentialField): boolean =>
  draft.editing !== null &&
  (field === 'saslPassword'
    ? draft.editing.saslPasswordSet
    : draft.editing.tlsClientKeySet);

/** What a credential will be after a save: typed, kept, or nothing. */
export function credentialState(
  draft: ConnectionDraft,
  field: CredentialField,
): 'typed' | 'kept' | 'removed' | 'none' {
  const visible = visibleGroups(draft);
  const shown = field === 'saslPassword' ? visible.sasl : visible.tls;
  if (!shown) return storedSet(draft, field) ? 'removed' : 'none';
  if (draft[field] !== '') return 'typed';
  if (!storedSet(draft, field)) return 'none';
  return draft.remove.includes(field) ? 'removed' : 'kept';
}

/** One bootstrap server per non-empty line, as typed. */
const servers = (text: string): string[] =>
  text
    .split('\n')
    .map((line) => line.replace(/\r$/, ''))
    .filter((line) => line.trim() !== '');

/**
 * The record the form describes, as the schema sees it: only the fields the
 * protocol shows, and an empty optional field left out. Credentials are the
 * typed text, or the stand-in for a kept one when `forCheck` is set.
 */
function recordOf(
  draft: ConnectionDraft,
  forCheck: boolean,
): Record<string, unknown> {
  const visible = visibleGroups(draft);
  const record: Record<string, unknown> = {
    name: draft.name,
    bootstrapServers: servers(draft.bootstrapServers),
  };
  if (draft.securityProtocol !== '') {
    record['securityProtocol'] = draft.securityProtocol;
  }
  const optional = (field: ConnectionField, value: string) => {
    if (value !== '') record[field] = value;
  };
  if (visible.sasl) {
    optional('saslMechanism', draft.saslMechanism);
    optional('saslUsername', draft.saslUsername);
  }
  if (visible.tls) {
    optional('tlsCaCertificate', draft.tlsCaCertificate);
    optional('tlsClientCertificate', draft.tlsClientCertificate);
  }
  for (const field of ['saslPassword', 'tlsClientKey'] as const) {
    const state = credentialState(draft, field);
    if (state === 'typed') record[field] = draft[field];
    if (state === 'kept' && forCheck) record[field] = keptStandIn;
  }
  return record;
}

/**
 * A pattern the schema refuses a field by, in words: Zod's own message
 * quotes the regular expression. The words describe the schema's pattern;
 * the pattern itself is only ever checked by the schema.
 */
const patternWords: Partial<Record<ConnectionField, string>> = {
  name: 'A name is a letter or digit, then letters, digits, dots, underscores or hyphens, at most 64 characters.',
  bootstrapServers:
    'Each line is host:port with a port from 1 to 65535, an IPv6 host in brackets.',
};

const worded = (field: ConnectionField, message: string): string =>
  message.includes('must match pattern')
    ? (patternWords[field] ?? 'This is not in the expected form.')
    : message;

/** Every problem with the form, by field: the schema and the rules. */
export function draftIssues(
  draft: ConnectionDraft,
): Partial<Record<ConnectionField, string>> {
  const found: Partial<Record<ConnectionField, string>> = {};
  for (const issue of connectionIssues(recordOf(draft, true))) {
    const field = fieldOf(issue.pointer);
    if (found[field] === undefined) found[field] = worded(field, issue.message);
  }
  return found;
}

/** The field a JSON Pointer from the schema or the agent is about. */
export function fieldOf(pointer: string): ConnectionField {
  const first = pointer.split('/')[1] ?? '';
  const fields: readonly string[] = [
    'name',
    'bootstrapServers',
    'securityProtocol',
    'saslMechanism',
    'saslUsername',
    'saslPassword',
    'tlsCaCertificate',
    'tlsClientCertificate',
    'tlsClientKey',
  ];
  return fields.includes(first) ? (first as ConnectionField) : 'name';
}

/**
 * What a save drops of the stored connection because the chosen protocol
 * does not carry it, said before saving. Switching back means typing it
 * again.
 */
export function droppedOnSave(draft: ConnectionDraft): string[] {
  if (draft.editing === null || draft.securityProtocol === '') return [];
  const visible = visibleGroups(draft);
  const stored = draft.editing;
  const dropped: string[] = [];
  if (!visible.sasl) {
    if (stored.saslMechanism !== undefined) dropped.push('the SASL mechanism');
    if (stored.saslUsername !== undefined) dropped.push('the SASL username');
    if (stored.saslPasswordSet) dropped.push('the stored SASL password');
  }
  if (!visible.tls) {
    if (stored.tlsCaCertificate !== undefined)
      dropped.push('the CA certificate');
    if (stored.tlsClientCertificate !== undefined)
      dropped.push('the client certificate');
    if (stored.tlsClientKeySet) dropped.push('the stored client key');
  }
  return dropped;
}

/**
 * The request a valid form sends: a create, or an update of the connection
 * being edited that leaves a kept credential out and lists a removed one.
 * `null` while the form has a problem, so an invalid form cannot be saved.
 */
export function connectionRequest(
  draft: ConnectionDraft,
  confirm = false,
):
  | { kind: 'create'; request: Connection }
  | { kind: 'update'; request: ConnectionUpdate }
  | null {
  if (Object.keys(draftIssues(draft)).length > 0) return null;
  const record = recordOf(draft, false);
  if (draft.editing === null) {
    return { kind: 'create', request: record as unknown as Connection };
  }
  const remove = (['saslPassword', 'tlsClientKey'] as const).filter(
    (field) =>
      credentialState(draft, field) === 'removed' &&
      draft.remove.includes(field),
  );
  const update: Record<string, unknown> = {
    current: draft.editing.name,
    ...record,
  };
  if (remove.length > 0) update['remove'] = remove;
  if (confirm) update['confirm'] = true;
  return { kind: 'update', request: update as unknown as ConnectionUpdate };
}

/**
 * The form after a request was sent, successful or not: the typed
 * credentials are cleared, everything else is kept.
 */
export function withoutTypedCredentials(
  draft: ConnectionDraft,
): ConnectionDraft {
  return { ...draft, saslPassword: '', tlsClientKey: '' };
}

/**
 * The agent's refusal of a save, by field: an invalid request's problems at
 * the fields they name, a duplicate name at the name.
 */
export function refusalIssues(
  error: ConnectionsError,
): Partial<Record<ConnectionField, string>> {
  const found: Partial<Record<ConnectionField, string>> = {};
  if (error.error === 'duplicateName') {
    found.name = error.message;
  }
  for (const problem of error.problems ?? []) {
    const field = fieldOf(problem.pointer);
    if (found[field] === undefined) found[field] = problem.message;
  }
  return found;
}
