// The source files an opened pipeline's custom-code blocks show (#14), and
// every request for them, owned by the shell and apart from React so it is
// tested in Node against the real typed client. The nodes only draw what is
// here and report what is typed (blocks/BlockNode.tsx); nothing they do
// reaches the agent but through this.
//
//   - One buffer per path (blocks/source-buffer.ts), shared by every step
//     naming it. A path is read once, through `readSource` for the opened
//     pipeline's root and directory, when the pipeline is drawn, and again
//     only on `reload`. A read answered `notFound` is a file that does not
//     exist yet: an empty buffer a save creates with `expectedAbsent`.
//   - Typed code stays here, in memory, until `save`, which writes the
//     buffer as `textToWrite` makes it with the hash the read or the last
//     save answered. A save answered `changed` (or `exists` for a new file)
//     keeps what was typed and says so; nothing ever overwrites.
//   - Nothing is sent while there is no working connection; what was typed
//     stays.

import type { PipelineLocation, SourceError } from '@kpv/schema';

import type { AgentClient, AgentResult } from '../api/client.ts';
import type { RequestProblem } from '../connection/describe.ts';
import type { SourceBuffer } from '../blocks/source-buffer.ts';
import {
  edited,
  isUnsaved,
  loadedSource,
  textToWrite,
} from '../blocks/source-buffer.ts';

export interface SourcesState {
  buffers: Readonly<Partial<Record<string, SourceBuffer>>>;
  /**
   * The paths whose buffer is unsaved, in the order they were first read.
   * The same array until that set changes, so a subscriber that reads only
   * this is not woken by each keystroke.
   */
  unsaved: readonly string[];
}

export interface SourcesController {
  current: () => SourcesState;
  subscribe: (listener: () => void) => () => void;
  setOnline(online: boolean): void;
  /** Reads each of these paths not read yet (once it is online). */
  load(paths: readonly string[]): void;
  /** Reads the path again, dropping what was typed; the caller asks first. */
  reload(path: string): void;
  /** What the editor of this path now holds. */
  edit(path: string, text: string): void;
  /** Writes the path's buffer, when it is unsaved and can be written. */
  save(path: string): void;
  /** Drops these paths' buffers; the caller asks first. */
  forget(paths: readonly string[]): void;
}

const problemOf = (
  result: Exclude<AgentResult<unknown, SourceError>, { kind: 'ok' | 'error' }>,
): RequestProblem =>
  result.kind === 'failed'
    ? { kind: 'failed', status: result.status }
    : { kind: result.kind };

export function createSourcesController(
  client: AgentClient | null,
  location: PipelineLocation,
): SourcesController {
  let state: SourcesState = { buffers: {}, unsaved: [] };
  let online = false;
  // Every path asked for, in order, and the read or save of each that is
  // current: an answer to an older one is dropped.
  const wanted: string[] = [];
  const sequence = new Map<string, number>();
  const listeners = new Set<() => void>();

  const set = (buffers: SourcesState['buffers']) => {
    const unsaved = wanted.filter((path) => isUnsaved(buffers[path]));
    const same =
      unsaved.length === state.unsaved.length &&
      unsaved.every((path, i) => state.unsaved[i] === path);
    state = { buffers, unsaved: same ? state.unsaved : unsaved };
    for (const listener of listeners) listener();
  };
  const put = (path: string, buffer: SourceBuffer | undefined) => {
    const buffers = { ...state.buffers };
    if (buffer === undefined) delete buffers[path];
    else buffers[path] = buffer;
    set(buffers);
  };
  const next = (path: string) => {
    const n = (sequence.get(path) ?? 0) + 1;
    sequence.set(path, n);
    return n;
  };
  const where = (path: string) =>
    location.directory === undefined
      ? { root: location.root, path }
      : { root: location.root, directory: location.directory, path };

  const read = (path: string) => {
    if (client === null || !online) return;
    const n = next(path);
    put(path, { status: 'loading' });
    void client.readSource(where(path)).then((result) => {
      if (sequence.get(path) !== n) return;
      if (result.kind === 'ok') {
        put(path, loadedSource(result.value));
      } else if (result.kind === 'error') {
        put(
          path,
          result.error.error === 'notFound'
            ? loadedSource(null)
            : { status: 'refused', error: result.error },
        );
      } else {
        put(path, { status: 'problem', problem: problemOf(result) });
      }
    });
  };

  const readWanted = () => {
    for (const path of wanted) {
      if (state.buffers[path] === undefined) read(path);
    }
  };

  return {
    current: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    setOnline(now) {
      if (now === online) return;
      online = now;
      if (online) {
        readWanted();
        return;
      }
      // An answer still to come is for a connection that is gone: a read
      // stays to be read again, a save is left unsent as far as the page
      // knows, with what was typed kept.
      const buffers = { ...state.buffers };
      for (const [path, buffer] of Object.entries(state.buffers)) {
        next(path);
        if (buffer?.status === 'loading') delete buffers[path];
        if (buffer?.status === 'loaded' && buffer.saving) {
          buffers[path] = { ...buffer, saving: false };
        }
      }
      set(buffers);
    },
    load(paths) {
      for (const path of paths) {
        if (!wanted.includes(path)) wanted.push(path);
      }
      readWanted();
    },
    reload(path) {
      if (!wanted.includes(path)) wanted.push(path);
      const buffer = state.buffers[path];
      if (buffer?.status === 'loaded' && buffer.saving) return;
      read(path);
    },
    edit(path, text) {
      const buffer = state.buffers[path];
      if (buffer?.status !== 'loaded' || buffer.text === text) return;
      put(path, edited(buffer, text));
    },
    save(path) {
      const buffer = state.buffers[path];
      if (client === null || !online) return;
      if (buffer?.status !== 'loaded' || buffer.saving) return;
      if (!isUnsaved(buffer)) return;
      const text = textToWrite(buffer.disk?.text ?? '', buffer.text);
      const n = next(path);
      put(path, { ...buffer, saving: true, failure: null });
      const write =
        buffer.disk === null
          ? { ...where(path), text, expectedAbsent: true as const }
          : { ...where(path), text, expectedSha256: buffer.disk.sha256 };
      void client.writeSource(write).then((result) => {
        if (sequence.get(path) !== n) return;
        const now = state.buffers[path];
        if (now?.status !== 'loaded') return;
        if (result.kind === 'ok') {
          // What was typed since the save was sent stays, unsaved against
          // what was written.
          put(path, {
            ...loadedSource({
              text,
              sha256: result.value.sha256,
              generated: false,
            }),
            text: now.text,
          });
        } else if (result.kind === 'error') {
          const changed =
            result.error.error === 'exists' ||
            (result.error.error === 'changed' &&
              result.error.sha256 !== undefined);
          put(path, {
            ...now,
            saving: false,
            failure: changed
              ? { kind: 'changed' }
              : { kind: 'refused', error: result.error },
          });
        } else {
          put(path, {
            ...now,
            saving: false,
            failure: { kind: 'problem', problem: problemOf(result) },
          });
        }
      });
    },
    forget(paths) {
      const buffers = { ...state.buffers };
      for (const path of paths) {
        next(path);
        delete buffers[path];
        const at = wanted.indexOf(path);
        if (at >= 0) wanted.splice(at, 1);
      }
      set(buffers);
    },
  };
}
