// The names the Kafka form's connection select offers (#12): what
// `GET /api/connections/names` (#24) last answered, read through the one
// client, apart from React so it is tested in Node.
//
//   - The names are read again whenever a Kafka block's form is shown, so a
//     connection made, renamed or deleted on the connection screen is what
//     the select offers next.
//   - A failed read keeps the names last read, and says why; with none read
//     yet, the form cannot tell a defined name from a dangling one and says
//     so rather than guessing.
//   - Only the names come back: the answer has no credential field to hold.

import type { ConnectionsError } from '@kpv/schema';

import type { AgentClient } from '../api/client.ts';
import type { RequestProblem } from '../connection/describe.ts';

export interface ConnectionNamesState {
  /** The names as the agent last listed them, or `null` before any read. */
  names: readonly string[] | null;
  loading: boolean;
  /** The last read's connection problem, until one succeeds. */
  problem: RequestProblem | null;
  /** The endpoint's own refusal of the last read, until one succeeds. */
  failure: ConnectionsError | null;
}

export interface ConnectionNamesController {
  current: () => ConnectionNamesState;
  subscribe: (listener: () => void) => () => void;
  setOnline(online: boolean): void;
  refresh(): void;
}

/** The state of a form shown with no names to offer and none being read. */
export const noConnectionNames: ConnectionNamesState = {
  names: null,
  loading: false,
  problem: null,
  failure: null,
};

export function createConnectionNamesController(
  client: AgentClient | null,
): ConnectionNamesController {
  let state: ConnectionNamesState = noConnectionNames;
  let online = false;
  let sequence = 0;
  const listeners = new Set<() => void>();
  const set = (next: Partial<ConnectionNamesState>) => {
    state = { ...state, ...next };
    for (const listener of listeners) listener();
  };

  const refresh = () => {
    if (client === null || !online) return;
    const read = ++sequence;
    set({ loading: true });
    void client.listConnectionNames().then((result) => {
      if (read !== sequence) return;
      switch (result.kind) {
        case 'ok':
          set({
            names: result.value.names,
            loading: false,
            problem: null,
            failure: null,
          });
          return;
        case 'error':
          set({ loading: false, problem: null, failure: result.error });
          return;
        case 'failed':
          set({
            loading: false,
            problem: { kind: 'failed', status: result.status },
            failure: null,
          });
          return;
        default:
          set({
            loading: false,
            problem: { kind: result.kind },
            failure: null,
          });
      }
    });
  };

  return {
    current: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    setOnline(next) {
      if (next === online) return;
      online = next;
      if (online) refresh();
      else {
        sequence++;
        if (state.loading) set({ loading: false });
      }
    },
    refresh,
  };
}
