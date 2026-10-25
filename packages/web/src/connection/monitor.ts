// The agent connection state, which the page always shows.
//
// The push channel is the liveness signal: while its WebSocket is open the
// agent is there and has accepted us. When it is not open -- it failed to
// open, or it dropped -- a WebSocket cannot say why: a refused handshake and
// an agent that is not running both end in the same close event. So a drop is
// followed by one plain request through the client, and that request's
// outcome is the state, because a request does say why (see api/client.ts):
//
//   - no answer at all: `unreachable`, and reconnecting with backoff.
//   - the uniform rejection: `unauthenticated`, and no reconnecting. The token
//     this page holds is not the agent's, and waiting will not change that;
//     re-opening the agent's startup URL will.
//   - any other answer: `failed`, the agent is there but not well, and
//     reconnecting with backoff.
//   - a healthy answer: the channel alone failed, so reconnecting with backoff.
//
// A page with no token at all -- the reload after the token was stripped from
// the URL -- is `unauthenticated` from the start and makes no request.

import type { AgentClient } from '../api/client.ts';
import { createBackoff, type Backoff } from './backoff.ts';

export type ConnectionState =
  /** Opening the channel; `retryInMs` is set while waiting to try again. */
  | { status: 'connecting'; retryInMs?: number }
  | { status: 'connected' }
  /** The agent is refusing this page, or the page has no token to offer. */
  | { status: 'unauthenticated'; reason: 'missing-token' | 'rejected' }
  /** Nothing answered: the agent is not running, or not where it was. */
  | { status: 'unreachable'; retryInMs: number }
  /** The agent answered, but not with anything this page understands. */
  | { status: 'failed'; httpStatus: number; retryInMs: number };

/** Timers, so a test can run the backoff without waiting for it. */
export interface Timers {
  setTimeout(callback: () => void, ms: number): unknown;
  clearTimeout(handle: unknown): void;
}

// `current` and `subscribe` are properties rather than methods because React's
// useSyncExternalStore is handed them detached from the monitor.
export interface ConnectionMonitor {
  /** The current state. */
  current: () => ConnectionState;
  /** Calls `listener` on every change; returns the unsubscribe. */
  subscribe: (listener: () => void) => () => void;
  /** Starts connecting. Calling it again does nothing. */
  start(): void;
  /** Closes the channel and stops reconnecting. */
  stop(): void;
}

const browserTimers: Timers = {
  setTimeout: (callback, ms) => globalThis.setTimeout(callback, ms),
  clearTimeout: (handle) =>
    globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>),
};

/**
 * Creates the monitor. `client` is `null` when the page has no token, which is
 * the one case that is decided without asking the agent.
 */
export function createConnectionMonitor(
  client: AgentClient | null,
  options: { timers?: Timers; backoff?: Backoff } = {},
): ConnectionMonitor {
  const timers = options.timers ?? browserTimers;
  const backoff = options.backoff ?? createBackoff();
  const listeners = new Set<() => void>();
  let state: ConnectionState =
    client === null
      ? { status: 'unauthenticated', reason: 'missing-token' }
      : { status: 'connecting' };
  let started = false;
  let stopped = false;
  let socket: WebSocket | null = null;
  let timer: unknown = null;

  function set(next: ConnectionState) {
    state = next;
    for (const listener of listeners) {
      listener();
    }
  }

  function connect(agent: AgentClient) {
    timer = null;
    if (stopped) {
      return;
    }
    if (state.status !== 'connecting' || state.retryInMs !== undefined) {
      set({ status: 'connecting' });
    }
    const ws = agent.openChannel();
    socket = ws;
    ws.addEventListener('open', () => {
      if (socket !== ws || stopped) {
        return;
      }
      backoff.reset();
      set({ status: 'connected' });
    });
    // An error is always followed by a close, so the close is the one place
    // a drop is handled.
    ws.addEventListener('close', () => {
      if (socket !== ws || stopped) {
        return;
      }
      socket = null;
      void diagnose(agent);
    });
  }

  async function diagnose(agent: AgentClient) {
    const result = await agent.probe();
    if (stopped) {
      return;
    }
    if (result.kind === 'rejected') {
      set({ status: 'unauthenticated', reason: 'rejected' });
      return;
    }
    const retryInMs = backoff.next();
    switch (result.kind) {
      case 'unreachable':
        set({ status: 'unreachable', retryInMs });
        break;
      case 'failed':
      case 'error':
        set({ status: 'failed', httpStatus: result.status, retryInMs });
        break;
      case 'ok':
        set({ status: 'connecting', retryInMs });
        break;
    }
    timer = timers.setTimeout(() => connect(agent), retryInMs);
  }

  return {
    current: () => state,
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    start() {
      if (started || client === null) {
        return;
      }
      started = true;
      connect(client);
    },
    stop() {
      stopped = true;
      if (timer !== null) {
        timers.clearTimeout(timer);
        timer = null;
      }
      socket?.close();
      socket = null;
    },
  };
}
