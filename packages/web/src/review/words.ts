// What the review says (#43): a sentence for every kind the agent answers,
// every state a staged file's project copy can be in, and every result.

import type {
  WorkcopyErrorKind,
  WorkcopyResult,
  WorkcopyState,
} from '@kpv/schema';

import { sourceRefusalWords } from '../blocks/source-buffer.ts';

/**
 * What a staged file's project copy is now, when it is not what the change
 * was staged from. The kinds #87 has a sentence for use that sentence.
 */
export const stateWords: Record<Exclude<WorkcopyState, 'ready'>, string> = {
  changed:
    'The file was changed in the project since this was staged, so apply would write nothing.',
  created:
    'A file was created at this path in the project since this was staged, so apply would write nothing.',
  deleted:
    'The file was deleted in the project since this was staged, so apply would write nothing.',
  link: sourceRefusalWords.link,
  notRegularFile: sourceRefusalWords.notRegularFile,
  tooLarge: sourceRefusalWords.tooLarge,
  notText: sourceRefusalWords.notText,
  unreadable: sourceRefusalWords.unreadable,
};

/** What a refused working-copy request says. */
export const reviewErrorWords: Record<WorkcopyErrorKind, string> = {
  invalid: 'The agent did not understand the request.',
  unavailable:
    'The agent has no config directory, so it keeps no staged changes and applies none.',
  notAuthorized:
    'This pipeline is not in an authorized project root any more, so nothing was written.',
  rootUnavailable:
    "This pipeline's project root is unavailable, so nothing was written.",
  notFound: 'The pipeline or the snapshot is not there any more.',
  link: 'A directory on the way to a staged file, or the file itself, is a symbolic link, which the agent never follows. Nothing was written.',
  changed:
    'What is staged, or a directory on the way, changed since this review was drawn, so nothing was written. The review is drawn again.',
  unreadable: sourceRefusalWords.unreadable,
  generated: sourceRefusalWords.generated,
  reserved: 'pipeline.yaml is the pipeline itself and is never deleted.',
  notRegularFile: sourceRefusalWords.notRegularFile,
  tooLarge: sourceRefusalWords.tooLarge,
  notText: sourceRefusalWords.notText,
  tooMany: 'More than 32 files would be staged.',
  drift:
    'Some project files are not what was staged from, so nothing was written. Nothing here overwrites them.',
  snapshot:
    'The snapshot could not be written, so nothing in the project was written.',
  io: 'The agent could not read or write the staged changes.',
};

/** What a report says of one path. */
export const resultWords: Record<WorkcopyResult, string> = {
  written: 'written',
  notWritten: 'not written, still staged',
  failed: 'failed, still staged',
  unchanged: 'already as the snapshot holds it',
};

export const reviewWords = {
  title: 'Staged changes',
  staged: (count: number) => `${count} file${count === 1 ? '' : 's'} staged`,
  nothingStaged: 'Nothing is staged for this pipeline.',
  unreadable:
    'The staged changes for this pipeline could not be read, so they were set aside and nothing is staged.',
  noDiff: 'No diff is shown for this file.',
  applied: 'Applied. Every staged file was written.',
  appliedPartly:
    'The apply stopped partway. The files not written stay staged; restore the snapshot to undo what was written.',
  restored: 'Restored. Every file the snapshot covers holds its bytes again.',
  restoredPartly:
    'The restore stopped partway. Restore again once the reason is fixed.',
  driftTitle: 'Nothing was written. These files differ:',
  snapshots: 'Snapshots',
  noSnapshots: 'No snapshots yet; each apply takes one before it writes.',
  emptyDirectories:
    'A directory left empty by a deletion, or made by an apply and emptied by a restore, is kept.',
} as const;

/** The question discard asks first, naming how many files are staged. */
export const discardStagedQuestion = (count: number): string =>
  `Discard the ${count} staged file${count === 1 ? '' : 's'}? The project stays as it is.`;

/** The question removing a snapshot asks first. */
export const removeSnapshotQuestion = (taken: string): string =>
  `Remove the snapshot taken ${taken}? It can then no longer be restored.`;
