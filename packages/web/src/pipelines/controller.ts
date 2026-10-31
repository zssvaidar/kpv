// The pipeline list's state and every request it makes (#48), apart from
// React, so it is tested in Node against the real typed client.
//
//   - The list is read again on every refresh, and on every return to the
//     list screen, the way the agent reads the disk again on every call.
//     The latest read wins: an answer is applied only if no read was started
//     after it.
//   - Opening an entry sends only its root and directory, as the list gave
//     them. The answer's pipeline is parsed with schemaIssues (graph/edit.ts)
//     before anything is drawn; a document that fails there is shown as
//     unreadable with its problems, never drawn. The pipeline that is drawn
//     is the document as the agent sent it, not the parse output, so no
//     default is filled in.
//   - A request that is rejected, unanswered or not understood is a
//     connection problem, said in #8's words, and not a list error.

import type {
  Pipeline,
  PipelineDocument,
  PipelineList,
  PipelineLocation,
  PipelineProblem,
  PipelinesError,
} from '@kpv/schema';

import type { AgentClient, AgentResult } from '../api/client.ts';
import type { RequestProblem } from '../connection/describe.ts';
import { schemaIssues } from '../graph/edit.ts';
import {
  createRequest,
  emptyDraft,
  type CreateDraft,
  type CreateField,
} from './create-form.ts';

/** An opened pipeline, as the graph draws it. */
export interface OpenedPipeline {
  location: PipelineLocation;
  pipeline: Pipeline;
  sha256: string;
}

/** Why an entry did not open. */
export type OpenFailure =
  /** The agent's own answer: gone, unreadable by now, root unavailable... */
  | { kind: 'error'; error: PipelinesError }
  /** The agent sent a document the page's schema check refused. */
  | { kind: 'invalid'; problems: PipelineProblem[] }
  /** The request itself: rejected, unanswered or not understood. */
  | { kind: 'problem'; problem: RequestProblem };

export interface PipelinesState {
  /** Whether the page has a session with the agent at all. */
  session: boolean;
  online: boolean;
  /** The last list read, `null` before the first. */
  list: PipelineList | null;
  listError: PipelinesError | null;
  /** A read is in flight. */
  loading: boolean;
  /** The last list read's connection problem, until one succeeds. */
  problem: RequestProblem | null;
  /** The entry being opened. */
  opening: PipelineLocation | null;
  opened: OpenedPipeline | null;
  /** Why the last open failed, shown in place of the graph. */
  openFailure: { location: PipelineLocation; failure: OpenFailure } | null;
  /** The create form, when it is open. */
  draft: CreateDraft | null;
  creating: boolean;
  createFailure: OpenFailure | null;
}

export interface PipelinesActions {
  refresh(): void;
  open(location: PipelineLocation): void;
  /** Back to the list from an opened pipeline or a failed open. */
  close(): void;
  startCreate(root?: string): void;
  edit(field: CreateField | 'root', value: string): void;
  cancelCreate(): void;
  submitCreate(): void;
}

export interface PipelinesController extends PipelinesActions {
  current: () => PipelinesState;
  subscribe: (listener: () => void) => () => void;
  setOnline(online: boolean): void;
}

/** The roots a pipeline can be created in: available, not themselves one. */
export function creatableRoots(list: PipelineList | null): string[] {
  if (list === null) return [];
  return list.roots
    .filter(
      (root) =>
        root.available &&
        !root.pipelines.some((entry) => entry.directory === undefined),
    )
    .map((root) => root.path);
}

/**
 * The pipeline a document holds, once the page's schema check accepts it, or
 * the problems it found by pointer.
 */
export function checkDocument(
  document: PipelineDocument,
):
  | { ok: true; pipeline: Pipeline }
  | { ok: false; problems: PipelineProblem[] } {
  const candidate = document.pipeline as unknown as Pipeline;
  const issues = schemaIssues(candidate);
  if (issues.length === 0) return { ok: true, pipeline: candidate };
  return {
    ok: false,
    problems: issues.map((issue) => ({
      pointer:
        issue.path.length === 0
          ? ''
          : `/${issue.path.map((part) => String(part)).join('/')}`,
      message: issue.message,
    })),
  };
}

