// The pipeline list (#48): the entry screen of the graph area. It lists every
// pipeline under the authorized roots as the agent answered, offers to open a
// readable one and to create one, and says in words why anything else is as
// it is. Everything comes from the controller (controller.ts); this file only
// renders its state and hands it the user's actions. Every path and name the
// agent sends is rendered as React text.

import { useId } from 'react';

import type {
  PipelineEntry,
  PipelineProblem,
  PipelineRoot,
  PipelinesError,
} from '@kpv/schema';

import { describeRequestProblem } from '../connection/describe.ts';
import {
  createRequest,
  fieldProblems,
  type CreateDraft,
  type CreateField,
} from './create-form.ts';
import {
  creatableRoots,
  type OpenFailure,
  type PipelinesActions,
  type PipelinesState,
} from './controller.ts';
import {
  duplicateWords,
  emptyRootWords,
  leftBehindWords,
  locationWords,
  noRootsWords,
  pipelinesErrorTitles,
  rootReasonWords,
  skippedLinksNote,
  truncatedNote,
  unreadableWords,
} from './words.ts';

/** The id of the directory picker's heading, which the empty state links to. */
export const pickerAnchor = 'directory-picker';

/** The list screen, rendered from the controller's state alone. */
export function PipelinesView(props: {
  state: PipelinesState;
  actions: PipelinesActions;
}) {
  const { state, actions } = props;
  if (!state.session) {
    return (
      <p className="muted">
        This page has no session with the agent, so no pipeline can be listed.
      </p>
    );
  }
  const roots = creatableRoots(state.list);
  return (
    <section className="pipelines" aria-labelledby="pipelines-heading">
      <header className="pipelines-header">
        <h1 id="pipelines-heading">Pipelines</h1>
        <button
          type="button"
          onClick={() => {
            actions.refresh();
          }}
          disabled={!state.online || state.loading}
        >
          Refresh
        </button>
        {roots.length > 0 && state.draft === null ? (
          <button
            type="button"
            onClick={() => {
              actions.startCreate();
            }}
            disabled={!state.online}
          >
            New pipeline
          </button>
        ) : null}
      </header>
      {state.loading ? (
        <p className="muted" role="status">
          Reading the pipelines…
        </p>
      ) : null}
      {state.problem === null ? null : (
        <ProblemNotice
          title={describeRequestProblem(state.problem).label}
          detail={describeRequestProblem(state.problem).detail}
        />
      )}
      {state.listError === null ? null : (
        <ErrorNotice error={state.listError} />
      )}
      {state.draft === null ? null : (
        <CreateForm
          draft={state.draft}
          roots={roots}
          creating={state.creating}
          online={state.online}
          failure={state.createFailure}
          actions={actions}
        />
      )}
      {state.list === null ? null : state.list.roots.length === 0 ? (
        <div className="pipelines-empty" data-state="no-roots">
          <p>{noRootsWords}</p>
          <a href={`#${pickerAnchor}`}>Choose a project directory</a>
        </div>
      ) : (
        state.list.roots.map((root) => (
          <RootView
            key={root.path}
            root={root}
            creatable={roots.includes(root.path)}
            state={state}
            actions={actions}
          />
        ))
      )}
    </section>
  );
}

