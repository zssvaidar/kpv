// The directory picker's state and every request it makes, apart from React,
// so it is tested in Node against the real typed client with an injected
// fetch.
//
// Three rules shape it:
//
//   - The latest browse wins. Every browse takes a sequence number, and an
//     answer is applied only if no browse was started after it. A late answer
//     for a directory the user already left never replaces the one they are
//     in. The roots list is read the same way.
//
//   - Nothing is assumed across a dropped connection. While the page is not
//     connected, what is on screen is the last known listing and roots list,
//     marked as such, and authorizing and revoking are off. Going offline
//     invalidates every request in flight; an authorize or revoke in flight is
//     reported as unconfirmed, never as done, and its late answer is dropped.
//     On reconnect the current directory and the roots list are read again.
//
//   - A request that is rejected, unanswered or not understood is a connection
//     problem, not a picker error. It is reported in #8's words (see
//     describeRequestProblem) and treated like being offline until a request
//     succeeds again.

import type {
  DirectoryError,
  DirectoryListing,
  ProjectRoot,
  RootsError,
} from '@kpv/schema';

import type { AgentClient, AgentResult } from '../api/client.ts';
import type { RequestProblem } from '../connection/describe.ts';
import {
  directoryErrorWords,
  rootsErrorWords,
  truncationNote,
} from './words.ts';

/** A browse that failed with the endpoint's own error. */
export interface BrowseFailure {
  /** The path asked for; `null` for the home directory. */
  path: string | null;
  error: DirectoryError;
}

/** The outcome of the last authorize or revoke, until the next one. */
export type RootsOutcome =
  | { kind: 'authorized'; path: string }
  | { kind: 'revoked'; path: string }
  | {
      kind: 'refused';
      action: 'authorize' | 'revoke';
      path: string;
      error: RootsError;
    }
  /** The connection went before the agent answered: not known to have happened. */
  | { kind: 'unconfirmed'; action: 'authorize' | 'revoke'; path: string };

export interface PickerState {
  /** Whether the connection monitor says the agent is connected. */
  online: boolean;
  /** The last directory listed successfully, `null` before the first. */
  listing: DirectoryListing | null;
  /** The browse being waited for: a path, or `null` for home. */
  browsing: { path: string | null } | null;
  /** The last browse's own error, until a browse succeeds. */
  browseError: BrowseFailure | null;
  /** The authorized roots, `null` until first read. */
  roots: ProjectRoot[] | null;
  /** The roots list's own error, until it is read successfully. */
  rootsError: RootsError | null;
  /** A request the agent rejected, did not answer or answered unreadably. */
  problem: RequestProblem | null;
  /** The authorize or revoke in flight; only one at a time. */
  busy: { action: 'authorize' | 'revoke'; path: string } | null;
  outcome: RootsOutcome | null;
  /** The sentence the live region reads out for the latest outcome. */
  announcement: string;
  /** Bumped when a directory the user opened arrives, to move focus to it. */
  focusTick: number;
}

export interface PickerController {
  current: () => PickerState;
  subscribe: (listener: () => void) => () => void;
  /** Tells the picker whether the connection is up; it reads on the way up. */
  setOnline(online: boolean): void;
  /** Lists `path`, which the user opened, typed or picked. */
  open(path: string): void;
  /** Lists the home directory. */
  home(): void;
  /** Reads the current directory and the roots list again. */
  refresh(): void;
  /** Authorizes the current directory. */
  authorize(): void;
  /** Revokes the root at `path`. */
  revoke(path: string): void;
}

/** Whether what is on screen is only the last known state. */
export function isLastKnown(state: PickerState): boolean {
  return !state.online || state.problem !== null;
}

/** Whether authorize and revoke may be offered at all right now. */
export function canChangeRoots(state: PickerState): boolean {
  return !isLastKnown(state) && state.busy === null;
}

/** The stored root at exactly the listed path, if there is one. */
export function rootAt(
  state: PickerState,
  path: string,
): ProjectRoot | undefined {
  return state.roots?.find((root) => root.path === path);
}

