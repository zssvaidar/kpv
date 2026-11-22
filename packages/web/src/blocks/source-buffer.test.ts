// The custom-code editor's model (#14) without a DOM: which file a step
// shows, the buffer shared per path, unsaved state, line endings, and what
// each state and refusal shows.

import { describe, expect, it } from 'vitest';

import type { Pipeline, ProcessingStep, SourceError } from '@kpv/schema';
import { sourceErrorKindSchema } from '@kpv/schema';

import { graphFixtures } from '../graph/fixtures.ts';
import { insertStep, newStep } from '../graph/edit.ts';
import {
  codeView,
  codeWords,
  droppedPaths,
  edited,
  editorText,
  isUnsaved,
  loadedSource,
  matchLimit,
  sourcePaths,
  sourceRefusalWords,
  stepSource,
  stepsNaming,
  textToWrite,
  unsavedDroppedBy,
  usualLineEnding,
  wellFormed,
} from './source-buffer.ts';

const base = graphFixtures['every-block-type'];

const customCode = (
  id: string,
  path?: string,
  symbol?: string,
): ProcessingStep => ({
  id,
  type: 'customCode',
  configuration: { entrypoint: 'process' },
  ...(path === undefined
    ? {}
    : { customCode: symbol === undefined ? { path } : { path, symbol } }),
});

const withSteps = (steps: ProcessingStep[]): Pipeline => ({
  ...base,
  processingSteps: steps,
});

const hash = 'a'.repeat(64);
const loaded = (text: string, generated = false) =>
  loadedSource({ text, sha256: hash, generated });

describe('which file a step shows', () => {
  it("is a customCode step's customCode.path, with its symbol", () => {
    expect(stepSource(customCode('c', 'src/a.py', 'Run'))).toEqual({
      kind: 'file',
      path: 'src/a.py',
      symbol: 'Run',
    });
    expect(stepSource(customCode('c', 'src/a.py'))).toEqual({
      kind: 'file',
      path: 'src/a.py',
    });
  });

  it('is no file for a customCode step without a reference, as a new one is', () => {
    const inserted = newStep('customCode', 'customCode-1');
    expect(stepSource(inserted)).toEqual({ kind: 'none' });
    expect(codeView({ kind: 'none' }, undefined, 'go', null)).toEqual({
      kind: 'noReference',
    });
  });

  it('is nothing for a filter, transform or validate step, whatever it references', () => {
    for (const step of base.processingSteps ?? []) {
      if (step.type === 'customCode') continue;
      expect(
        stepSource({
          ...step,
          customCode: { path: 'src/x.py' },
        }),
      ).toBeNull();
    }
  });

  it('lists each path once, in pipeline order, so two steps share a buffer', () => {
    const pipeline = withSteps([
      customCode('one', 'src/b.py'),
      customCode('two', 'src/a.py'),
      customCode('three', 'src/b.py'),
      customCode('four'),
    ]);
    expect(sourcePaths(pipeline)).toEqual(['src/b.py', 'src/a.py']);
    expect(stepsNaming(pipeline, ['src/b.py'])).toEqual([
      'step:one',
      'step:three',
    ]);
  });

  it('drops a path only when no step names it any more, and asks only for unsaved code', () => {
    const before = withSteps([
      customCode('one', 'src/a.py'),
      customCode('two', 'src/a.py'),
      customCode('three', 'src/b.py'),
    ]);
    const oneGone = withSteps([
      customCode('two', 'src/a.py'),
      customCode('three', 'src/b.py'),
    ]);
    expect(droppedPaths(before, oneGone)).toEqual([]);
    const bothGone = withSteps([customCode('three', 'src/b.py')]);
    expect(droppedPaths(before, bothGone)).toEqual(['src/a.py']);
    expect(unsavedDroppedBy(before, bothGone, ['src/a.py'])).toEqual([
      'src/a.py',
    ]);
    expect(unsavedDroppedBy(before, bothGone, ['src/b.py'])).toEqual([]);
    // An inserted step drops nothing.
    const inserted = insertStep(
      before,
      { before: { role: 'source' }, after: { role: 'step', id: 'one' } },
      'filter',
    );
    if (!inserted.ok) throw new Error(inserted.reason);
    expect(droppedPaths(before, inserted.pipeline)).toEqual([]);
  });
});

