// The development topology in one function: the agent, and the Vite dev
// server in front of it forwarding `/api`. `pnpm dev` is this plus a terminal;
// the integration test is this twice.

import type { AddressInfo } from 'node:net';
import { fileURLToPath } from 'node:url';

import { createServer, type ViteDevServer } from 'vite';

import { agentProxy } from './agent-proxy.ts';
import { startAgent, type RunningAgent } from './agent-process.ts';

/** The web package, which is the Vite root. */
export const webRoot = fileURLToPath(new URL('../', import.meta.url));

/** A running development setup. */
export interface DevSetup {
  agent: RunningAgent;
  vite: ViteDevServer;
  /** The dev server's origin, for example `http://127.0.0.1:5173`. */
  origin: string;
  /**
   * The URL to open: the dev server's, carrying this run's token. The page
   * takes the token out of it and out of the session history on load.
   */
  openUrl: string;
  /** Stops the dev server and then the agent. */
  stop(): Promise<void>;
}

export interface DevOptions {
  /** The dev server's port. 5173 by default, the next free one if taken. */
  port?: number;
  /** Where the agent's log goes. */
  agentLog?: (chunk: Buffer) => void;
}

/** Starts the agent, then the dev server pointed at wherever it bound. */
export async function startDev(options: DevOptions = {}): Promise<DevSetup> {
  const agent = await startAgent(options.agentLog);
  let vite: ViteDevServer;
  try {
    vite = await createServer({
      root: webRoot,
      configFile: fileURLToPath(new URL('../vite.config.ts', import.meta.url)),
      plugins: [agentProxy(agent.origin)],
      server: {
        host: '127.0.0.1',
        ...(options.port === undefined ? {} : { port: options.port }),
      },
    });
    await vite.listen();
  } catch (err) {
    await agent.stop();
    throw err;
  }
  const address = vite.httpServer?.address() as AddressInfo;
  const origin = `http://127.0.0.1:${address.port}`;
  const openUrl = `${origin}/?token=${encodeURIComponent(agent.token)}`;
  return {
    agent,
    vite,
    origin,
    openUrl,
    stop: async () => {
      await vite.close();
      await agent.stop();
    },
  };
}
