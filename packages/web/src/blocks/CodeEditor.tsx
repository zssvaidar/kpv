// The CodeMirror 6 editor a custom-code node holds (#14). It is controlled:
// it shows the text it is handed and reports each change up, and whoever
// owns the text (the shell) hands the change back in. Text handed in that
// differs from what it shows (typed in another node showing the same file,
// or read again) is applied as the smallest replacement and kept out of
// this editor's undo history, so its own history, cursor and selection
// survive it.
//
// The editor is created in an effect, so rendering to a string (the tests,
// which have no DOM) gives only its empty host. The host carries React
// Flow's nodrag, nopan and nowheel classes and keeps its key presses to
// itself, so dragging in it selects text, the wheel scrolls it, and no key
// typed in it reaches the graph: no step is deleted, selected or unselected
// and no drag is cancelled. Tab is not bound, so it moves focus on.
//
// The arrow keys up and down and Page Up and Down move by document line
// (lineMotion), not by CodeMirror's own commands, which find the line above
// by screen coordinates: zoomed out on the graph a line is under 2 screen
// pixels tall, and CodeMirror 6.43's moveVertically then scans by
// `textHeight >> 1`, which is 0, and never returns, hanging the page. Lines
// are not wrapped, so a document line is a line on screen.

import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
import { go } from '@codemirror/lang-go';
import { javascript } from '@codemirror/lang-javascript';
import { python } from '@codemirror/lang-python';
import { syntaxHighlighting } from '@codemirror/language';
import {
  Annotation,
  Compartment,
  EditorSelection,
  EditorState,
  Transaction,
  type Extension,
} from '@codemirror/state';
import {
  drawSelection,
  EditorView,
  keymap,
  lineNumbers,
} from '@codemirror/view';
import { classHighlighter } from '@lezer/highlight';
import { useEffect, useRef } from 'react';

import type { Language } from '@kpv/schema';

/** The highlighting for the pipeline's language, never the file's name. */
export function languageSupport(language: Language): Extension {
  switch (language) {
    case 'python':
      return python();
    case 'typescript':
      return javascript({ typescript: true });
    case 'go':
      return go();
  }
}

/** How many lines Page Up and Page Down move. */
export const pageLines = 20;

/**
 * The selection after moving every range `by` document lines, keeping the
 * column it was moved from, or extending each range when `extend`. Moving
 * up from the first line goes to the start, down from the last to the end.
 */
export function lineMotion(
  state: EditorState,
  by: number,
  extend: boolean,
): EditorSelection {
  return EditorSelection.create(
    state.selection.ranges.map((range) => {
      const line = state.doc.lineAt(range.head);
      const goal = range.goalColumn ?? range.head - line.from;
      const number = Math.min(Math.max(line.number + by, 1), state.doc.lines);
      let head: number;
      if (number === line.number) {
        head = by < 0 ? 0 : state.doc.length;
      } else {
        const target = state.doc.line(number);
        head = target.from + Math.min(goal, target.length);
      }
      return extend
        ? EditorSelection.range(range.anchor, head, goal)
        : EditorSelection.cursor(head, undefined, undefined, goal);
    }),
    state.selection.mainIndex,
  );
}

const move =
  (by: number, extend: boolean) =>
  (view: EditorView): boolean => {
    view.dispatch({
      selection: lineMotion(view.state, by, extend),
      scrollIntoView: true,
      userEvent: 'select',
    });
    return true;
  };

const lineMotionKeymap = [
  { key: 'ArrowUp', run: move(-1, false), shift: move(-1, true) },
  { key: 'ArrowDown', run: move(1, false), shift: move(1, true) },
  {
    key: 'PageUp',
    run: move(-pageLines, false),
    shift: move(-pageLines, true),
  },
  {
    key: 'PageDown',
    run: move(pageLines, false),
    shift: move(pageLines, true),
  },
  { mac: 'Ctrl-p', run: move(-1, false), shift: move(-1, true) },
  { mac: 'Ctrl-n', run: move(1, false), shift: move(1, true) },
];

/** Marks a change that came in as a prop rather than being typed here. */
const fromOwner = Annotation.define<boolean>();

const readOnlyState = (readOnly: boolean): Extension => [
  EditorState.readOnly.of(readOnly),
  EditorView.editable.of(!readOnly),
];

/** The smallest replacement that turns `from` into `to`. */
export function replacement(
  from: string,
  to: string,
): { from: number; to: number; insert: string } {
  let start = 0;
  const shorter = Math.min(from.length, to.length);
  while (start < shorter && from[start] === to[start]) start++;
  let end = 0;
  while (
    end < shorter - start &&
    from[from.length - 1 - end] === to[to.length - 1 - end]
  ) {
    end++;
  }
  return {
    from: start,
    to: from.length - end,
    insert: to.slice(start, to.length - end),
  };
}

export function CodeEditor(props: {
  text: string;
  language: Language;
  readOnly: boolean;
  /** The editor's accessible name. */
  label: string;
  onChange: (text: string) => void;
}) {
  const { text, language, readOnly, label, onChange } = props;
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const shown = useRef(text);
  const report = useRef(onChange);
  const languageSlot = useRef(new Compartment());
  const readOnlySlot = useRef(new Compartment());
  useEffect(() => {
    report.current = onChange;
  }, [onChange]);

  // Created once, with what is handed in when it mounts; every later change
  // of a prop is applied to it below, never by making another.
  const initial = useRef({ text, language, readOnly, label });
  useEffect(() => {
    const parent = host.current;
    if (parent === null) return;
    const first = initial.current;
    const created = new EditorView({
      parent,
      state: EditorState.create({
        doc: first.text,
        extensions: [
          lineNumbers(),
          history(),
          drawSelection(),
          keymap.of([...lineMotionKeymap, ...defaultKeymap, ...historyKeymap]),
          syntaxHighlighting(classHighlighter),
          languageSlot.current.of(languageSupport(first.language)),
          readOnlySlot.current.of(readOnlyState(first.readOnly)),
          EditorView.contentAttributes.of({ 'aria-label': first.label }),
          EditorView.updateListener.of((update) => {
            if (!update.docChanged) return;
            const typed = update.transactions.some(
              (transaction) => transaction.annotation(fromOwner) !== true,
            );
            const now = update.state.doc.toString();
            shown.current = now;
            if (typed) report.current(now);
          }),
        ],
      }),
    });
    shown.current = first.text;
    view.current = created;
    // No key typed in the editor goes on to the graph or the page.
    const keep = (event: KeyboardEvent) => {
      event.stopPropagation();
    };
    parent.addEventListener('keydown', keep);
    return () => {
      parent.removeEventListener('keydown', keep);
      created.destroy();
      view.current = null;
    };
  }, []);

  useEffect(() => {
    const editor = view.current;
    if (editor === null || text === shown.current) return;
    editor.dispatch({
      changes: replacement(editor.state.doc.toString(), text),
      annotations: [fromOwner.of(true), Transaction.addToHistory.of(false)],
    });
  }, [text]);

  useEffect(() => {
    view.current?.dispatch({
      effects: languageSlot.current.reconfigure(languageSupport(language)),
    });
  }, [language]);

  useEffect(() => {
    view.current?.dispatch({
      effects: readOnlySlot.current.reconfigure(readOnlyState(readOnly)),
    });
  }, [readOnly]);

  return (
    <div
      ref={host}
      className="code-editor nodrag nopan nowheel nokey"
      data-language={language}
      data-readonly={readOnly ? '' : undefined}
    />
  );
}
