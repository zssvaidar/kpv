// The review (#43): the pure diff model, the ending labels and which
// controls are enabled, tested without a DOM; the controller through the
// real typed client against a stand-in agent; the panel rendered to a
// string.

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import type { WorkcopyFile, WorkcopyReview } from '@kpv/schema';

import { fakeAgent, json } from '../picker/fake-agent.ts';
import { createReviewController } from './controller.ts';
import type { ReviewState } from './controller.ts';
import { controlWords, reviewControls } from './controls.ts';
import type { DiffRow } from './diff.ts';
import {
  changeSentence,
  diffLines,
  endingLabel,
  fileDiff,
  splitLines,
} from './diff.ts';
import { ReviewPanel, takenWords } from './ReviewPanel.tsx';
import {
  discardStagedQuestion,
  reviewErrorWords,
  reviewWords,
  stateWords,
} from './words.ts';

const location = { root: '/home/user/code', directory: 'orders' };
const revision = 'a'.repeat(32);
const snapshot = '20261122T101500Z-0a1b2c3d';

/** A row as a short string: marker, numbers, text and ending label. */
const show = (rows: DiffRow[]) =>
  rows.map((row) => {
    const ending =
      row.kind === 'same' ? '' : ` [${endingLabel[row.line.ending]}]`;
    switch (row.kind) {
      case 'same':
        return `  ${row.base}:${row.staged} ${row.line.text}`;
      case 'removed':
        return `- ${row.base}: ${row.line.text}${ending}`;
      case 'added':
        return `+ :${row.staged} ${row.line.text}${ending}`;
    }
  });

describe('the diff model', () => {
  it('splits lines keeping each ending, and a lone CR as text', () => {
    expect(splitLines('a\r\nb\nc')).toEqual([
      { text: 'a', ending: 'crlf' },
      { text: 'b', ending: 'lf' },
      { text: 'c', ending: 'none' },
    ]);
    expect(splitLines('')).toEqual([]);
    expect(splitLines('\n')).toEqual([{ text: '', ending: 'lf' }]);
    expect(splitLines('a\rb\n')).toEqual([{ text: 'a\rb', ending: 'lf' }]);
  });

  it('marks removed and added lines with line numbers on both sides', () => {
    expect(show(diffLines('a\nb\nc\nd\n', 'a\nB\nc\nd\ne\n'))).toEqual([
      '  1:1 a',
      '- 2: b [LF]',
      '+ :2 B [LF]',
      '  3:3 c',
      '  4:4 d',
      '+ :5 e [LF]',
    ]);
  });

  it('shows a change of line endings as changed lines with the ending named, never as an empty diff', () => {
    expect(show(diffLines('a\r\nb\r\n', 'a\nb\n'))).toEqual([
      '- 1: a [CRLF]',
      '- 2: b [CRLF]',
      '+ :1 a [LF]',
      '+ :2 b [LF]',
    ]);
  });

  it('shows a final newline added or removed as the last line changed', () => {
    expect(show(diffLines('a\nb', 'a\nb\n'))).toEqual([
      '  1:1 a',
      '- 2: b [no final newline]',
      '+ :2 b [LF]',
    ]);
    expect(show(diffLines('a\n', 'a'))).toEqual([
      '- 1: a [LF]',
      '+ :1 a [no final newline]',
    ]);
  });

  it('names every ending', () => {
    expect(endingLabel).toEqual({
      crlf: 'CRLF',
      lf: 'LF',
      none: 'no final newline',
    });
  });

  const file = (fields: Partial<WorkcopyFile>): WorkcopyFile => ({
    path: 'src/a.py',
    change: 'modified',
    state: 'ready',
    ...fields,
  });

  it('shows a deletion as the whole file removed and an added file as every line added', () => {
    expect(
      show(fileDiff(file({ change: 'deleted', baseText: 'a\nb\n' })) ?? []),
    ).toEqual(['- 1: a [LF]', '- 2: b [LF]']);
    expect(
      show(fileDiff(file({ change: 'added', stagedText: 'x' })) ?? []),
    ).toEqual(['+ :1 x [no final newline]']);
    expect(changeSentence(file({ change: 'deleted', baseText: 'a' }))).toBe(
      'Deleted: the whole file is removed',
    );
  });

  it('names an empty staged file as added or modified to empty', () => {
    expect(changeSentence(file({ change: 'added', stagedText: '' }))).toBe(
      'Added, as an empty file',
    );
    expect(changeSentence(file({ baseText: 'a\n', stagedText: '' }))).toBe(
      'Modified, to an empty file',
    );
    expect(
      show(fileDiff(file({ baseText: 'a\n', stagedText: '' })) ?? []),
    ).toEqual(['- 1: a [LF]']);
  });

  it('has no diff for a file whose project copy is not what it was staged from', () => {
    expect(fileDiff(file({ state: 'notText' }))).toBeNull();
    expect(fileDiff(file({ state: 'changed' }))).toBeNull();
  });

  it('diffs a large file without pairing past the cell limit, still every line', () => {
    const base = Array.from({ length: 3000 }, (_, i) => `a${i}\n`).join('');
    const staged = Array.from({ length: 3000 }, (_, i) => `b${i}\n`).join('');
    const rows = diffLines(base, staged);
    expect(rows.filter((row) => row.kind === 'removed')).toHaveLength(3000);
    expect(rows.filter((row) => row.kind === 'added')).toHaveLength(3000);
  });
});

