// What the pipeline list says about each thing the agent can answer (#48).
// One module, so every kind has words and a test can hold them apart; the
// list branches on the kind the agent sent, never on its message.

import type {
  PipelineLocation,
  PipelineRootReason,
  PipelinesErrorKind,
  UnreadableKind,
} from '@kpv/schema';

import { unavailableWords } from '../picker/words.ts';

/** Why a pipeline.yaml did not load, in words. */
export const unreadableWords: Record<UnreadableKind, string> = {
  parse:
    'pipeline.yaml is not one well-formed YAML document a pipeline can be read from',
  schema: 'pipeline.yaml is not a valid pipeline',
  tooLarge: 'pipeline.yaml is larger than 1 MiB, so it was not read',
  link: 'pipeline.yaml is a symbolic link, which is never read',
  notRegularFile:
    'pipeline.yaml is not a regular file: it is a directory, pipe or device',
  unreadable: 'the agent may not read this directory or its pipeline.yaml',
};

/**
 * Why a root is unavailable: the directory picker's words, so a root reads
 * the same in both places.
 */
export const rootReasonWords: Record<PipelineRootReason, string> =
  unavailableWords;

/** A heading for each error a pipeline request can answer. */
export const pipelinesErrorTitles: Record<PipelinesErrorKind, string> = {
  invalid: 'Not a valid request',
  notAuthorized: 'Not an authorized project directory',
  rootUnavailable: 'Project directory unavailable',
  notFound: 'No pipeline here',
  unreadable: 'Pipeline did not load',
  rootIsPipeline: 'This project directory is a pipeline',
  changed: 'Directory changed',
  noFreeId: 'No free id',
  io: 'Not read or written',
};

/** What a truncated root is, so its list is never taken for all of it. */
export const truncatedNote =
  'Not every directory here was looked at: this project directory holds more than 1000 entries, so a pipeline may be missing from this list.';

export function skippedLinksNote(count: number): string {
  return count === 1
    ? '1 symbolic link here was not followed.'
    : `${count} symbolic links here were not followed.`;
}

/** Where a pipeline is, as one line: its root and directory. */
export function locationWords(location: PipelineLocation): string {
  return location.directory === undefined
    ? location.root
    : `${location.root}/${location.directory}`;
}

/** What a duplicate id is, naming the other locations. */
export function duplicateWords(others: PipelineLocation[]): string {
  return `Another pipeline has this id: ${others.map(locationWords).join(', ')}.`;
}

/** Whether a failed create left its directory behind, when it says. */
export function leftBehindWords(left: boolean | undefined): string | null {
  if (left === undefined) return null;
  return left
    ? 'The directory it made is still there; nothing in it is a pipeline.'
    : 'The directory it made was removed again.';
}

export const noRootsWords =
  'No project directory is authorized yet, so there are no pipelines to list. Choose one with the directory picker.';

export const emptyRootWords = 'No pipelines here yet.';
