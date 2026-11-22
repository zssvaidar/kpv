// The code a custom-code block shows (#14), apart from the editor itself and
// from React, so all of it is tested without a DOM, as the panel forms'
// models are (panel/*-form.ts):
//
//   - which file a step shows: only a customCode step's `customCode.path`;
//     a filter, transform or validate step's reference gets no editor
//   - one buffer per path, shared by every step naming it, holding what the
//     read answered and what has been typed since
//   - whether a buffer is unsaved, judged by what a save would write
//   - the bytes a save writes: the editor's lines with each unchanged line's
//     own line ending, so a CRLF file stays CRLF and a missing final newline
//     stays missing
//   - what the node says for each state, and for each refusal kind #87 has
//
// The editor (CodeMirror) splits a document on \r\n, \r and \n and gives its
// text back joined by \n, so the buffer holds that normalized text and the
// read holds the file's exact text. A lone UTF-16 surrogate in the buffer is
// written by the agent's JSON decoder as U+FFFD, so the buffer is compared,
// and sent, as that: what would be written, never what merely differs in
// memory.

import type {
  Language,
  Pipeline,
  ProcessingStep,
  SourceError,
  SourceErrorKind,
} from '@kpv/schema';

import type { RequestProblem } from '../connection/describe.ts';
import { describeRequestProblem } from '../connection/describe.ts';

/** Which file a processing step shows, when it is one that shows a file. */
export type StepSource =
  /** A customCode step with no `customCode` yet (#88 sets one). */
  { kind: 'none' } | { kind: 'file'; path: string; symbol?: string };

/**
 * The file a step's editor shows, or `null` when the step gets no editor:
 * only a customCode step does, whatever reference another type carries.
 */
export function stepSource(step: ProcessingStep): StepSource | null {
  if (step.type !== 'customCode') return null;
  const reference = step.customCode;
  if (reference === undefined) return { kind: 'none' };
  return reference.symbol === undefined
    ? { kind: 'file', path: reference.path }
    : { kind: 'file', path: reference.path, symbol: reference.symbol };
}

/** Every distinct path the pipeline's customCode steps name, in order. */
export function sourcePaths(pipeline: Pipeline): string[] {
  const paths: string[] = [];
  for (const step of pipeline.processingSteps ?? []) {
    const source = stepSource(step);
    if (source?.kind === 'file' && !paths.includes(source.path)) {
      paths.push(source.path);
    }
  }
  return paths;
}

/** The node ids (layout.ts) of the customCode steps naming any of `paths`. */
export function stepsNaming(
  pipeline: Pipeline,
  paths: readonly string[],
): string[] {
  return (pipeline.processingSteps ?? [])
    .filter((step) => {
      const source = stepSource(step);
      return source?.kind === 'file' && paths.includes(source.path);
    })
    .map((step) => `step:${step.id}`);
}

/** The paths `before` names that `after` no longer does. */
export function droppedPaths(before: Pipeline, after: Pipeline): string[] {
  const kept = sourcePaths(after);
  return sourcePaths(before).filter((path) => !kept.includes(path));
}

/**
 * The paths with unsaved code an edit from `before` to `after` would drop:
 * the last step naming each is gone. A path another step still names keeps
 * its buffer, so nothing is lost and nothing needs asking.
 */
export function unsavedDroppedBy(
  before: Pipeline,
  after: Pipeline,
  unsaved: readonly string[],
): string[] {
  return droppedPaths(before, after).filter((path) => unsaved.includes(path));
}

// Line endings and what is written.

const lineBreak = /\r\n?|\n/g;

/** The text as the editor holds it: every line ending a \n. */
export const editorText = (text: string): string =>
  text.replace(lineBreak, '\n');

/**
 * The text as the agent would write it: a lone UTF-16 surrogate becomes
 * U+FFFD, which is what its JSON decoder makes of one.
 */
export function wellFormed(text: string): string {
  return text.replace(
    /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/g,
    '�',
  );
}

function lineParts(text: string): { lines: string[]; ends: string[] } {
  const lines: string[] = [];
  const ends: string[] = [];
  let start = 0;
  for (const match of text.matchAll(lineBreak)) {
    lines.push(text.slice(start, match.index));
    ends.push(match[0]);
    start = match.index + match[0].length;
  }
  lines.push(text.slice(start));
  return { lines, ends };
}

/** The line ending most of the file's lines have; \n for a file with none. */
export function usualLineEnding(text: string): string {
  const counts = new Map<string, number>();
  for (const end of lineParts(text).ends) {
    counts.set(end, (counts.get(end) ?? 0) + 1);
  }
  let best = '\n';
  let most = counts.get('\n') ?? 0;
  for (const [end, count] of counts) {
    if (count > most) {
      best = end;
      most = count;
    }
  }
  return best;
}

