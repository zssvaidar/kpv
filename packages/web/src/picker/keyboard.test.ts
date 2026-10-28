import { describe, expect, it } from 'vitest';

import { listKeyAction, pageStep, rovingIndex } from './keyboard.ts';

const key = (
  k: string,
  mods: { altKey?: boolean; ctrlKey?: boolean } = {},
) => ({
  key: k,
  ...mods,
});

describe('listKeyAction', () => {
  it('moves down and up one entry, stopping at either end', () => {
    expect(listKeyAction(key('ArrowDown'), 0, 3, false)).toEqual({
      kind: 'focus',
      index: 1,
    });
    expect(listKeyAction(key('ArrowDown'), 2, 3, false)).toEqual({
      kind: 'focus',
      index: 2,
    });
    expect(listKeyAction(key('ArrowUp'), 2, 3, false)).toEqual({
      kind: 'focus',
      index: 1,
    });
    expect(listKeyAction(key('ArrowUp'), 0, 3, false)).toEqual({
      kind: 'focus',
      index: 0,
    });
  });

  it('jumps to the first and last entry, and by a page', () => {
    expect(listKeyAction(key('Home'), 5, 9, false)).toEqual({
      kind: 'focus',
      index: 0,
    });
    expect(listKeyAction(key('End'), 0, 9, false)).toEqual({
      kind: 'focus',
      index: 8,
    });
    expect(listKeyAction(key('PageDown'), 0, 100, false)).toEqual({
      kind: 'focus',
      index: pageStep,
    });
    expect(listKeyAction(key('PageDown'), 95, 100, false)).toEqual({
      kind: 'focus',
      index: 99,
    });
    expect(listKeyAction(key('PageUp'), 3, 100, false)).toEqual({
      kind: 'focus',
      index: 0,
    });
  });

  it('keeps an index that is out of range inside the list', () => {
    // A listing that shrank under the active index.
    expect(listKeyAction(key('ArrowDown'), 40, 3, false)).toEqual({
      kind: 'focus',
      index: 2,
    });
  });

  it('goes up to the parent only when the listing offered one', () => {
    expect(listKeyAction(key('Backspace'), 0, 3, true)).toEqual({
      kind: 'parent',
    });
    expect(listKeyAction(key('ArrowUp', { altKey: true }), 0, 3, true)).toEqual(
      { kind: 'parent' },
    );
    expect(listKeyAction(key('Backspace'), 0, 3, false)).toBeNull();
    expect(
      listKeyAction(key('ArrowUp', { altKey: true }), 0, 3, false),
    ).toBeNull();
  });

  it('moves to the path field on /', () => {
    expect(listKeyAction(key('/'), 1, 3, false)).toEqual({
      kind: 'pathField',
    });
  });

  it('leaves Enter, Space, Tab and modified keys to the browser', () => {
    // Enter and Space are the entry button's own and open it; Tab leaves the
    // list, which is one tab stop.
    for (const k of ['Enter', ' ', 'Tab', 'a']) {
      expect(listKeyAction(key(k), 0, 3, true)).toBeNull();
    }
    expect(
      listKeyAction(key('ArrowDown', { ctrlKey: true }), 0, 3, true),
    ).toBeNull();
  });

  it('does nothing to an empty list but still goes up', () => {
    expect(listKeyAction(key('ArrowDown'), 0, 0, true)).toBeNull();
    expect(listKeyAction(key('ArrowUp', { altKey: true }), 0, 0, true)).toEqual(
      { kind: 'parent' },
    );
  });
});

describe('rovingIndex', () => {
  it('is the one tab stop of the list, kept in range', () => {
    expect(rovingIndex(0, 0)).toBe(-1);
    expect(rovingIndex(0, 4)).toBe(0);
    expect(rovingIndex(3, 4)).toBe(3);
    expect(rovingIndex(9, 4)).toBe(3);
    expect(rovingIndex(-2, 4)).toBe(0);
  });
});