describe('the bytes a save writes', () => {
  it('gives back exactly what was read for a buffer the editor did not change', () => {
    for (const text of [
      '',
      'a',
      'a\n',
      'a\r\nb\r\n',
      'a\r\nb',
      'a\nb\r\nc\rd',
      '\r\n\r\n',
      '﻿bom\r\n',
    ]) {
      expect(textToWrite(text, editorText(text))).toBe(text);
    }
  });

  it('keeps CRLF on a CRLF file, for changed and new lines too', () => {
    const read = 'def f():\r\n    return 1\r\n';
    const buffer = editorText(read).replace('return 1', 'return 2\n    # two');
    expect(textToWrite(read, buffer)).toBe(
      'def f():\r\n    return 2\r\n    # two\r\n',
    );
  });

  it('keeps a missing final newline missing, and adds none', () => {
    const read = 'a\r\nb';
    expect(textToWrite(read, 'a\nB')).toBe('a\r\nB');
    expect(textToWrite(read, 'a\nb\nc')).toBe('a\r\nb\r\nc');
  });

  it("keeps each unchanged line's own ending in a file of mixed endings", () => {
    const read = 'one\ntwo\r\nthree\nfour\r\nfive';
    const buffer = 'one\ntwo\nTHREE\nfour\nfive\nsix';
    // three changed, so it ends as most lines do; the rest as they did.
    expect(textToWrite(read, buffer)).toBe(
      'one\ntwo\r\nTHREE\nfour\r\nfive\nsix',
    );
    // An unchanged line between two changed ones keeps its own ending too.
    expect(textToWrite(read, 'ONE\ntwo\nthree\nfour\nFIVE')).toBe(
      'ONE\ntwo\r\nthree\nfour\r\nFIVE',
    );
  });

  it("ends a new file's lines with \\n, and a file with no line ending too", () => {
    expect(usualLineEnding('')).toBe('\n');
    expect(usualLineEnding('a\r\nb\r\nc\n')).toBe('\r\n');
    expect(textToWrite('', 'a\nb\n')).toBe('a\nb\n');
  });

  it('falls back to the usual ending, never a wrong line, past the match limit', () => {
    const lines = Math.ceil(Math.sqrt(matchLimit)) + 2;
    const read = Array.from({ length: lines }, (_, i) => `l${i}\r\n`).join('');
    const buffer = editorText(read)
      .split('\n')
      .map((line, i) => (i === 0 || i === lines - 1 ? `${line}!` : line))
      .join('\n');
    const written = textToWrite(read, buffer);
    expect(editorText(written)).toBe(buffer);
    expect(written.split('\r\n')).toHaveLength(lines + 1);
  });
});

describe('unsaved', () => {
  it('is a buffer that differs from what was read, and not one typed back to it', () => {
    const read = loaded('a\r\nb\r\n');
    expect(read.text).toBe('a\nb\n');
    expect(isUnsaved(read)).toBe(false);
    const typed = edited(read, 'a\nbc\n');
    expect(isUnsaved(typed)).toBe(true);
    expect(isUnsaved(edited(typed, 'a\nb\n'))).toBe(false);
  });

  it('judges a lone surrogate by what the agent would write: U+FFFD', () => {
    // A file holding U+FFFD, and a buffer holding a lone surrogate where it
    // is: a save would write the same bytes, so it is not unsaved.
    const read = loaded('x = "�"\n');
    const surrogate = edited(read, 'x = "\uD800"\n');
    expect(wellFormed('x = "\uD800"\n')).toBe('x = "�"\n');
    expect(wellFormed('😀')).toBe('😀');
    expect(wellFormed('\uDE00\uD83D')).toBe('��');
    expect(isUnsaved(surrogate)).toBe(false);
    // And what is sent is what would be written.
    const other = edited(loaded('x\n'), 'y\uDC00\n');
    expect(isUnsaved(other)).toBe(true);
    expect(textToWrite('x\n', other.text)).toBe('y�\n');
  });

  it('is never a generated file, which cannot be edited', () => {
    const generated = loaded('package gen\n', true);
    expect(edited(generated, 'changed')).toBe(generated);
    expect(isUnsaved({ ...generated, text: 'changed' })).toBe(false);
  });

  it('is an empty new file only once something is typed in it', () => {
    const missing = loadedSource(null);
    expect(missing.disk).toBeNull();
    expect(isUnsaved(missing)).toBe(false);
    expect(isUnsaved(edited(missing, 'def f(): pass\n'))).toBe(true);
  });
});