function problemOf<T, E>(result: AgentResult<T, E>): RequestProblem | null {
  switch (result.kind) {
    case 'rejected':
    case 'unreachable':
      return { kind: result.kind };
    case 'failed':
      return { kind: 'failed', status: result.status };
    default:
      return null;
  }
}

/** Whether two locations are the same entry. */
export const sameLocation = (a: PipelineLocation, b: PipelineLocation) =>
  a.root === b.root && a.directory === b.directory;

export function createPipelinesController(
  client: AgentClient | null,
): PipelinesController {
  let state: PipelinesState = {
    session: client !== null,
    online: false,
    list: null,
    listError: null,
    loading: false,
    problem: null,
    opening: null,
    opened: null,
    openFailure: null,
    draft: null,
    creating: false,
    createFailure: null,
  };
  const listeners = new Set<() => void>();
  const set = (next: Partial<PipelinesState>) => {
    state = { ...state, ...next };
    for (const listener of listeners) listener();
  };
  let readSequence = 0;
  let openSequence = 0;

  const refresh = () => {
    if (client === null || !state.online) return;
    const sequence = ++readSequence;
    set({ loading: true });
    void client.listPipelines().then((result) => {
      if (sequence !== readSequence) return;
      if (result.kind === 'ok') {
        set({
          loading: false,
          list: result.value,
          listError: null,
          problem: null,
        });
      } else if (result.kind === 'error') {
        set({ loading: false, listError: result.error, problem: null });
      } else {
        set({ loading: false, problem: problemOf(result) });
      }
    });
  };

  const settleDocument = (
    location: PipelineLocation,
    result: AgentResult<PipelineDocument, PipelinesError>,
  ): { opened: OpenedPipeline } | { failure: OpenFailure } => {
    if (result.kind === 'ok') {
      const checked = checkDocument(result.value);
      if (checked.ok) {
        return {
          opened: {
            location,
            pipeline: checked.pipeline,
            sha256: result.value.sha256,
          },
        };
      }
      return { failure: { kind: 'invalid', problems: checked.problems } };
    }
    if (result.kind === 'error') {
      return { failure: { kind: 'error', error: result.error } };
    }
    const problem = problemOf(result) ?? { kind: 'unreachable' as const };
    return { failure: { kind: 'problem', problem } };
  };

  const open = (location: PipelineLocation) => {
    if (client === null || !state.online) return;
    const sequence = ++openSequence;
    set({ opening: location, openFailure: null, opened: null });
    void client.openPipeline(location).then((result) => {
      if (sequence !== openSequence) return;
      const settled = settleDocument(location, result);
      if ('opened' in settled) {
        set({ opening: null, opened: settled.opened, openFailure: null });
      } else {
        set({
          opening: null,
          opened: null,
          openFailure: { location, failure: settled.failure },
        });
      }
    });
  };

  return {
    current: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    setOnline(online) {
      if (online === state.online) return;
      set({ online });
      if (online) refresh();
      else {
        readSequence++;
        openSequence++;
        set({ loading: false, opening: null });
      }
    },
    refresh,
    open,
    close() {
      openSequence++;
      set({ opened: null, openFailure: null, opening: null });
      refresh();
    },
    startCreate(root) {
      const roots = creatableRoots(state.list);
      const chosen =
        root !== undefined && roots.includes(root)
          ? root
          : roots.length === 1
            ? (roots[0] ?? '')
            : '';
      set({ draft: emptyDraft(chosen), createFailure: null });
    },
    edit(field, value) {
      if (state.draft === null) return;
      set({ draft: { ...state.draft, [field]: value } });
    },
    cancelCreate() {
      set({ draft: null, createFailure: null });
    },
    submitCreate() {
      if (client === null || !state.online || state.creating) return;
      if (state.draft === null) return;
      const request = createRequest(state.draft, creatableRoots(state.list));
      if (request === null) return;
      set({ creating: true, createFailure: null });
      void client.createPipeline(request).then((result) => {
        const location: PipelineLocation =
          result.kind === 'ok' && result.value.directory !== undefined
            ? { root: result.value.root, directory: result.value.directory }
            : { root: request.root };
        const settled = settleDocument(location, result);
        if ('opened' in settled) {
          openSequence++;
          set({
            creating: false,
            draft: null,
            opened: settled.opened,
            openFailure: null,
          });
        } else {
          set({ creating: false, createFailure: settled.failure });
        }
        refresh();
      });
    },
  };
}
