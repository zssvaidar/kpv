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
//     succeeds again. It is the picker's own request's problem: while the
//     channel is up the page says so, and when the channel goes the problem is
//     dropped, since the reconnect re-read decides what is true then.
//
// An authorize or revoke that was not answered readably is "unconfirmed"
// until a roots read started after it succeeds; that read settles it, in
// words that claim no more than the agent's list shows (#66).

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
  listingGoneNote,
  rootsOutcomeSentence,
  truncationNote,
} from './words.ts';

/** A browse that failed with the endpoint's own error. */
export interface BrowseFailure {
  /** The path asked for; `null` for the home directory. */
  path: string | null;
  error: DirectoryError;
}

export type RootsAction = 'authorize' | 'revoke';

/**
 * Why an authorize or revoke is not known to have happened:
 *   - `dropped`: the connection went before the agent answered;
 *   - `unreachable`: the request got no answer while the channel was up;
 *   - `failed`: the agent answered, in a form the page could not read.
 */
export type UnconfirmedReason =
  | { kind: 'dropped' }
  | { kind: 'unreachable' }
  | { kind: 'failed'; status: number };

/** The outcome of the last authorize or revoke, until the next one. */
export type RootsOutcome =
  | { kind: 'authorized'; path: string }
  | { kind: 'revoked'; path: string }
  | {
      kind: 'refused';
      action: RootsAction;
      path: string;
      error: RootsError;
    }
  /** The uniform 404: rejected before the route ran, so nothing changed. */
  | { kind: 'rejected'; action: RootsAction; path: string }
  /** Not known to have happened, until a later roots read settles it. */
  | {
      kind: 'unconfirmed';
      action: RootsAction;
      path: string;
      reason: UnconfirmedReason;
    }
  /** An unconfirmed outcome, settled by whether the agent's list has `path`. */
  | { kind: 'settled'; action: RootsAction; path: string; listed: boolean };

