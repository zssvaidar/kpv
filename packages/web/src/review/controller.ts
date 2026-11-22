// The review's owning state (#43): every request it makes, through the one
// client, for one opened pipeline. It reads the review when it is asked to
// (when the pipeline is drawn, when the review is opened, and after every
// action), and sends apply, restore, discard and snapshot removal only when
// the person activates them. A `changed` answer, which means the review is
// out of date, draws it again. After an apply or restore it tells its owner
// which paths were written, so the shell re-reads the buffers it holds for
// them (shell/sources.ts).

import type {
  PipelineLocation,
  WorkcopyError,
  WorkcopyReport,
  WorkcopyReview,
} from '@kpv/schema';

import type { AgentClient, AgentResult } from '../api/client.ts';
import type { RequestProblem } from '../connection/describe.ts';

export type ReviewAction = 'apply' | 'restore' | 'discard' | 'remove';

export interface ReviewState {
  online: boolean;
  /** Whether the review is shown. */
  open: boolean;
  loading: boolean;
  review: WorkcopyReview | null;
  /** Why the last read got no review. */
  readFailure: WorkcopyError | null;
  /** A request that got no usable answer. */
  problem: RequestProblem | null;
  busy: ReviewAction | null;
  /** The last action the agent refused, and why. */
  failure: { action: ReviewAction; error: WorkcopyError } | null;
  /** The last apply's or restore's report. */
  report: { action: 'apply' | 'restore'; report: WorkcopyReport } | null;
}

export interface ReviewController {
  current: () => ReviewState;
  subscribe: (listener: () => void) => () => void;
  setOnline(online: boolean): void;
  /** Reads the review again. */
  load(): void;
  show(): void;
  hide(): void;
  /** Applies what the review drawn now shows; the person activated it. */
  apply(): void;
  /** Restores a snapshot; the person activated it. */
  restore(snapshot: string): void;
  /** Discards the working copy; the caller has asked first. */
  discard(): void;
  /** Removes a snapshot; the caller has asked first. */
  removeSnapshot(snapshot: string): void;
}

const problemOf = (
  result: Exclude<
    AgentResult<unknown, WorkcopyError>,
    { kind: 'ok' | 'error' }
  >,
): RequestProblem =>
  result.kind === 'failed'
    ? { kind: 'failed', status: result.status }
    : { kind: result.kind };

export function createReviewController(
  client: AgentClient | null,
  location: PipelineLocation,
  onWritten: (paths: readonly string[]) => void = () => undefined,
): ReviewController {
  let state: ReviewState = {
    online: false,
    open: false,
    loading: false,
    review: null,
    readFailure: null,
    problem: null,
    busy: null,
    failure: null,
    report: null,
  };
  let sequence = 0;
  const listeners = new Set<() => void>();
  const set = (next: Partial<ReviewState>) => {
    state = { ...state, ...next };
    for (const listener of listeners) listener();
  };
  const where = (): PipelineLocation =>
    location.directory === undefined
      ? { root: location.root }
      : { root: location.root, directory: location.directory };

  const load = () => {
    if (client === null || !state.online) return;
    const n = ++sequence;
    set({ loading: true });
    void client.reviewWorkcopy(where()).then((result) => {
      if (n !== sequence) return;
      if (result.kind === 'ok') {
        set({
          loading: false,
          review: result.value,
          readFailure: null,
          problem: null,
        });
      } else if (result.kind === 'error') {
        set({
          loading: false,
          review: null,
          readFailure: result.error,
          problem: null,
        });
      } else {
        set({ loading: false, problem: problemOf(result) });
      }
    });
  };

  const send = <T>(
    action: ReviewAction,
    request: () => Promise<AgentResult<T, WorkcopyError>>,
    done: (value: T) => void,
  ) => {
    if (client === null || !state.online || state.busy !== null) return;
    set({ busy: action, failure: null, problem: null });
    void request().then((result) => {
      if (result.kind === 'ok') {
        set({ busy: null });
        done(result.value);
      } else if (result.kind === 'error') {
        set({ busy: null, failure: { action, error: result.error } });
      } else {
        set({ busy: null, problem: problemOf(result) });
      }
      // Whatever happened, the review is drawn again from the agent.
      load();
    });
  };

  const written = (report: WorkcopyReport) =>
    report.files
      .filter((file) => file.result === 'written')
      .map((file) => file.path);

  return {
    current: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    setOnline(online) {
      if (online === state.online) return;
      sequence++;
      set({ online, loading: false, busy: null });
      if (online) load();
    },
    load,
    show() {
      set({ open: true });
      load();
    },
    hide() {
      set({ open: false });
    },
    apply() {
      const revision = state.review?.revision;
      if (revision === undefined) return;
      send(
        'apply',
        () => (client as AgentClient).applyWorkcopy({ ...where(), revision }),
        (report) => {
          set({ report: { action: 'apply', report } });
          onWritten(written(report));
        },
      );
    },
    restore(snapshot) {
      send(
        'restore',
        () => (client as AgentClient).restoreSnapshot({ ...where(), snapshot }),
        (report) => {
          set({ report: { action: 'restore', report } });
          onWritten(written(report));
        },
      );
    },
    discard() {
      const revision = state.review?.revision;
      if (revision === undefined) return;
      send(
        'discard',
        () => (client as AgentClient).discardWorkcopy({ ...where(), revision }),
        () => set({ report: null }),
      );
    },
    removeSnapshot(snapshot) {
      send(
        'remove',
        () => (client as AgentClient).removeSnapshot({ ...where(), snapshot }),
        () => undefined,
      );
    },
  };
}
