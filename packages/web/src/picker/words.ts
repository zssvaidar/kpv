// What the directory picker says about each thing the agent can answer. One
// module, so every kind has words, a test can hold them apart, and the picker
// branches on the kind the agent sent and never on its message: the agent's
// messages are fixed sentences that are shown as they are, beside these.

import type {
  DirectoryEntry,
  DirectoryErrorKind,
  RootUnavailableReason,
  RootsErrorKind,
} from '@kpv/schema';

import type { RequestProblem } from '../connection/describe.ts';
import type {
  RootsAction,
  RootsOutcome,
  UnconfirmedReason,
} from './controller.ts';

/** What an entry is, in words, and whether the picker can open it. */
export interface EntryFacts {
  /** The entry's kind as text, never only an icon or a colour. */
  kind: string;
  /** Whether opening it lists it. */
  openable: boolean;
  /** Why it cannot be opened, when it cannot; read out with the entry. */
  reason: string | null;
}

const linkWords = {
  directory: 'symlink to a directory',
  file: 'symlink to a file',
  other: 'symlink to a pipe, socket or device',
  broken: 'broken symlink',
  outside: 'symlink leading outside the browsable area',
  unknown: 'symlink whose target could not be checked',
} as const;

const linkReasons = {
  directory: null,
  file: 'it leads to a file',
  other: 'it leads to a pipe, socket or device',
  broken: 'its target does not exist',
  outside: 'it leads outside the browsable area',
  unknown: 'its target could not be checked',
} as const;

export function entryFacts(entry: DirectoryEntry): EntryFacts {
  let kind: string;
  let reason: string | null;
  switch (entry.kind) {
    case 'directory':
      kind = 'directory';
      reason = null;
      break;
    case 'file':
      kind = 'file';
      reason = 'it is a file';
      break;
    case 'symlink': {
      // The agent always sends linkTarget on a symlink; without one there is
      // nothing to say it leads to a directory, so it is not opened.
      const target = entry.linkTarget ?? 'unknown';
      kind = linkWords[target];
      reason = linkReasons[target];
      break;
    }
    case 'other':
      kind = 'other: a pipe, socket or device';
      reason = 'it is not a file or directory';
      break;
    case 'unknown':
      kind = 'unknown: the agent could not describe it';
      reason = 'the agent could not describe it';
      break;
  }
  if (entry.unreadable) {
    reason = 'it is unreadable';
  }
  return { kind, openable: reason === null, reason };
}

/** A heading and a sentence for a failure, shown with the agent's message. */
export interface ErrorWords {
  title: string;
  detail: string;
}

export const directoryErrorWords: Record<DirectoryErrorKind, ErrorWords> = {
  notFound: {
    title: 'Not there',
    detail:
      'This directory does not exist. It may have been deleted or moved after it was listed.',
  },
  forbidden: {
    title: 'Outside what can be browsed',
    detail:
      'Only the home directory and the authorized project directories can be browsed.',
  },
  unreadable: {
    title: 'Unreadable',
    detail: 'The agent is not permitted to read this directory.',
  },
  notDirectory: {
    title: 'Not a directory',
    detail: 'That path is not a directory, so it cannot be listed.',
  },
  invalid: {
    title: 'Not a path the agent accepts',
    detail: 'Type an absolute path, one that starts with /.',
  },
  io: {
    title: 'Could not be listed',
    detail: 'The agent failed to read this directory.',
  },
};

export const rootsErrorWords: Record<RootsErrorKind, ErrorWords> = {
  tooBroad: {
    title: 'Too broad to authorize',
    detail:
      'The home directory, the directories above it and / cannot be project directories. Open a directory inside your home directory and authorize that.',
  },
  overlaps: {
    title: 'Overlaps an authorized directory',
    detail:
      'This directory is inside, or contains, a directory that is already authorized. Open that one, or revoke it and try again.',
  },
  forbidden: {
    title: 'Outside the home directory',
    detail: 'Only a directory inside your home directory can be authorized.',
  },
  notFound: {
    title: 'Not there',
    detail: 'There is no such directory, or no such authorized directory.',
  },
  notDirectory: {
    title: 'Not a directory',
    detail: 'Only a directory can be authorized.',
  },
  unreadable: {
    title: 'Unreadable',
    detail: 'The agent is not permitted to read this directory.',
  },
  invalid: {
    title: 'Not a request the agent accepts',
    detail: 'The agent refused the request as malformed.',
  },
  io: {
    title: 'Not saved',
    detail: 'The agent could not change the authorized directories.',
  },
};

/** Why a stored root is not available, in words. */
export const unavailableWords: Record<RootUnavailableReason, string> = {
  notFound: 'unavailable: it no longer exists',
  notDirectory: 'unavailable: it is no longer a directory',
  unreadable: 'unavailable: the agent may no longer read it',
  forbidden:
    'unavailable: it is no longer inside the home directory, or it now holds the agent’s own store',
  tooBroad: 'unavailable: it is now the home directory or above it',
  changed:
    'unavailable: it now leads somewhere else than when it was authorized',
  overlaps: 'unavailable: it overlaps a directory authorized before it',
  io: 'unavailable: it could not be checked',
};

