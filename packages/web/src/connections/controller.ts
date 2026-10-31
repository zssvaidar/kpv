// The connection screen's state and every request it makes (#24), apart from
// React, so it is tested in Node against the real typed client.
//
//   - The list is read again on every refresh and after every change.
//   - A save sends only what connectionRequest builds from a valid form, and
//     clears the typed password and key from the form the moment the request
//     is sent, whatever comes back: the typed value is not kept here.
//   - A delete or rename that pipelines name, or that could not be checked,
//     comes back referenced; the report is shown and nothing is sent again
//     until the person confirms it.
//   - A request that is rejected, unanswered or not understood is a
//     connection problem, said in #8's words.

import type { ConnectionSummary, ConnectionsError } from '@kpv/schema';

import type { AgentClient, AgentResult } from '../api/client.ts';
import type { RequestProblem } from '../connection/describe.ts';
import {
  connectionRequest,
  draftFromSummary,
  emptyConnectionDraft,
  refusalIssues,
  withoutTypedCredentials,
  type ConnectionDraft,
  type ConnectionField,
  type CredentialField,
} from './form.ts';

/** A delete or rename waiting for the person to confirm it. */
export interface PendingReport {
  action: 'delete' | 'rename';
  name: string;
  error: ConnectionsError;
  /** Credentials typed for the refused save, cleared, to type again. */
  retype: CredentialField[];
}

export interface ConnectionsState {
  session: boolean;
  online: boolean;
  list: ConnectionSummary[] | null;
  loading: boolean;
  /** The last request's connection problem, until one succeeds. */
  problem: RequestProblem | null;
  /** The form, when it is open. */
  draft: ConnectionDraft | null;
  saving: boolean;
  /** The agent's refusal of the last save, by field. */
  refusal: Partial<Record<ConnectionField, string>>;
  /** A refusal that is about no field: not found, io. */
  failure: ConnectionsError | null;
  report: PendingReport | null;
}

export interface ConnectionsActions {
  refresh(): void;
  startCreate(): void;
  startEdit(name: string): void;
  edit(field: ConnectionField, value: string): void;
  /** Marks a stored credential to be removed, or keeps it again. */
  toggleRemove(field: CredentialField): void;
  cancel(): void;
  save(): void;
  remove(name: string): void;
  confirm(): void;
  cancelReport(): void;
}

export interface ConnectionsController extends ConnectionsActions {
  current: () => ConnectionsState;
  subscribe: (listener: () => void) => () => void;
  setOnline(online: boolean): void;
}

function problemOf<T, E>(result: AgentResult<T, E>): RequestProblem | null {
  switch (result.kind) {
    case 'rejected':
    case 'unreachable':
      return { kind: result.kind };
    case 'failed':
      return { kind: 'failed', status: result.status };
    default:
      return null;
  }
}

const typedCredentials = (draft: ConnectionDraft): CredentialField[] =>
  (['saslPassword', 'tlsClientKey'] as const).filter(
    (field) => draft[field] !== '',
  );