export interface PickerState {
  /** Whether the connection monitor says the agent is connected. */
  online: boolean;
  /** The last directory listed successfully, `null` before the first. */
  listing: DirectoryListing | null;
  /** The browse being waited for: a path, or `null` for home. */
  browsing: { path: string | null } | null;
  /** The last browse's own error, until a browse succeeds. */
  browseError: BrowseFailure | null;
  /**
   * The failure of the latest browse of the listed directory's own path, until
   * a browse succeeds: what is listed may be gone, so it is not offered for
   * authorizing. A failed browse of another path leaves it as it is.
   */
  listingFailure: BrowseFailure | null;
  /** The authorized roots, `null` until first read. */
  roots: ProjectRoot[] | null;
  /** The roots list's own error, until it is read successfully. */
  rootsError: RootsError | null;
  /** A request the agent rejected, did not answer or answered unreadably. */
  problem: RequestProblem | null;
  /** The authorize or revoke in flight; only one at a time. */
  busy: { action: RootsAction; path: string } | null;
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

/** Whether the listed directory could not be listed again just now. */
export function listingGone(state: PickerState): boolean {
  return (
    state.listing !== null &&
    state.listingFailure !== null &&
    state.listingFailure.path === state.listing.path
  );
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
    listingFailure: null,
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
  // The roots read count when the current unconfirmed outcome was set: only a
  // read started after it may settle it.
  let unconfirmedAt = 0;
  // The problem of the last roots read, until a roots read succeeds: a browse
  // that succeeds in the meantime says nothing about the roots list, so it
  // does not clear it.
  let rootsProblem: RequestProblem | null = null;

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
      // A re-read landing after the roots list settled an outcome keeps the
      // settled sentence in the announcement rather than replacing it.
      const settled =
        !byUser &&
        state.outcome?.kind === 'settled' &&
        state.announcement === rootsOutcomeSentence(state.outcome)
          ? ` ${state.announcement}`
          : '';
      set({
        listing,
        browsing: null,
        browseError: null,
        listingFailure: null,
        problem: rootsProblem,
        focusTick: byUser ? state.focusTick + 1 : state.focusTick,
        announcement: describeListing(listing) + settled,
      });
      return;
    }
    if (result.kind === 'error') {
      const words = directoryErrorWords[result.error.error];
      const failure = { path, error: result.error };
      const own = path !== null && path === state.listing?.path;
      set({
        browsing: null,
        browseError: failure,
        problem: rootsProblem,
        ...(own ? { listingFailure: failure } : {}),
        announcement: own
          ? `${words.title}. ${result.error.message} ${listingGoneNote(path, result.error.error)}`
          : `${words.title}. ${result.error.message}`,
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
      case 'ok': {
        const roots = result.value.roots;
        const outcome = state.outcome;
        if (outcome?.kind === 'unconfirmed' && seq > unconfirmedAt) {
          const settled: RootsOutcome = {
            kind: 'settled',
            action: outcome.action,
            path: outcome.path,
            listed: roots.some((root) => root.path === outcome.path),
          };
          rootsProblem = null;
          set({
            roots,
            rootsError: null,
            problem: null,
            outcome: settled,
            announcement: rootsOutcomeSentence(settled),
          });
          return;
        }
        rootsProblem = null;
        set({ roots, rootsError: null, problem: null });
        return;
      }
      case 'error':
        set({ rootsError: result.error });
        return;
      default:
        rootsProblem = linkProblem(result);
        set({ problem: rootsProblem });
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
        const outcome: RootsOutcome = { kind: 'authorized', path: authorized };
        set({
          busy: null,
          outcome,
          roots: roots.some((root) => root.path === authorized)
            ? roots
            : [...roots, { path: authorized, available: true }],
          announcement: rootsOutcomeSentence(outcome),
        });
        void loadRoots();
        return;
      }
      case 'error': {
        const outcome: RootsOutcome = {
          kind: 'refused',
          action: 'authorize',
          path,
          error: result.error,
        };
        set({
          busy: null,
          outcome,
          announcement: rootsOutcomeSentence(outcome),
        });
        return;
      }
      default:
        unanswered('authorize', path, result);
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
      case 'ok': {
        rootsSeq++;
        const outcome: RootsOutcome = { kind: 'revoked', path };
        set({
          busy: null,
          roots: result.value.roots,
          rootsError: null,
          outcome,
          announcement: rootsOutcomeSentence(outcome),
        });
        return;
      }
      case 'error': {
        const outcome: RootsOutcome = {
          kind: 'refused',
          action: 'revoke',
          path,
          error: result.error,
        };
        set({
          busy: null,
          outcome,
          announcement: rootsOutcomeSentence(outcome),
        });
        // Most often another tab revoked it first: show what the agent has.
        void loadRoots();
        return;
      }
      default:
        unanswered('revoke', path, result);
    }
  }

  /** Sets an outcome that only a roots read started after it may settle. */
  function unconfirmed(
    action: RootsAction,
    path: string,
    reason: UnconfirmedReason,
    next: Partial<PickerState> = {},
  ) {
    unconfirmedAt = rootsSeq;
    const outcome: RootsOutcome = { kind: 'unconfirmed', action, path, reason };
    set({
      ...next,
      busy: null,
      outcome,
      announcement: rootsOutcomeSentence(outcome),
    });
  }

  /** An authorize or revoke answered with no answer of its own. */
  function unanswered(
    action: RootsAction,
    path: string,
    result: AgentResult<unknown, unknown>,
  ) {
    const problem = linkProblem(result);
    switch (result.kind) {
      case 'rejected': {
        const outcome: RootsOutcome = { kind: 'rejected', action, path };
        set({
          busy: null,
          outcome,
          problem,
          announcement: rootsOutcomeSentence(outcome),
        });
        return;
      }
      case 'failed':
        unconfirmed(
          action,
          path,
          { kind: 'failed', status: result.status },
          { problem },
        );
        return;
      default:
        unconfirmed(action, path, { kind: 'unreachable' }, { problem });
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
        // The picker's own request problem is dropped with the connection:
        // the re-read on reconnect decides what is shown after it.
        rootsProblem = null;
        const next = { online: false, browsing: null, problem: null };
        if (busy === null) {
          set(next);
        } else {
          unconfirmed(busy.action, busy.path, { kind: 'dropped' }, next);
        }
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
