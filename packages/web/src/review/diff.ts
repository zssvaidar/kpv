// The line diff the review shows for one staged file (#43): pure, tested
// without a DOM, as blocks/source-buffer.ts is.
//
// A line is its text and its ending (CRLF, LF, or none for a last line with
// no final newline), and two lines are the same only when both are, so a
// change that is only line endings, or only the final newline, shows the
// affected lines as removed and added with the ending named, never as an
// empty diff. Only "\r\n" and "\n" end a line; a lone "\r" is text.

import type { WorkcopyFile } from '@kpv/schema';

export type LineEnding = 'crlf' | 'lf' | 'none';

export interface Line {
  text: string;
  ending: LineEnding;
}

export type DiffRow =
  | { kind: 'same'; base: number; staged: number; line: Line }
  | { kind: 'removed'; base: number; line: Line }
  | { kind: 'added'; staged: number; line: Line };

/** The words for an ending, as a changed line names it. */
export const endingLabel: Record<LineEnding, string> = {
  crlf: 'CRLF',
  lf: 'LF',
  none: 'no final newline',
};

/** The lines of a text, each with its own ending. An empty text has none. */
export function splitLines(text: string): Line[] {
  const lines: Line[] = [];
  let start = 0;
  for (let at = text.indexOf('\n'); at >= 0; at = text.indexOf('\n', start)) {
    const crlf = at > start && text[at - 1] === '\r';
    lines.push({
      text: text.slice(start, crlf ? at - 1 : at),
      ending: crlf ? 'crlf' : 'lf',
    });
    start = at + 1;
  }
  if (start < text.length)
    lines.push({ text: text.slice(start), ending: 'none' });
  return lines;
}

const same = (a: Line, b: Line): boolean =>
  a.text === b.text && a.ending === b.ending;

/**
 * The most cells the longest-common-subsequence table may have; a larger
 * middle is shown as all removed then all added, which is still every line
 * with its number, only not paired.
 */
export const maxDiffCells = 4_000_000;

/** The line diff of base to staged, with 1-based numbers on both sides. */
export function diffLines(baseText: string, stagedText: string): DiffRow[] {
  const base = splitLines(baseText);
  const staged = splitLines(stagedText);
  let head = 0;
  while (
    head < base.length &&
    head < staged.length &&
    same(base[head] as Line, staged[head] as Line)
  ) {
    head++;
  }
  let tail = 0;
  while (
    tail < base.length - head &&
    tail < staged.length - head &&
    same(
      base[base.length - 1 - tail] as Line,
      staged[staged.length - 1 - tail] as Line,
    )
  ) {
    tail++;
  }
  const rows: DiffRow[] = [];
  for (let i = 0; i < head; i++) {
    rows.push({
      kind: 'same',
      base: i + 1,
      staged: i + 1,
      line: base[i] as Line,
    });
  }
  const a = base.slice(head, base.length - tail);
  const b = staged.slice(head, staged.length - tail);
  rows.push(...middle(a, b, head));
  for (let i = tail; i > 0; i--) {
    rows.push({
      kind: 'same',
      base: base.length - i + 1,
      staged: staged.length - i + 1,
      line: base[base.length - i] as Line,
    });
  }
  return rows;
}

function middle(a: Line[], b: Line[], offset: number): DiffRow[] {
  const removedAll = (): DiffRow[] => [
    ...a.map((line, i): DiffRow => ({
      kind: 'removed',
      base: offset + i + 1,
      line,
    })),
    ...b.map((line, j): DiffRow => ({
      kind: 'added',
      staged: offset + j + 1,
      line,
    })),
  ];
  if (
    a.length === 0 ||
    b.length === 0 ||
    (a.length + 1) * (b.length + 1) > maxDiffCells
  ) {
    return removedAll();
  }
  // lcs[i][j] is the length of the longest common subsequence of a[i..] and
  // b[j..], in one flat array.
  const width = b.length + 1;
  const lcs = new Uint32Array((a.length + 1) * width);
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i * width + j] = same(a[i] as Line, b[j] as Line)
        ? (lcs[(i + 1) * width + j + 1] ?? 0) + 1
        : Math.max(lcs[(i + 1) * width + j] ?? 0, lcs[i * width + j + 1] ?? 0);
    }
  }
  const rows: DiffRow[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && same(a[i] as Line, b[j] as Line)) {
      rows.push({
        kind: 'same',
        base: offset + i + 1,
        staged: offset + j + 1,
        line: a[i] as Line,
      });
      i++;
      j++;
    } else if (
      j >= b.length ||
      (i < a.length &&
        (lcs[(i + 1) * width + j] ?? 0) >= (lcs[i * width + j + 1] ?? 0))
    ) {
      rows.push({ kind: 'removed', base: offset + i + 1, line: a[i] as Line });
      i++;
    } else {
      rows.push({ kind: 'added', staged: offset + j + 1, line: b[j] as Line });
      j++;
    }
  }
  return rows;
}

/** What one staged file's entry in the review says above its diff. */
export function changeSentence(file: WorkcopyFile): string {
  const empty = file.stagedText === '';
  switch (file.change) {
    case 'added':
      return empty ? 'Added, as an empty file' : 'Added';
    case 'modified':
      return empty ? 'Modified, to an empty file' : 'Modified';
    case 'deleted':
      return 'Deleted: the whole file is removed';
  }
}

/**
 * The diff of one staged file, or null when the review has no texts for it
 * because the project file is not what it was staged from. A deletion is
 * every line removed; an added file every line added.
 */
export function fileDiff(file: WorkcopyFile): DiffRow[] | null {
  if (file.state !== 'ready') return null;
  return diffLines(file.baseText ?? '', file.stagedText ?? '');
}
