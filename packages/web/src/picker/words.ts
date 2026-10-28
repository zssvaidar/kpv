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