const file = {
  kind: 'file',
  path: 'src/discounts.py',
  symbol: 'apply',
} as const;
const error = (kind: SourceError['error']): SourceError => ({
  error: kind,
  message: 'the agent sentence',
});

describe('what the node shows', () => {
  it('says there is no file to read for a pipeline not opened from disk', () => {
    expect(codeView(file, null, 'python', null)).toEqual({ kind: 'noFile' });
  });

  it('says the file is being read until it is', () => {
    expect(codeView(file, undefined, 'python', null)).toMatchObject({
      kind: 'loading',
      symbol: 'apply',
    });
    expect(codeView(file, { status: 'loading' }, 'python', null).kind).toBe(
      'loading',
    );
  });

  it("shows each refusal kind's own sentence and no text", () => {
    const sentences = new Set<string>();
    for (const kind of sourceErrorKindSchema.options) {
      const view = codeView(
        file,
        { status: 'refused', error: error(kind) },
        'python',
        null,
      );
      expect(view).toEqual({
        kind: 'refused',
        path: file.path,
        symbol: 'apply',
        sentence: sourceRefusalWords[kind],
      });
      expect(view).not.toHaveProperty('text');
      sentences.add(sourceRefusalWords[kind]);
    }
    expect(sentences.size).toBe(sourceErrorKindSchema.options.length);
  });

  it('says why a read got no usable answer, in the connection words', () => {
    const view = codeView(
      file,
      { status: 'problem', problem: { kind: 'unreachable' } },
      'python',
      null,
    );
    expect(view).toMatchObject({ kind: 'refused' });
    expect(view.kind === 'refused' && view.sentence).toContain(
      'Agent not reachable',
    );
  });

  it('shows an empty file as an empty, editable editor', () => {
    const view = codeView(file, loaded(''), 'python', null);
    expect(view).toMatchObject({
      kind: 'editor',
      text: '',
      generated: false,
      missing: false,
      unsaved: false,
    });
  });

  it('shows a missing file as an empty editor marked as not existing yet', () => {
    const view = codeView(file, loadedSource(null), 'python', null);
    expect(view).toMatchObject({ kind: 'editor', text: '', missing: true });
  });

  it('shows a generated file read-only', () => {
    const view = codeView(file, loaded('x', true), 'go', null);
    expect(view).toMatchObject({ kind: 'editor', generated: true });
  });

  it('highlights by the language handed in, not the file name', () => {
    const view = codeView(file, loaded('x'), 'typescript', null);
    expect(view).toMatchObject({ language: 'typescript' });
  });

  it('enables Save only for an unsaved buffer while connected and not saving', () => {
    const clean = loaded('a\n');
    expect(codeView(file, clean, 'go', null)).toMatchObject({
      saveBlocked: codeWords.nothingToSave,
    });
    const dirty = edited(clean, 'b\n');
    expect(codeView(file, dirty, 'go', null)).toMatchObject({
      unsaved: true,
      saveBlocked: null,
    });
    expect(
      codeView(file, { ...dirty, saving: true }, 'go', null),
    ).toMatchObject({ saveBlocked: codeWords.saving });
    expect(codeView(file, dirty, 'go', 'Agent not reachable')).toMatchObject({
      unsaved: true,
      text: 'b\n',
      saveBlocked: 'Save unavailable: Agent not reachable',
    });
  });

  it('keeps what was typed after a failed save, and says why', () => {
    const dirty = edited(loaded('a\n'), 'b\n');
    const changed = codeView(
      file,
      { ...dirty, failure: { kind: 'changed' } },
      'go',
      null,
    );
    expect(changed).toMatchObject({
      text: 'b\n',
      unsaved: true,
      changedOnDisk: true,
      notice: codeWords.changed,
    });
    const refused = codeView(
      file,
      { ...dirty, failure: { kind: 'refused', error: error('tooLarge') } },
      'go',
      null,
    );
    expect(refused).toMatchObject({
      text: 'b\n',
      changedOnDisk: false,
      notice: sourceRefusalWords.tooLarge,
    });
  });
});