function RootView(props: {
  root: PipelineRoot;
  creatable: boolean;
  state: PipelinesState;
  actions: PipelinesActions;
}) {
  const { root, creatable, state, actions } = props;
  return (
    <section
      className="pipeline-root"
      data-available={root.available}
      aria-label={root.path}
    >
      <h2>{root.path}</h2>
      {!root.available ? (
        <p className="root-unavailable" data-reason={root.reason}>
          {rootReasonWords[root.reason ?? 'io']}
        </p>
      ) : (
        <>
          {root.truncated ? (
            <p className="root-truncated" role="note">
              {truncatedNote}
            </p>
          ) : null}
          {root.skippedLinks > 0 ? (
            <p className="muted">{skippedLinksNote(root.skippedLinks)}</p>
          ) : null}
          {root.pipelines.length === 0 ? (
            <div className="pipelines-empty" data-state="empty-root">
              <p>{emptyRootWords}</p>
              {creatable ? (
                <button
                  type="button"
                  onClick={() => {
                    actions.startCreate(root.path);
                  }}
                  disabled={!state.online}
                >
                  Create a pipeline here
                </button>
              ) : null}
            </div>
          ) : (
            <ul className="pipeline-entries">
              {root.pipelines.map((entry) => (
                <EntryView
                  key={entry.directory ?? ''}
                  entry={entry}
                  state={state}
                  actions={actions}
                />
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  );
}

function EntryView(props: {
  entry: PipelineEntry;
  state: PipelinesState;
  actions: PipelinesActions;
}) {
  const { entry, state, actions } = props;
  const where = entry.directory ?? 'this project directory itself';
  if (!entry.readable || entry.unreadable !== undefined) {
    const unreadable = entry.unreadable ?? {
      kind: 'unreadable' as const,
      problems: [],
    };
    return (
      <li className="pipeline-entry" data-readable="false">
        <span className="entry-directory">{where}</span>{' '}
        <span className="entry-kind" data-kind={unreadable.kind}>
          Unreadable: {unreadableWords[unreadable.kind]}
        </span>
        <ProblemList problems={unreadable.problems} />
      </li>
    );
  }
  const location =
    entry.directory === undefined
      ? { root: entry.root }
      : { root: entry.root, directory: entry.directory };
  const opening =
    state.opening !== null &&
    state.opening.root === entry.root &&
    state.opening.directory === entry.directory;
  return (
    <li className="pipeline-entry" data-readable="true">
      <span className="entry-name">{entry.name ?? entry.id}</span>{' '}
      <span className="muted">
        {entry.id} · {entry.language} · {where}
        {entry.modified === undefined ? null : ` · modified ${entry.modified}`}
      </span>{' '}
      <button
        type="button"
        onClick={() => {
          actions.open(location);
        }}
        disabled={!state.online || opening}
      >
        {opening ? 'Opening…' : 'Open'}
      </button>
      {entry.duplicateId === undefined ? null : (
        <p className="entry-duplicate">{duplicateWords(entry.duplicateId)}</p>
      )}
    </li>
  );
}

/** Each problem with its pointer and, when the agent knows it, its line. */
export function ProblemList(props: { problems: PipelineProblem[] }) {
  if (props.problems.length === 0) return null;
  return (
    <ul className="pipeline-problems">
      {props.problems.map((problem, index) => (
        <li key={index}>
          <code>
            {problem.pointer === '' ? '(the document)' : problem.pointer}
          </code>
          {problem.line === undefined ? null : (
            <span>
              {' '}
              line {problem.line}
              {problem.column === undefined
                ? null
                : `, column ${problem.column}`}
            </span>
          )}
          : {problem.message}
        </li>
      ))}
    </ul>
  );
}

function ProblemNotice(props: { title: string; detail: string }) {
  return (
    <div className="pipelines-problem" role="alert">
      <strong>{props.title}</strong> {props.detail}
    </div>
  );
}

/** An error the agent answered, in words, with what it carries. */
export function ErrorNotice(props: { error: PipelinesError }) {
  const { error } = props;
  const left = leftBehindWords(error.directoryLeftBehind);
  return (
    <div className="pipelines-error" role="alert" data-error={error.error}>
      <strong>{pipelinesErrorTitles[error.error]}</strong> {error.message}
      {error.reason === undefined ? null : (
        <p>{rootReasonWords[error.reason]}</p>
      )}
      {error.unreadable === undefined ? null : (
        <>
          <p data-kind={error.unreadable.kind}>
            {unreadableWords[error.unreadable.kind]}
          </p>
          <ProblemList problems={error.unreadable.problems} />
        </>
      )}
      {error.problems === undefined ? null : (
        <ProblemList problems={error.problems} />
      )}
      {left === null ? null : <p>{left}</p>}
    </div>
  );
}

/** A failed open or create: what the agent said, or why the page refused. */
export function FailureNotice(props: { failure: OpenFailure }) {
  const { failure } = props;
  switch (failure.kind) {
    case 'error':
      return <ErrorNotice error={failure.error} />;
    case 'invalid':
      return (
        <div className="pipelines-error" role="alert" data-error="unreadable">
          <strong>{pipelinesErrorTitles.unreadable}</strong>{' '}
          {unreadableWords.schema}
          <ProblemList problems={failure.problems} />
        </div>
      );
    case 'problem': {
      const described = describeRequestProblem(failure.problem);
      return (
        <ProblemNotice title={described.label} detail={described.detail} />
      );
    }
  }
}

/**
 * Shown in the graph area in place of the graph when an entry did not open,
 * so nothing half-loaded is ever drawn.
 */
export function OpenFailureView(props: {
  state: PipelinesState;
  actions: PipelinesActions;
}) {
  const { state, actions } = props;
  if (state.openFailure === null) return null;
  return (
    <section className="open-failure" aria-label="Pipeline not opened">
      <h1>{locationWords(state.openFailure.location)}</h1>
      <FailureNotice failure={state.openFailure.failure} />
      <button
        type="button"
        onClick={() => {
          actions.close();
        }}
      >
        Back to the pipelines
      </button>
    </section>
  );
}

const fieldLabels: Record<CreateField, string> = {
  name: 'Name',
  language: 'Language',
  sourceTopic: 'Source topic',
  destinationTopic: 'Destination topic',
  networkMode: 'Network mode',
};

const choices: Partial<Record<CreateField, readonly string[]>> = {
  language: ['python', 'typescript', 'go'],
  networkMode: ['docker', 'host'],
};

function CreateForm(props: {
  draft: CreateDraft;
  roots: string[];
  creating: boolean;
  online: boolean;
  failure: OpenFailure | null;
  actions: PipelinesActions;
}) {
  const { draft, roots, creating, online, failure, actions } = props;
  const id = useId();
  const problems = fieldProblems(draft);
  const ready = createRequest(draft, roots) !== null;
  return (
    <form
      className="create-pipeline"
      aria-label="New pipeline"
      onSubmit={(event) => {
        event.preventDefault();
        actions.submitCreate();
      }}
    >
      <h2>New pipeline</h2>
      {roots.length > 1 ? (
        <label>
          Project directory{' '}
          <select
            value={draft.root}
            onChange={(event) => {
              actions.edit('root', event.target.value);
            }}
          >
            <option value="">Choose one</option>
            {roots.map((root) => (
              <option key={root} value={root}>
                {root}
              </option>
            ))}
          </select>
        </label>
      ) : (
        <p className="muted">In {draft.root}</p>
      )}
      {(Object.keys(fieldLabels) as CreateField[]).map((field) => {
        const options = choices[field];
        const problemId = `${id}-${field}-problem`;
        const problem = problems[field];
        return (
          <label key={field} data-field={field}>
            {fieldLabels[field]}{' '}
            {options === undefined ? (
              <input
                value={draft[field]}
                aria-invalid={problem !== null}
                aria-describedby={problem === null ? undefined : problemId}
                onChange={(event) => {
                  actions.edit(field, event.target.value);
                }}
              />
            ) : (
              <select
                value={draft[field]}
                aria-invalid={problem !== null}
                aria-describedby={problem === null ? undefined : problemId}
                onChange={(event) => {
                  actions.edit(field, event.target.value);
                }}
              >
                <option value="">Choose one</option>
                {options.map((option) => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
            )}
            {problem === null ? null : (
              <span className="field-problem" id={problemId}>
                {problem}
              </span>
            )}
          </label>
        );
      })}
      {failure === null ? null : <FailureNotice failure={failure} />}
      <button type="submit" disabled={!ready || creating || !online}>
        {creating ? 'Creating…' : 'Create'}
      </button>
      <button
        type="button"
        onClick={() => {
          actions.cancelCreate();
        }}
      >
        Cancel
      </button>
    </form>
  );
}
