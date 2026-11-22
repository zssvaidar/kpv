// The layout shell: a status bar, then the three regions every later screen
// fills in -- the graph area (#9), the side panel (#12, #13) and the bottom
// panel. The graph area's entry screen is the pipeline list (#48): opening an
// entry, or creating a pipeline, draws it (#9), and the side panel shows
// which block is selected in it, with the controls to edit the chain (#11),
// and the form that configures it: for the Kafka source or destination (#12),
// and for a filter, transform or validate step (#13). An entry that did not
// open says why in place of the graph. Edits are kept here, in memory, as the pipeline the
// graph draws; saving them is #3. So is what was typed into a form and not
// yet saved into that pipeline, per block, so selecting another block keeps
// it.
//
// The status bar also leads to the Kafka connection screen (#24), which takes
// the graph area's place until it is closed: connections are their own
// screen, never part of the block side panel. The Kafka forms name one by
// a select of the connection names the agent lists (#12), read again each
// time a Kafka block's form is shown.
//
// A custom-code block shows its step's file in an editor on its node (#14).
// The files are read and written here (sources.ts), never by the node: each
// distinct path once when an opened pipeline is drawn and again when the
// person asks to reload, and written only when they save. What was typed is
// held here, in memory; deleting the last step naming a file with unsaved
// code, going back to the list (the way to open another pipeline) or
// reloading that file asks first, and the file on disk is never deleted.
//
// The connection state is in the status bar on every screen, and when there
// is no working connection the graph area says why in words, so a dead or
// refusing agent never looks like an empty screen. An opened pipeline stays
// drawn below those words, so code typed and not yet saved stays in view,
// with its Save disabled until the agent is back.

import { useEffect, useMemo, useState, useSyncExternalStore } from 'react';

import type { Pipeline } from '@kpv/schema';

import type { AgentClient } from '../api/client.ts';
import { ConnectionsView } from '../connections/ConnectionsScreen.tsx';
import {
  createConnectionsController,
  type ConnectionsController,
} from '../connections/controller.ts';
import {
  droppedPaths,
  sourcePaths,
  stepsNaming,
  unsavedDroppedBy,
} from '../blocks/source-buffer.ts';
import { describeConnection } from '../connection/describe.ts';
import type {
  ConnectionMonitor,
  ConnectionState,
} from '../connection/monitor.ts';
import type { EditResult } from '../graph/edit.ts';
import { EditControls } from '../graph/EditControls.tsx';
import { blockForNode, nodeId, placedBlocks } from '../graph/layout.ts';
import type { CodeSources } from '../graph/PipelineGraph.tsx';
import { PipelineGraph } from '../graph/PipelineGraph.tsx';
import { SelectionSummary } from '../graph/SelectionSummary.tsx';
import {
  createConnectionNamesController,
  type ConnectionNamesController,
} from '../panel/connection-names.ts';
import type { KafkaDraft, KafkaRole } from '../panel/kafka-form.ts';
import { saveDraft } from '../panel/kafka-form.ts';
import { KafkaBlockForm, UnsavedElsewhere } from '../panel/KafkaBlockForm.tsx';
import type { StepDraft } from '../panel/step-form.ts';
import { saveStepDraft, stepFor } from '../panel/step-form.ts';
import { StepBlockForm } from '../panel/StepBlockForm.tsx';
import { DirectoryPicker } from '../picker/DirectoryPicker.tsx';
import {
  createPipelinesController,
  type PipelinesController,
} from '../pipelines/controller.ts';
import {
  OpenFailureView,
  pickerAnchor,
  PipelinesView,
} from '../pipelines/PipelinesScreen.tsx';
import { createSourcesController, type SourcesController } from './sources.ts';

/**
 * What the configuration forms hold that the pipeline does not, by the node
 * id of the block it is for: 'source', 'destination' or 'step:<id>'.
 */
type Drafts = Partial<Record<string, KafkaDraft | StepDraft>>;

const withoutDraft = (drafts: Drafts, node: string): Drafts => {
  const next = { ...drafts };
  delete next[node];
  return next;
};

/** The drafts whose block is still in the pipeline: a deleted step's go. */
const keptDrafts = (pipeline: Pipeline, drafts: Drafts): Drafts =>
  Object.fromEntries(
    Object.entries(drafts).filter(
      ([node]) => blockForNode(pipeline, node) !== null,
    ),
  );

const stepNode = (id: string): string => `step:${id}`;

const noSubscription = () => () => undefined;
const noPaths: readonly string[] = [];

