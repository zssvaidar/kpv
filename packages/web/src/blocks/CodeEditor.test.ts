// The editor's own logic that needs no DOM: the smallest replacement a text
// handed in is applied as, and the line motion that stands in for
// CodeMirror's coordinate-based one (CodeEditor.tsx says why).

import { EditorSelection, EditorState } from '@codemirror/state';
import { describe, expect, it } from 'vitest';

import { lineMotion, pageLines, replacement } from './CodeEditor.tsx';

const apply = (from: string, to: string) => {
  const change = replacement(from, to);
  return from.slice(0, change.from) + change.insert + from.slice(change.to);
};

describe('replacement', () => {
  it('turns one text into the other, touching only what differs', () => {
    for (const [from, to] of [
      ['', ''],
      ['', 'abc'],
      ['abc', ''],
      ['abc', 'abXc'],
      ['aaaa', 'aa'],
      ['def f():\n    pass\n', 'def g():\n    pass\n'],
    ] as const) {
      expect(apply(from, to)).toBe(to);
    }
    expect(replacement('hello world', 'hello there world')).toEqual({
      from: 6,
      to: 6,
      insert: 'there ',
    });
  });
});

describe('line motion', () => {
  const state = (doc: string, anchor: number, head = anchor) =>
    EditorState.create({
      doc,
      selection: EditorSelection.single(anchor, head),
    });
  const doc = 'first line\nab\nthird line\n';

  it('moves by document line keeping the column, clamped to a shorter line', () => {
    const down = lineMotion(state(doc, 8), 1, false).main;
    // Column 8 on a two-character line ends at its end.
    expect(down.head).toBe(13);
    const further = lineMotion(
      state(doc, 13).update({ selection: EditorSelection.create([down]) })
        .state,
      1,
      false,
    ).main;
    // The column it was moved from is kept for the next line.
    expect(further.head).toBe(14 + 8);
    expect(lineMotion(state(doc, 22), -1, false).main.head).toBe(13);
  });

  it('goes to the start from the first line, and to the end from the last', () => {
    expect(lineMotion(state(doc, 4), -1, false).main.head).toBe(0);
    expect(lineMotion(state(doc, doc.length), 1, false).main.head).toBe(
      doc.length,
    );
  });

  it('extends the selection with shift, keeping the anchor', () => {
    const extended = lineMotion(state(doc, 2), 1, true).main;
    expect(extended.anchor).toBe(2);
    expect(extended.head).toBe(13);
  });

  it('moves a page of lines, clamped to the document', () => {
    const long = Array.from({ length: 50 }, (_, i) => `line ${i}`).join('\n');
    const at = lineMotion(state(long, 0), pageLines, false).main.head;
    expect(EditorState.create({ doc: long }).doc.lineAt(at).number).toBe(
      1 + pageLines,
    );
    const end = lineMotion(state(long, 0), 1000, false).main.head;
    expect(EditorState.create({ doc: long }).doc.lineAt(end).number).toBe(50);
  });
});