export function createPickerController(client: AgentClient): PickerController {
  const listeners = new Set<() => void>();
  let state: PickerState = {
    online: false,
    listing: null,
    browsing: null,
    browseError: null,
    roots: null,
    rootsError: null,
    problem: null,
    busy: null,
    outcome: null,
    announcement: '',
    focusTick: 0,
  };
  let browseSeq = 0;
  let rootsSeq = 0;
  // Bumped whenever the connection goes, so an answer that comes back after
  // it is not trusted, whatever it says.
  let epoch = 0;
  let started = false;

  function set(next: Partial<PickerState>) {
    state = { ...state, ...next };
    for (const listener of listeners) {
      listener();
    }
  }

  function linkProblem(
    result: AgentResult<unknown, unknown>,
  ): RequestProblem | null {
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

  async function browse(path: string | null, byUser: boolean) {
    const seq = ++browseSeq;
    set({ browsing: { path } });
    const result = await client.listDirectories(path === null ? {} : { path });
    if (seq !== browseSeq) {
      return;
    }
    if (result.kind === 'ok') {
      const listing = result.value;
      set({
        listing,
        browsing: null,
        browseError: null,
        problem: null,
        focusTick: byUser ? state.focusTick + 1 : state.focusTick,
        announcement: describeListing(listing),
      });
      return;
    }
    if (result.kind === 'error') {
      const words = directoryErrorWords[result.error.error];
      set({
        browsing: null,
        browseError: { path, error: result.error },
        problem: null,
        announcement: `${words.title}. ${result.error.message}`,
      });
      return;
    }
    set({
      browsing: null,
      problem: linkProblem(result),
      announcement: 'The agent did not list the directory.',
    });
  }

  async function loadRoots() {
    const seq = ++rootsSeq;
    const result = await client.listRoots();
    if (seq !== rootsSeq) {
      return;
    }
    switch (result.kind) {
      case 'ok':
        set({ roots: result.value.roots, rootsError: null, problem: null });
        return;
      case 'error':
        set({ rootsError: result.error });
        return;
      default:
        set({ problem: linkProblem(result) });
    }
  }

  function refresh() {
    void browse(state.listing?.path ?? null, false);
    void loadRoots();
  }

  async function authorize() {
    const listing = state.listing;
    if (listing === null || !canChangeRoots(state)) {
      return;
    }
    const path = listing.path;
    const asked = epoch;
    set({ busy: { action: 'authorize', path }, outcome: null });
    const result = await client.authorizeRoot(path);
    if (asked !== epoch) {
      return;
    }
    switch (result.kind) {
      case 'ok': {
        const authorized = result.value.path;
        // Shown at once, then the list is read from the agent to confirm.
        const roots = state.roots ?? [];
        set({
          busy: null,
          outcome: { kind: 'authorized', path: authorized },
          roots: roots.some((root) => root.path === authorized)
            ? roots
            : [...roots, { path: authorized, available: true }],
          announcement: `Authorized ${authorized}.`,
        });
        void loadRoots();
        return;
      }
      case 'error':
        set({
          busy: null,
          outcome: {
            kind: 'refused',
            action: 'authorize',
            path,
            error: result.error,
          },
          announcement: `Not authorized. ${rootsErrorWords[result.error.error].title}. ${result.error.message}`,
        });
        return;
      default:
        set({
          busy: null,
          outcome: { kind: 'unconfirmed', action: 'authorize', path },
          problem: linkProblem(result),
          announcement: unconfirmed('authorize', path),
        });
    }
  }

  async function revoke(path: string) {
    if (!canChangeRoots(state)) {
      return;
    }
    const asked = epoch;
    set({ busy: { action: 'revoke', path }, outcome: null });
    const result = await client.revokeRoot(path);
    if (asked !== epoch) {
      return;
    }
    switch (result.kind) {
      case 'ok':
        rootsSeq++;
        set({
          busy: null,
          roots: result.value.roots,
          rootsError: null,
          outcome: { kind: 'revoked', path },
          announcement: `Revoked ${path}. Nothing on disk was deleted.`,
        });
        return;
      case 'error':
        set({
          busy: null,
          outcome: {
            kind: 'refused',
            action: 'revoke',
            path,
            error: result.error,
          },
          announcement: `Not revoked. ${rootsErrorWords[result.error.error].title}. ${result.error.message}`,
        });
        // Most often another tab revoked it first: show what the agent has.
        void loadRoots();
        return;
      default:
        set({
          busy: null,
          outcome: { kind: 'unconfirmed', action: 'revoke', path },
          problem: linkProblem(result),
          announcement: unconfirmed('revoke', path),
        });
    }
  }

  return {
    current: () => state,
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    setOnline(online) {
      if (online === state.online) {
        return;
      }
      if (!online) {
        epoch++;
        browseSeq++;
        rootsSeq++;
        const busy = state.busy;
        set({
          online: false,
          browsing: null,
          busy: null,
          ...(busy === null
            ? {}
            : {
                outcome: { kind: 'unconfirmed', ...busy },
                announcement: unconfirmed(busy.action, busy.path),
              }),
        });
        return;
      }
      set({ online: true });
      if (started) {
        refresh();
        return;
      }
      started = true;
      void browse(null, false);
      void loadRoots();
    },
    open(path) {
      void browse(path, true);
    },
    home() {
      void browse(null, true);
    },
    refresh,
    authorize() {
      void authorize();
    },
    revoke(path) {
      void revoke(path);
    },
  };
}

function unconfirmed(action: 'authorize' | 'revoke', path: string): string {
  const verb = action === 'authorize' ? 'Authorizing' : 'Revoking';
  return `${verb} ${path} was not confirmed: the connection to the agent went before it answered. The list of authorized directories will be read again from the agent.`;
}

/** What the live region says when a directory is listed. */
export function describeListing(listing: DirectoryListing): string {
  const count = listing.entries.length;
  const entries = count === 1 ? '1 entry' : `${count} entries`;
  const parts = [`Listed ${listing.path}: ${entries}.`];
  if (listing.looksLikeProject) {
    parts.push('It looks like a project.');
  }
  if (listing.truncated) {
    parts.push(truncationNote(count));
  }
  return parts.join(' ');
}