/** What revoking does, said where the control is. */
export const revokeNote =
  'Revoking only takes the directory off this list. It deletes nothing on disk.';

/** What a truncated page is, so it is never taken for the whole directory. */
export function truncationNote(shown: number): string {
  return (
    `This directory has more entries than the ${shown} shown. ` +
    'They are a sample in the order the filesystem returned them, sorted by name, ' +
    'not the alphabetical start of the directory. To reach one that is not shown, type its path.'
  );
}

/**
 * Why what is shown is only the last known state. While the channel is down
 * it is the connection; while it is up, it is only the picker's own last
 * request, which the status bar does not follow (#66).
 */
export function staleNote(online: boolean, problem: RequestProblem | null) {
  if (!online || problem === null) {
    return (
      'Last known, not current: the agent is not connected, so what is shown ' +
      'here may be out of date. Authorizing and revoking are off until it is ' +
      'back, and both lists are read again then.'
    );
  }
  let cause: string;
  switch (problem.kind) {
    case 'rejected':
      cause = 'the agent rejected the picker’s last request';
      break;
    case 'unreachable':
      cause = 'the agent did not answer the picker’s last request';
      break;
    case 'failed':
      cause = 'the agent’s last answer to the picker could not be read';
      break;
  }
  return (
    `Last known, not current: ${cause}, so what is shown here may be out of ` +
    'date. Authorizing and revoking are off until a request from the picker ' +
    'succeeds again.'
  );
}

/** Says that a request problem is the picker's, not the connection's. */
export const pickerProblemScope =
  'This is about the directory picker’s last request only, not the whole ' +
  'connection: the status bar shows the connection to the agent, which this ' +
  'does not change.';

/** Why Authorize is off for a listed directory that could not be listed again. */
export function listingGoneNote(path: string, kind: DirectoryErrorKind) {
  return (
    `${path} could not be listed just now: ${directoryErrorWords[kind].title}. ` +
    'Open a directory that can be listed to authorize it.'
  );
}

/** What "Still showing" says when the listed directory's own browse failed. */
export function staleListingNote(path: string) {
  return (
    `Still showing ${path} as it was listed before this failure: it could ` +
    'not be listed again, so the entries below may no longer be there.'
  );
}

/**
 * The sentence shown, and announced, for an authorize or revoke that was
 * rejected, not answered readably, or settled afterwards by the roots list.
 * The other outcomes have their own markup.
 */
export function rootsOutcomeSentence(outcome: RootsOutcome): string {
  switch (outcome.kind) {
    case 'rejected':
      return outcome.action === 'authorize'
        ? `Not authorized: the agent rejected the request, so ${outcome.path} was not authorized. Nothing was changed on the agent.`
        : `Not revoked: the agent rejected the request, so ${outcome.path} was not revoked. Nothing was changed on the agent.`;
    case 'unconfirmed':
      return unconfirmedSentence(outcome.action, outcome.path, outcome.reason);
    case 'settled':
      if (outcome.action === 'authorize') {
        return outcome.listed
          ? `${outcome.path} is authorized, as confirmed by the list of authorized directories the agent returned.`
          : `Not authorized by that path: ${outcome.path} is not in the list of authorized directories the agent returned.`;
      }
      return outcome.listed
        ? `${outcome.path} is still authorized: the list of authorized directories the agent returned still has it.`
        : `${outcome.path} is revoked: the list of authorized directories the agent returned no longer has it. Nothing on disk was deleted.`;
    case 'authorized':
      return `Authorized ${outcome.path}.`;
    case 'revoked':
      return `Revoked ${outcome.path}. Nothing on disk was deleted.`;
    case 'refused':
      return `${outcome.action === 'authorize' ? 'Not authorized' : 'Not revoked'}. ${rootsErrorWords[outcome.error.error].title}. ${outcome.error.message}`;
  }
}

function unconfirmedSentence(
  action: RootsAction,
  path: string,
  reason: UnconfirmedReason,
): string {
  const done = action === 'authorize' ? 'authorized' : 'revoked';
  const settles =
    'The list of authorized directories settles it once it is read again: Try again reads it.';
  switch (reason.kind) {
    case 'dropped':
      return `Not confirmed: the connection went before the agent answered, so ${path} is not known to be ${done}. The list is read again from the agent once it is back.`;
    case 'unreachable':
      return `Not confirmed: the agent did not answer, so it is not known whether ${path} was ${done}. ${settles}`;
    case 'failed':
      return `Not confirmed: the agent answered with HTTP ${reason.status}, but the answer could not be read, so it is not known whether ${path} was ${done}. ${settles}`;
  }
}
