// Bootstrap. The order matters: the token is taken out of the URL and out of
// the session history before anything renders or connects, and from then on
// the only copy of it is the one inside the client.

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import { browserEnvironment, createAgentClient } from './api/client.ts';
import { createConnectionMonitor } from './connection/monitor.ts';
import { takeToken } from './session/token.ts';
import { App } from './shell/App.tsx';
import './shell/shell.css';

const token = takeToken(window);
const client =
  token === null ? null : createAgentClient(token, browserEnvironment());
const monitor = createConnectionMonitor(client);
monitor.start();

const root = document.getElementById('root');
if (root === null) {
  throw new Error('index.html has no #root element');
}
createRoot(root).render(
  <StrictMode>
    <App client={client} monitor={monitor} />
  </StrictMode>,
);
