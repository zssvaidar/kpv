// The layout shell: a status bar, then the three regions every later screen
// fills in -- the graph area (#9), the side panel (#12, #13) and the bottom
// panel. Layout only; nothing in it knows about a pipeline yet.
//
// The connection state is in the status bar on every screen, and when there
// is no working connection the graph area says why in words, so a dead or
// refusing agent never looks like an empty screen.

import { useSyncExternalStore } from 'react';

import type { AgentClient } from '../api/client.ts';
import { describeConnection } from '../connection/describe.ts';
import type {
  ConnectionMonitor,
  ConnectionState,
} from '../connection/monitor.ts';
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
}) {
  const { state, client } = props;
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
        {connected ? (
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