/**
 * The longest run of cells a line match is computed over. Past it, the lines
 * between the unchanged first and last ones are matched by nothing, and each
 * of them ends the way most of the file's lines do.
 */
export const matchLimit = 1_000_000;

/**
 * For each line of `next`, the index of the line of `read` it is unchanged
 * from, or -1: the common first and last lines, and a longest common
 * subsequence of the lines between them.
 */
function matchLines(read: string[], next: string[]): number[] {
  const match = new Array<number>(next.length).fill(-1);
  let head = 0;
  while (
    head < read.length &&
    head < next.length &&
    read[head] === next[head]
  ) {
    match[head] = head;
    head++;
  }
  let tail = 0;
  while (
    tail < read.length - head &&
    tail < next.length - head &&
    read[read.length - 1 - tail] === next[next.length - 1 - tail]
  ) {
    match[next.length - 1 - tail] = read.length - 1 - tail;
    tail++;
  }
  const a = read.slice(head, read.length - tail);
  const b = next.slice(head, next.length - tail);
  const width = b.length + 1;
  if (a.length === 0 || b.length === 0) return match;
  if ((a.length + 1) * width > matchLimit) return match;
  // lengths[i * width + j]: the longest common subsequence of a[i..], b[j..].
  const lengths = new Uint32Array((a.length + 1) * width);
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lengths[i * width + j] =
        a[i] === b[j]
          ? (lengths[(i + 1) * width + j + 1] ?? 0) + 1
          : Math.max(
              lengths[(i + 1) * width + j] ?? 0,
              lengths[i * width + j + 1] ?? 0,
            );
    }
  }
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      match[head + j] = head + i;
      i++;
      j++;
    } else if (
      (lengths[(i + 1) * width + j] ?? 0) >= (lengths[i * width + j + 1] ?? 0)
    ) {
      i++;
    } else {
      j++;
    }
  }
  return match;
}

/**
 * The text a save writes for the editor's `buffer` of a file that read as
 * `read`: the buffer as the agent would write it, each line ending as that
 * line did when it was read, and a new line, or one that was the last and
 * has a line after it now, ending the way most of the file's lines do. A
 * buffer the editor did not change gives back exactly `read`.
 */
export function textToWrite(read: string, buffer: string): string {
  const before = lineParts(read);
  const after = lineParts(wellFormed(buffer)).lines;
  const usual = usualLineEnding(read);
  const match = matchLines(before.lines, after);
  let text = '';
  after.forEach((line, j) => {
    text += line;
    if (j === after.length - 1) return;
    const from = match[j] ?? -1;
    text += from >= 0 && from < before.ends.length ? before.ends[from] : usual;
  });
  return text;
}

// The buffers.

/** What the last save of a buffer was answered, when it did not succeed. */
export type SaveFailure =
  /** The file is not as it was read: `changed`, or `exists` for a new one. */
  | { kind: 'changed' }
  | { kind: 'refused'; error: SourceError }
  | { kind: 'problem'; problem: RequestProblem };

/** One file's buffer, shared by every step naming its path. */
export type SourceBuffer =
  | { status: 'loading' }
  /** The read was refused: no editor text, the kind's sentence instead. */
  | { status: 'refused'; error: SourceError }
  /** The read got no answer the page could use. */
  | { status: 'problem'; problem: RequestProblem }
  | LoadedSource;

export interface LoadedSource {
  status: 'loaded';
  /**
   * The file as last read or written, exactly, with the hash a save sends;
   * `null` for a file that does not exist yet, which a save creates.
   */
  disk: { text: string; sha256: string } | null;
  /** `disk.text` as the editor holds it, or '' for no file. */
  read: string;
  /** Under generated/: read-only, regeneration owns it. */
  generated: boolean;
  /** What the editor holds now. */
  text: string;
  saving: boolean;
  failure: SaveFailure | null;
}

/** A buffer for what a read answered. */
export function loadedSource(
  file: { text: string; sha256: string; generated: boolean } | null,
): LoadedSource {
  if (file === null) {
    return {
      status: 'loaded',
      disk: null,
      read: '',
      generated: false,
      text: '',
      saving: false,
      failure: null,
    };
  }
  const read = editorText(file.text);
  return {
    status: 'loaded',
    disk: { text: file.text, sha256: file.sha256 },
    read,
    generated: file.generated,
    text: read,
    saving: false,
    failure: null,
  };
}

/**
 * Whether a buffer holds code a save would change: what it would write is
 * not what was read. A buffer typed back to what was read is not unsaved.
 */
export function isUnsaved(buffer: SourceBuffer | undefined): boolean {
  if (buffer?.status !== 'loaded' || buffer.generated) return false;
  return wellFormed(buffer.text) !== buffer.read;
}

/** A buffer after the editor reported new text. */
export function edited(buffer: LoadedSource, text: string): LoadedSource {
  return buffer.generated ? buffer : { ...buffer, text };
}

// What the node says.

