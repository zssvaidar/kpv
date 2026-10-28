// The layout shell: a status bar, then the three regions every later screen
// fills in -- the graph area (#9), the side panel (#12, #13) and the bottom
// panel. When it is handed a pipeline the graph area draws it (#9) and the
// side panel shows which block is selected in it, with the controls to edit
// the chain (#11), and the form that configures it: for the Kafka source or
// destination (#12), and for a filter, transform or validate step (#13);
// nothing opens one yet. Edits are kept here, in memory, as the pipeline the
// graph draws; saving them is #3. So is what was typed into a form and not
// yet saved into that pipeline, per block, so selecting another block keeps
// it.
//
// The connection state is in the status bar on every screen, and when there
// is no working connection the graph area says why in words, so a dead or
// refusing agent never looks like an empty screen.

import { useState, useSyncExternalStore } from 'react';

import type { Pipeline } from '@kpv/schema';

import type { AgentClient } from '../api/client.ts';
import { describeConnection } from '../connection/describe.ts';
import type {
  ConnectionMonitor,
  ConnectionState,
} from '../connection/monitor.ts';
import type { EditResult } from '../graph/edit.ts';
import { EditControls } from '../graph/EditControls.tsx';
import { blockForNode, nodeId, placedBlocks } from '../graph/layout.ts';
import { PipelineGraph } from '../graph/PipelineGraph.tsx';
import { SelectionSummary } from '../graph/SelectionSummary.tsx';
import type { KafkaDraft, KafkaRole } from '../panel/kafka-form.ts';
import { saveDraft } from '../panel/kafka-form.ts';
import { KafkaBlockForm, UnsavedElsewhere } from '../panel/KafkaBlockForm.tsx';
import type { StepDraft } from '../panel/step-form.ts';
import { saveStepDraft, stepFor } from '../panel/step-form.ts';
import { StepBlockForm } from '../panel/StepBlockForm.tsx';
import { DirectoryPicker } from '../picker/DirectoryPicker.tsx';

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

/** The shell for one connection state, so it can be rendered without an agent. */
export function Shell(props: {
  state: ConnectionState;
  client: AgentClient | null;
  pipeline?: Pipeline | null;
}) {
  const { state, client } = props;
  const opened = props.pipeline ?? null;
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
  const onSelect = (node: string | null) => {
    if (current !== null) setEdits({ ...current, node });
  };
  const onEdit = (result: EditResult) => {
    if (current === null) return;
    if (!result.ok) {
      setEdits({ ...current, notice: result.reason });
      return;
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
      </header>
      <main className="graph-area" aria-label="Graph area">
        {connected && pipeline !== null ? (
          <PipelineGraph
            key={pipeline.id}
            pipeline={pipeline}
            selected={selected}
            onSelect={onSelect}
            onEdit={onEdit}
          />
        ) : connected ? (
          <p className="muted">No pipeline open.</p>
        ) : (
          <section
            className="connection-notice"
            data-tone={description.tone}
            data-state={state.status}
            role="alert"
          >
            <h1>{description.label}</h1>
            <p>{description.detail}</p>
          </section>
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
              onShow={onSelect}
            />
          </>
        )}
        <h2>Project directory</h2>
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