const reviewOf = (fields: Partial<WorkcopyReview> = {}): WorkcopyReview => ({
  ...location,
  revision,
  files: [
    {
      path: 'src/main.py',
      change: 'modified',
      state: 'ready',
      baseText: 'a\r\n',
      stagedText: 'a\n',
    },
  ],
  unreadable: false,
  snapshots: [],
  ...fields,
});

describe('which controls are enabled', () => {
  it('enables apply and discard for a review of ready files', () => {
    expect(reviewControls(reviewOf(), null, false)).toEqual({
      apply: { enabled: true, reason: null },
      discard: { enabled: true, reason: null },
      snapshot: { enabled: true, reason: null },
    });
  });

  it('disables everything while disconnected, with the connection state as the reason', () => {
    const controls = reviewControls(reviewOf(), 'Agent not reachable', false);
    for (const control of Object.values(controls)) {
      expect(control).toEqual({
        enabled: false,
        reason: controlWords.unavailable('Agent not reachable'),
      });
    }
  });

  it('disables apply for a drifted file, and both with nothing staged or a request out', () => {
    const drifted = reviewOf({
      files: [{ path: 'a', change: 'modified', state: 'changed' }],
    });
    expect(reviewControls(drifted, null, false).apply.reason).toBe(
      controlWords.drift,
    );
    expect(reviewControls(drifted, null, false).discard.enabled).toBe(true);
    const empty = reviewOf({ files: [] });
    delete empty.revision;
    expect(reviewControls(empty, null, false).apply.reason).toBe(
      controlWords.nothingStaged,
    );
    expect(reviewControls(empty, null, false).discard.enabled).toBe(false);
    expect(reviewControls(null, null, false).apply.enabled).toBe(false);
    expect(reviewControls(reviewOf(), null, true).apply.reason).toBe(
      controlWords.busy,
    );
  });
});

describe('the review controller', () => {
  function started() {
    const agent = fakeAgent();
    const written: string[][] = [];
    const review = createReviewController(agent.client, location, (paths) =>
      written.push([...paths]),
    );
    return { agent, review, written };
  }

  it('reads nothing until online, then the review by root and directory', async () => {
    const { agent, review } = started();
    review.load();
    expect(agent.sent).toHaveLength(0);
    review.setOnline(true);
    const sent = agent.next('GET', '/api/workcopies/review');
    expect(sent.query.get('root')).toBe(location.root);
    expect(sent.query.get('directory')).toBe(location.directory);
    sent.answer(json(200, reviewOf()));
    await agent.settle();
    expect(review.current().review?.revision).toBe(revision);
  });

  it('applies only when asked, with the revision drawn, and tells its owner what was written', async () => {
    const { agent, review, written } = started();
    review.setOnline(true);
    agent.next('GET', '/api/workcopies/review').answer(json(200, reviewOf()));
    await agent.settle();
    expect(agent.sent).toHaveLength(1);
    review.apply();
    const apply = agent.next('POST', '/api/workcopies/apply');
    expect(apply.body).toEqual({ ...location, revision });
    apply.answer(
      json(200, {
        ...location,
        snapshot,
        complete: true,
        files: [{ path: 'src/main.py', result: 'written' }],
      }),
    );
    await agent.settle();
    expect(written).toEqual([['src/main.py']]);
    expect(review.current().report?.report.complete).toBe(true);
    // And the review is drawn again.
    agent.next('GET', '/api/workcopies/review');
  });

  it('draws the review again on changed, and keeps a drift to show', async () => {
    const { agent, review, written } = started();
    review.setOnline(true);
    agent.next('GET', '/api/workcopies/review').answer(json(200, reviewOf()));
    await agent.settle();
    review.apply();
    agent.next('POST', '/api/workcopies/apply').answer(
      json(409, {
        error: 'drift',
        message: 'm',
        drift: [{ path: 'src/main.py', now: 'changed' }],
      }),
    );
    await agent.settle();
    expect(review.current().failure?.error.error).toBe('drift');
    expect(written).toEqual([]);
    agent.next('GET', '/api/workcopies/review').answer(json(200, reviewOf()));
    await agent.settle();
    review.apply();
    agent
      .next('POST', '/api/workcopies/apply')
      .answer(json(409, { error: 'changed', message: 'm' }));
    await agent.settle();
    agent.next('GET', '/api/workcopies/review');
  });

  it('sends nothing while offline', () => {
    const { agent, review } = started();
    review.apply();
    review.discard();
    review.restore(snapshot);
    review.removeSnapshot(snapshot);
    expect(agent.sent).toHaveLength(0);
  });

  it('discards and removes a snapshot with exactly the request fields', async () => {
    const { agent, review } = started();
    review.setOnline(true);
    agent.next('GET', '/api/workcopies/review').answer(json(200, reviewOf()));
    await agent.settle();
    review.discard();
    const discard = agent.next('POST', '/api/workcopies/discard');
    expect(discard.body).toEqual({ ...location, revision });
    discard.answer(json(200, { ...location, discarded: 1 }));
    await agent.settle();
    agent.next('GET', '/api/workcopies/review').answer(json(200, reviewOf()));
    await agent.settle();
    review.removeSnapshot(snapshot);
    const removed = agent.next('DELETE', '/api/workcopies/snapshot');
    expect(removed.body).toEqual({ ...location, snapshot });
  });
});