/** The sentence for each kind the agent refuses a read or a save with. */
export const sourceRefusalWords: Record<SourceErrorKind, string> = {
  invalid:
    'This path is not a file inside the pipeline directory the agent will read: it has an empty, ".", ".." or dot-led segment, a backslash or a trailing "/".',
  notAuthorized:
    'This pipeline is not in an authorized project root any more, so the agent will not read its files.',
  rootUnavailable:
    "This pipeline's project root is unavailable, so its files cannot be read.",
  notFound: 'The file is not there.',
  reserved:
    "This path is the pipeline's own pipeline.yaml, which is not shown as code.",
  generated:
    'This file is under generated/, which regeneration owns, so it is not written here.',
  link: 'The file, or a directory on its path, is a symbolic link, which the agent never follows.',
  changed:
    'A directory on the path changed while the agent was opening it. Reload to try again.',
  exists: 'A file is already there.',
  notRegularFile:
    'This path names a directory or a special file, not a regular file.',
  tooLarge:
    'The file is over 1 MiB, which is more than the agent reads or writes through the page.',
  notText:
    'The file is not UTF-8 text, or holds a NUL byte, so it is not shown and cannot be overwritten here.',
  unreadable: 'The agent may not read this file or a directory on its path.',
  io: 'The agent could not read or write the file.',
};

export const codeWords = {
  noReference:
    'This block names no source file yet, so there is no code to show.',
  noFile:
    'This pipeline was not opened from disk, so there is no file to read.',
  loading: 'Reading the file.',
  missing: 'This file does not exist yet; saving creates it.',
  generated:
    'This file is generated: regeneration owns it and rewrites it, so it is read-only here.',
  unsaved: 'Unsaved',
  changed:
    'The file changed on disk since it was read. What you typed is kept; reload to see the file as it is now.',
  saving: 'Saving',
  nothingToSave: 'Nothing to save',
  /** A file the tool has staged a change to (#43); the editor shows disk. */
  staged: 'This file has staged changes waiting for review',
} as const;

/** What a request that got no usable answer says, in #8's words. */
export const problemWords = (problem: RequestProblem): string => {
  const description = describeRequestProblem(problem);
  return `${description.label}. ${description.detail}`;
};

/** Everything a custom-code node draws below its card. */
export type CodeView =
  | { kind: 'noReference' }
  | { kind: 'noFile' }
  | { kind: 'loading'; path: string; symbol?: string }
  | { kind: 'refused'; path: string; symbol?: string; sentence: string }
  | CodeEditorView;

export interface CodeEditorView {
  kind: 'editor';
  path: string;
  symbol?: string;
  text: string;
  language: Language;
  /** A generated file: read-only, with no save control. */
  generated: boolean;
  /** A file that does not exist yet. */
  missing: boolean;
  unsaved: boolean;
  /** Why Save is disabled, or `null` when it is enabled. */
  saveBlocked: string | null;
  saving: boolean;
  /** The last save's failure, said. */
  notice: string | null;
  /** The last save found the file changed on disk. */
  changedOnDisk: boolean;
}

/**
 * What a step's node shows, from the step, its buffer and whether a save can
 * be sent now. `offline` is the connection state's words while there is no
 * working connection to the agent, `null` while there is; `buffers` is
 * `null` for a pipeline not opened from disk.
 */
export function codeView(
  source: StepSource,
  buffer: SourceBuffer | undefined | null,
  language: Language,
  offline: string | null,
): CodeView {
  if (source.kind === 'none') return { kind: 'noReference' };
  if (buffer === null) return { kind: 'noFile' };
  const where =
    source.symbol === undefined
      ? { path: source.path }
      : { path: source.path, symbol: source.symbol };
  if (buffer === undefined || buffer.status === 'loading') {
    return { kind: 'loading', ...where };
  }
  if (buffer.status === 'refused') {
    return {
      kind: 'refused',
      ...where,
      sentence: sourceRefusalWords[buffer.error.error],
    };
  }
  if (buffer.status === 'problem') {
    return {
      kind: 'refused',
      ...where,
      sentence: problemWords(buffer.problem),
    };
  }
  const unsaved = isUnsaved(buffer);
  const failure = buffer.failure;
  return {
    kind: 'editor',
    ...where,
    text: buffer.text,
    language,
    generated: buffer.generated,
    missing: buffer.disk === null,
    unsaved,
    saveBlocked:
      offline !== null
        ? `Save unavailable: ${offline}`
        : buffer.saving
          ? codeWords.saving
          : !unsaved
            ? codeWords.nothingToSave
            : null,
    saving: buffer.saving,
    notice:
      failure === null
        ? null
        : failure.kind === 'changed'
          ? codeWords.changed
          : failure.kind === 'refused'
            ? sourceRefusalWords[failure.error.error]
            : problemWords(failure.problem),
    changedOnDisk: failure?.kind === 'changed',
  };
}
