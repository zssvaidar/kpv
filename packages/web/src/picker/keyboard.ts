// The picker's keyboard rules for its entry list, as a plain function so they
// are tested without a DOM.
//
// The list is one tab stop (a roving tabindex): Tab enters it at the active
// entry and leaves it, and inside it the arrow keys move. That keeps a
// thousand-entry page from being a thousand Tab presses. Every entry is in the
// list, including those that cannot be opened, so each one is reached and its
// reason read out. Enter and Space are the entry button's own: they open it.

/** What a key pressed on an entry does. */
export type ListKeyAction =
  /** Move focus to the entry at this index. */
  | { kind: 'focus'; index: number }
  /** Open the parent directory, which the listing offered. */
  | { kind: 'parent' }
  /** Focus the field the path is typed in. */
  | { kind: 'pathField' };

export interface ListKey {
  key: string;
  altKey?: boolean;
  ctrlKey?: boolean;
  metaKey?: boolean;
}

/**
 * What `key`, pressed on the entry at `index` of `count`, does; `null` when
 * the key is not the list's and is left to the browser.
 */
export function listKeyAction(
  key: ListKey,
  index: number,
  count: number,
  hasParent: boolean,
): ListKeyAction | null {
  if (key.ctrlKey === true || key.metaKey === true) {
    return null;
  }
  if (key.altKey === true) {
    return key.key === 'ArrowUp' && hasParent ? { kind: 'parent' } : null;
  }
  if (count === 0) {
    return null;
  }
  const last = count - 1;
  const at = Math.min(Math.max(index, 0), last);
  switch (key.key) {
    case 'ArrowDown':
      return { kind: 'focus', index: Math.min(at + 1, last) };
    case 'ArrowUp':
      return { kind: 'focus', index: Math.max(at - 1, 0) };
    case 'Home':
      return { kind: 'focus', index: 0 };
    case 'End':
      return { kind: 'focus', index: last };
    case 'PageDown':
      return { kind: 'focus', index: Math.min(at + pageStep, last) };
    case 'PageUp':
      return { kind: 'focus', index: Math.max(at - pageStep, 0) };
    case 'Backspace':
      return hasParent ? { kind: 'parent' } : null;
    case '/':
      return { kind: 'pathField' };
    default:
      return null;
  }
}

/** How far PageUp and PageDown move. */
export const pageStep = 10;

/** Which entry is the list's tab stop: the active one, kept in range. */
export function rovingIndex(active: number, count: number): number {
  if (count === 0) {
    return -1;
  }
  return Math.min(Math.max(active, 0), count - 1);
}