describe('the review panel', () => {
  const state = (fields: Partial<ReviewState> = {}): ReviewState => ({
    online: true,
    open: true,
    loading: false,
    review: reviewOf(),
    readFailure: null,
    problem: null,
    busy: null,
    failure: null,
    report: null,
    ...fields,
  });
  const actions = {
    apply: () => undefined,
    restore: () => undefined,
    discard: () => undefined,
    removeSnapshot: () => undefined,
    hide: () => undefined,
  };
  const draw = (s: ReviewState, offline: string | null = null) =>
    renderToStaticMarkup(
      <ReviewPanel
        state={s}
        actions={actions}
        offline={offline}
        confirm={() => false}
      />,
    );

  it('renders to a string: the count, each file by path with its line diff and endings', () => {
    const html = draw(state());
    expect(html).toContain(reviewWords.staged(1));
    expect(html).toContain('src/main.py');
    expect(html).toContain('data-kind="removed"');
    expect(html).toContain('data-kind="added"');
    expect(html).toContain('CRLF');
    expect(html).toContain('>LF<');
    expect(html).not.toMatch(/<button type="button" disabled="">Apply/);
  });

  it('shows a drifted file by path with its sentence and no diff, and disables apply', () => {
    const html = draw(
      state({
        review: reviewOf({
          files: [{ path: 'src/x.bin', change: 'modified', state: 'notText' }],
        }),
      }),
    );
    expect(html).toContain('src/x.bin');
    expect(html).toContain(stateWords.notText);
    expect(html).not.toContain('review-diff');
    expect(html).toMatch(
      /<button type="button" disabled="" aria-describedby="[^"]+">Apply/,
    );
  });

  it('disables apply while disconnected with the connection state as the reason', () => {
    const html = draw(state(), 'Agent not reachable');
    expect(html).toContain(controlWords.unavailable('Agent not reachable'));
    expect(html).toMatch(/disabled="" aria-describedby="[^"]+">Apply/);
  });

  it('lists every drifted file of a refused apply, and offers nothing that overwrites', () => {
    const html = draw(
      state({
        failure: {
          action: 'apply',
          error: {
            error: 'drift',
            message: 'm',
            drift: [
              { path: 'src/a.py', now: 'changed' },
              { path: 'src/b.py', now: 'link' },
            ],
          },
        },
      }),
    );
    expect(html).toContain(reviewErrorWords.drift);
    expect(html).toContain('src/a.py');
    expect(html).toContain(stateWords.link);
    expect(html.toLowerCase()).not.toContain('overwrite</button>');
  });

  it('shows a partial apply path by path and offers restore', () => {
    const html = draw(
      state({
        report: {
          action: 'apply',
          report: {
            ...location,
            snapshot,
            complete: false,
            files: [
              { path: 'src/a.py', result: 'written' },
              { path: 'src/b.py', result: 'failed', kind: 'io' },
              { path: 'src/c.py', result: 'notWritten' },
            ],
          },
        },
      }),
    );
    expect(html).toContain(reviewWords.appliedPartly);
    expect(html).toContain('data-result="failed"');
    expect(html).toContain('data-result="notWritten"');
    expect(html).toContain('Restore the snapshot');
  });

  it('lists the snapshots with their UTC time and paths', () => {
    const html = draw(
      state({
        review: reviewOf({
          snapshots: [
            {
              id: snapshot,
              taken: '2026-11-22T10:15:00Z',
              paths: ['src/a.py'],
            },
          ],
        }),
      }),
    );
    expect(takenWords('2026-11-22T10:15:00Z')).toBe('2026-11-22 10:15:00 UTC');
    expect(html).toContain('2026-11-22 10:15:00 UTC');
    expect(html).toContain('>Restore<');
    expect(html).toContain('>Remove<');
  });

  it('says when the stored staged changes could not be read', () => {
    const html = draw(
      state({
        review: { ...location, files: [], unreadable: true, snapshots: [] },
      }),
    );
    expect(html).toContain(reviewWords.unreadable);
    expect(html).toContain(reviewWords.nothingStaged);
  });

  it('asks before discarding, naming how many files are staged', () => {
    expect(discardStagedQuestion(3)).toBe(
      'Discard the 3 staged files? The project stays as it is.',
    );
    expect(discardStagedQuestion(1)).toContain('1 staged file?');
  });
});