/** What the person is asked before typed code is dropped. */
export const discardQuestion = (paths: readonly string[]): string =>
  `Discard the unsaved code in ${paths.join(', ')}? The file${paths.length === 1 ? '' : 's'} on disk stay${paths.length === 1 ? 's' : ''} as last saved.`;

export function App(props: {
  client: AgentClient | null;
  monitor: ConnectionMonitor;
}) {
  const { client, monitor } = props;
  const state = useSyncExternalStore(
    monitor.subscribe,
    monitor.current,
    monitor.current,
  );
  return <Shell state={state} client={client} />;
}

/**
 * The shell for one connection state, so it can be rendered without an agent.
 * A test hands it a pipeline to draw, or a pipelines controller in the state
 * it wants to show; the page builds the controller from the client.
 */
export function Shell(props: {
  state: ConnectionState;
  client: AgentClient | null;
  pipeline?: Pipeline | null;
  pipelines?: PipelinesController;
  connections?: ConnectionsController;
  connectionNames?: ConnectionNamesController;
  /** Which screen the graph area starts on; the pipelines by default. */
  screen?: 'pipelines' | 'connections';
  /** The custom-code files; by default one controller per opened pipeline. */
  sources?: SourcesController;
  /** Asks the person a yes or no question; `window.confirm` by default. */
  confirm?: (question: string) => boolean;
}) {
  const { state, client } = props;
  const [screen, setScreen] = useState(props.screen ?? 'pipelines');
  const [ownConnections] = useState(() =>
    props.connections === undefined
      ? createConnectionsController(client)
      : null,
  );
  const connectionsController = props.connections ?? ownConnections;
  if (connectionsController === null) {
    throw new Error('the shell has no connections controller');
  }
  const connectionsState = useSyncExternalStore(
    connectionsController.subscribe,
    connectionsController.current,
    connectionsController.current,
  );
  const [ownNames] = useState(() =>
    props.connectionNames === undefined
      ? createConnectionNamesController(client)
      : null,
  );
  const namesController = props.connectionNames ?? ownNames;
  if (namesController === null) {
    throw new Error('the shell has no connection names controller');
  }
  const namesState = useSyncExternalStore(
    namesController.subscribe,
    namesController.current,
    namesController.current,
  );
  const [ownController] = useState(() =>
    props.pipelines === undefined ? createPipelinesController(client) : null,
  );
  const pipelines = props.pipelines ?? ownController;
  if (pipelines === null) {
    throw new Error('the shell has no pipelines controller');
  }
  const listed = useSyncExternalStore(
    pipelines.subscribe,
    pipelines.current,
    pipelines.current,
  );
  const connectedNow = state.status === 'connected';
  useEffect(() => {
    pipelines.setOnline(connectedNow);
  }, [pipelines, connectedNow]);
  useEffect(() => {
    connectionsController.setOnline(connectedNow);
  }, [connectionsController, connectedNow]);
  useEffect(() => {
    namesController.setOnline(connectedNow);
  }, [namesController, connectedNow]);
  const opened = props.pipeline ?? listed.opened?.pipeline ?? null;
  // The files of the opened pipeline's custom-code blocks: one controller
  // per opened entry, so another entry starts with none read.
  const openedEntry = listed.opened;
  const sources = useMemo(
    () =>
      props.sources ??
      (openedEntry === null
        ? null
        : createSourcesController(client, openedEntry.location)),
    [props.sources, openedEntry, client],
  );
  useEffect(() => {
    sources?.setOnline(connectedNow);
  }, [sources, connectedNow]);
  const unsavedCode = useSyncExternalStore(
    sources?.subscribe ?? noSubscription,
    () => sources?.current().unsaved ?? noPaths,
    () => sources?.current().unsaved ?? noPaths,
  );
  const confirmQuestion = props.confirm;
  const ask = useMemo(
    () => confirmQuestion ?? ((question: string) => window.confirm(question)),
    [confirmQuestion],
  );
  // The pipeline as edited, the selected node, why the last edit was refused
  // and the Kafka forms' unsaved input, owned here so the side panel can read
  // them. They belong to the pipeline they were made in, so another pipeline
  // starts as it was handed in, unselected.
  const [edits, setEdits] = useState<{
    opened: Pipeline;
    pipeline: Pipeline;
    node: string | null;
    notice: string | null;
    drafts: Drafts;
  } | null>(null);
  const current =
    edits !== null && edits.opened === opened
      ? edits
      : opened === null
        ? null
        : { opened, pipeline: opened, node: null, notice: null, drafts: {} };
  const pipeline = current?.pipeline ?? null;
  const selected = current?.node ?? null;
  useEffect(() => {
    if (sources !== null && pipeline !== null) {
      sources.load(sourcePaths(pipeline));
    }
  }, [sources, pipeline]);
  const onSelect = (node: string | null) => {
    if (current !== null) setEdits({ ...current, node });
  };
  const onEdit = (result: EditResult) => {
    if (current === null) return;
    if (!result.ok) {
      setEdits({ ...current, notice: result.reason });
      return;
    }
    // A file no step names any more is dropped with its buffer, after
    // asking when it holds unsaved code; nothing is deleted on disk.
    if (sources !== null) {
      const dropped = droppedPaths(current.pipeline, result.pipeline);
      const lost = unsavedDroppedBy(
        current.pipeline,
        result.pipeline,
        unsavedCode,
      );
      if (lost.length > 0 && !ask(discardQuestion(lost))) return;
      if (dropped.length > 0) sources.forget(dropped);
    }
    // A new step is selected; a deleted one no longer can be.
    const node =
      result.stepId !== undefined
        ? `step:${result.stepId}`
        : blockForNode(result.pipeline, current.node) === null
          ? null
          : current.node;
    setEdits({
      ...current,
      pipeline: result.pipeline,
      node,
      notice: null,
      drafts: keptDrafts(result.pipeline, current.drafts),
    });
  };
  const onDraft = (role: KafkaRole, draft: KafkaDraft | null) => {
    if (current === null) return;
    const drafts =
      draft === null
        ? withoutDraft(current.drafts, role)
        : { ...current.drafts, [role]: draft };
    setEdits({ ...current, drafts });
  };
  const onSave = (draft: KafkaDraft) => {
    if (current === null) return;
    // The same validated path as every graph edit. Refused, what was typed
    // stays as it was, with the reason; saved, the form matches the pipeline.
    const result = saveDraft(current.pipeline, draft);
    if (!result.ok) {
      setEdits({ ...current, notice: result.reason });
      return;
    }
    setEdits({
      ...current,
      pipeline: result.pipeline,
      notice: null,
      drafts: withoutDraft(current.drafts, draft.role),
    });
  };
  const onStepDraft = (id: string, draft: StepDraft | null) => {
    if (current === null) return;
    const drafts =
      draft === null
        ? withoutDraft(current.drafts, stepNode(id))
        : { ...current.drafts, [stepNode(id)]: draft };
    setEdits({ ...current, drafts });
  };
  const onStepSave = (draft: StepDraft) => {
    if (current === null) return;
    const result = saveStepDraft(current.pipeline, draft);
    if (!result.ok) {
      setEdits({ ...current, notice: result.reason });
      return;
    }
    setEdits({
      ...current,
      pipeline: result.pipeline,
      notice: null,
      drafts: withoutDraft(current.drafts, stepNode(draft.id)),
    });
  };
  // The Kafka block the selection is, if it is one.
  const kafkaRole: KafkaRole | null =
    selected === 'source' || selected === 'destination' ? selected : null;
  // Whenever a Kafka form is shown, and whenever the connection screen has
  // read its list again (after each change it makes), the select offers the
  // names as they are now.
  const kafkaFormShown = kafkaRole !== null && current !== null;
  const connectionList = connectionsState.list;
  useEffect(() => {
    if (kafkaFormShown) namesController.refresh();
  }, [namesController, kafkaFormShown, kafkaRole, screen, connectionList]);
  const kafkaDraft = (role: KafkaRole): KafkaDraft | null => {
    const draft = current?.drafts[role];
    return draft !== undefined && 'role' in draft ? draft : null;
  };
  // The filter, transform or validate step the selection is, if it is one.
  const placed = pipeline === null ? null : blockForNode(pipeline, selected);
  const stepId =
    placed?.role === 'step' &&
    pipeline !== null &&
    stepFor(pipeline, placed.block.id) !== null
      ? placed.block.id
      : null;
  const stepDraft = (id: string): StepDraft | null => {
    const draft = current?.drafts[stepNode(id)];
    return draft !== undefined && 'type' in draft ? draft : null;
  };
  // Every other block with unsaved input, in pipeline order.
  const unsavedElsewhere =
    pipeline === null
      ? []
      : placedBlocks(pipeline)
          .map(nodeId)
          .filter(
            (node) => node !== selected && current?.drafts[node] !== undefined,
          );
  const description = describeConnection(state);
  const connected = state.status === 'connected';
  const language = pipeline?.language ?? 'python';
  const offline = connected ? null : description.label;
  const code = useMemo((): CodeSources | undefined => {
    if (sources === null) return undefined;
    return {
      subscribe: sources.subscribe,
      buffer: (path) => sources.current().buffers[path],
      language,
      offline,
      onChange: (path, text) => {
        sources.edit(path, text);
      },
      onSave: (path) => {
        sources.save(path);
      },
      onReload: (path) => {
        const unsaved = sources.current().unsaved.includes(path);
        if (unsaved && !ask(discardQuestion([path]))) return;
        sources.reload(path);
      },
    };
  }, [sources, language, offline, ask]);
  // The custom-code blocks whose file holds unsaved code, as the side panel
  // lists them beside the forms.
  const unsavedCodeNodes =
    pipeline === null
      ? []
      : stepsNaming(pipeline, unsavedCode).filter((node) => node !== selected);
  const graph =
    pipeline === null ? null : (
      <PipelineGraph
        key={
          listed.opened === null
            ? pipeline.id
            : `${listed.opened.location.root}\u0000${listed.opened.location.directory ?? ''}`
        }
        pipeline={pipeline}
        selected={selected}
        onSelect={onSelect}
        onEdit={onEdit}
        {...(code === undefined ? {} : { code })}
      />
    );
  const notice = (
    <section
      className="connection-notice"
      data-tone={description.tone}
      data-state={state.status}
      role="alert"
    >
      <h1>{description.label}</h1>
      <p>{description.detail}</p>
    </section>
  );
  return (
    <div className="shell">
      <header className="status-bar">
        <span className="product">kpv</span>
        <span
          className="connection"
          data-tone={description.tone}
          role="status"
          aria-live="polite"
        >
          {description.label}
        </span>
        <button
          type="button"
          className="to-connections"
          aria-pressed={screen === 'connections'}
          onClick={() => {
            setScreen(screen === 'connections' ? 'pipelines' : 'connections');
            if (screen !== 'connections') connectionsController.refresh();
          }}
        >
          Kafka connections
        </button>
      </header>
      <main className="graph-area" aria-label="Graph area">
        {connected && screen === 'connections' ? (
          <ConnectionsView
            state={connectionsState}
            actions={connectionsController}
            onClose={() => {
              setScreen('pipelines');
            }}
          />
        ) : connected && pipeline !== null ? (
          <>
            {listed.opened === null ? null : (
              <button
                type="button"
                className="back-to-pipelines"
                onClick={() => {
                  if (
                    unsavedCode.length > 0 &&
                    !ask(discardQuestion(unsavedCode))
                  ) {
                    return;
                  }
                  pipelines.close();
                }}
              >
                Back to the pipelines
              </button>
            )}
            {graph}
          </>
        ) : connected && listed.openFailure !== null ? (
          <OpenFailureView state={listed} actions={pipelines} />
        ) : connected ? (
          <PipelinesView state={listed} actions={pipelines} />
        ) : graph !== null && listed.opened !== null ? (
          <>
            {notice}
            {graph}
          </>
        ) : (
          notice
        )}
      </main>
      <aside className="side-panel" aria-label="Side panel">
        {pipeline === null ? null : (
          <>
            <SelectionSummary pipeline={pipeline} selected={selected} />
            {current === null || current.notice === null ? null : (
              <p className="edit-notice" role="status">
                {current.notice}
              </p>
            )}
            {connected ? (
              <EditControls
                pipeline={pipeline}
                selected={selected}
                onEdit={onEdit}
              />
            ) : null}
            {connected && kafkaRole !== null && current !== null ? (
              <KafkaBlockForm
                key={kafkaRole}
                pipeline={pipeline}
                role={kafkaRole}
                draft={kafkaDraft(kafkaRole)}
                onDraft={onDraft}
                onSave={onSave}
                connections={namesState}
                onReloadConnections={() => {
                  namesController.refresh();
                }}
              />
            ) : null}
            {connected && stepId !== null && current !== null ? (
              <StepBlockForm
                key={stepNode(stepId)}
                pipeline={pipeline}
                stepId={stepId}
                draft={stepDraft(stepId)}
                onDraft={onStepDraft}
                onSave={onStepSave}
              />
            ) : null}
            <UnsavedElsewhere
              pipeline={pipeline}
              nodes={unsavedElsewhere}
              codeNodes={unsavedCodeNodes}
              onShow={onSelect}
            />
          </>
        )}
        <h2 id={pickerAnchor}>Project directory</h2>
        <DirectoryPicker client={client} connection={state} />
      </aside>
      <footer className="bottom-panel" aria-label="Bottom panel">
        <span>Agent: {description.label}</span>
        {description.detail === '' ? null : (
          <span className="muted"> {description.detail}</span>
        )}
      </footer>
    </div>
  );
}