export function createConnectionsController(
  client: AgentClient | null,
): ConnectionsController {
  let state: ConnectionsState = {
    session: client !== null,
    online: false,
    list: null,
    loading: false,
    problem: null,
    draft: null,
    saving: false,
    refusal: {},
    failure: null,
    report: null,
  };
  const listeners = new Set<() => void>();
  const set = (next: Partial<ConnectionsState>) => {
    state = { ...state, ...next };
    for (const listener of listeners) listener();
  };
  let readSequence = 0;

  const refresh = () => {
    if (client === null || !state.online) return;
    const sequence = ++readSequence;
    set({ loading: true });
    void client.listConnections().then((result) => {
      if (sequence !== readSequence) return;
      if (result.kind === 'ok') {
        set({ loading: false, list: result.value.connections, problem: null });
      } else if (result.kind === 'error') {
        set({ loading: false, failure: result.error, problem: null });
      } else {
        set({ loading: false, problem: problemOf(result) });
      }
    });
  };

  const save = (confirm: boolean) => {
    if (client === null || !state.online || state.saving) return;
    const draft = state.draft;
    if (draft === null) return;
    const built = connectionRequest(draft, confirm);
    if (built === null) return;
    const retype = typedCredentials(draft);
    // The typed credentials leave the form as the request leaves the page.
    set({
      saving: true,
      draft: withoutTypedCredentials(draft),
      refusal: {},
      failure: null,
      report: null,
    });
    const sent =
      built.kind === 'create'
        ? client.createConnection(built.request)
        : client.updateConnection(built.request);
    void sent.then((result) => {
      if (result.kind === 'ok') {
        set({ saving: false, draft: null, problem: null });
        refresh();
        return;
      }
      if (result.kind === 'error') {
        if (result.error.error === 'referenced') {
          set({
            saving: false,
            report: {
              action: 'rename',
              name: draft.editing?.name ?? draft.name,
              error: result.error,
              retype,
            },
          });
          return;
        }
        const refusal = refusalIssues(result.error);
        set({
          saving: false,
          refusal,
          failure: Object.keys(refusal).length === 0 ? result.error : null,
        });
        return;
      }
      set({ saving: false, problem: problemOf(result) });
    });
  };

  const remove = (name: string, confirm: boolean) => {
    if (client === null || !state.online || state.saving) return;
    set({ saving: true, failure: null, report: null });
    void client
      .deleteConnection(confirm ? { name, confirm: true } : { name })
      .then((result) => {
        if (result.kind === 'ok') {
          const editing = state.draft?.editing?.name === name;
          set({
            saving: false,
            list: result.value.connections,
            problem: null,
            ...(editing ? { draft: null } : {}),
          });
          return;
        }
        if (result.kind === 'error') {
          if (result.error.error === 'referenced') {
            set({
              saving: false,
              report: {
                action: 'delete',
                name,
                error: result.error,
                retype: [],
              },
            });
            return;
          }
          set({ saving: false, failure: result.error });
          refresh();
          return;
        }
        set({ saving: false, problem: problemOf(result) });
      });
  };

  return {
    current: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    setOnline(online) {
      if (online === state.online) return;
      set({ online });
      if (online) refresh();
      else {
        readSequence++;
        set({ loading: false });
      }
    },
    refresh,
    startCreate() {
      set({
        draft: emptyConnectionDraft(),
        refusal: {},
        failure: null,
        report: null,
      });
    },
    startEdit(name) {
      const summary = state.list?.find((entry) => entry.name === name);
      if (summary === undefined) return;
      set({
        draft: draftFromSummary(summary),
        refusal: {},
        failure: null,
        report: null,
      });
    },
    edit(field, value) {
      if (state.draft === null) return;
      const refusal = { ...state.refusal };
      delete refusal[field];
      const next: ConnectionDraft = { ...state.draft, [field]: value };
      if (field === 'saslPassword' || field === 'tlsClientKey') {
        next.remove = next.remove.filter((entry) => entry !== field);
      }
      set({ draft: next, refusal });
    },
    toggleRemove(field) {
      if (state.draft === null) return;
      const removing = state.draft.remove.includes(field);
      set({
        draft: {
          ...state.draft,
          [field]: '',
          remove: removing
            ? state.draft.remove.filter((entry) => entry !== field)
            : [...state.draft.remove, field],
        },
      });
    },
    cancel() {
      set({ draft: null, refusal: {}, failure: null, report: null });
    },
    save() {
      save(false);
    },
    remove(name) {
      remove(name, false);
    },
    confirm() {
      const report = state.report;
      if (report === null) return;
      if (report.action === 'delete') remove(report.name, true);
      else save(true);
    },
    cancelReport() {
      set({ report: null });
    },
  };
}
