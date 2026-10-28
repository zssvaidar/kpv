// The layout shell: a status bar, then the three regions every later screen
// fills in -- the graph area (#9), the side panel (#12, #13) and the bottom
// panel. When it is handed a pipeline the graph area draws it (#9) and the
// side panel shows which block is selected in it, with the controls to edit
// the chain (#11); nothing opens one yet. Edits are kept here, in memory, as
// the pipeline the graph draws; saving them is #3.
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
import { blockForNode } from '../graph/layout.ts';
import { PipelineGraph } from '../graph/PipelineGraph.tsx';
import { SelectionSummary } from '../graph/SelectionSummary.tsx';
import { DirectoryPicker } from '../picker/DirectoryPicker.tsx';

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
  // The pipeline as edited, the selected node and why the last edit was
  // refused, owned here so the side panel can read them. They belong to the
  // pipeline they were made in, so another pipeline starts as it was handed
  // in, unselected.
  const [edits, setEdits] = useState<{
    opened: Pipeline;
    pipeline: Pipeline;
    node: string | null;
    notice: string | null;
  } | null>(null);
  const current =
    edits !== null && edits.opened === opened
      ? edits
      : opened === null
        ? null
        : { opened, pipeline: opened, node: null, notice: null };
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
    setEdits({ ...current, pipeline: result.pipeline, node, notice: null });
  };
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
