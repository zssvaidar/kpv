import { describe, expect, it } from 'vitest';

import type { AgentClient, AgentResult } from '../api/client.ts';
import { createBackoff } from './backoff.ts';
import {
  createConnectionMonitor,
  type ConnectionState,
  type Timers,
} from './monitor.ts';

// A WebSocket that does what the test tells it to.
class FakeSocket extends EventTarget {
  closed = false;
  open() {
    this.dispatchEvent(new Event('open'));
  }
  drop() {
    this.dispatchEvent(new Event('error'));
    this.dispatchEvent(new Event('close'));
  }
  close() {
    this.closed = true;
  }
}

// Timers that run only when the test says, and remember what was asked.
function manualTimers() {
  const pending: { callback: () => void; ms: number; id: number }[] = [];
  let next = 0;
  const timers: Timers = {
    setTimeout(callback, ms) {
      next += 1;
      pending.push({ callback, ms, id: next });
      return next;
    },
    clearTimeout(handle) {
      const index = pending.findIndex((p) => p.id === handle);
      if (index >= 0) pending.splice(index, 1);
    },
  };
  return {
    timers,
    pending,
    fire() {
      const timer = pending.shift();
      if (timer === undefined) throw new Error('no timer pending');
      timer.callback();
      return timer.ms;
    },
  };
}

function setup(probes: AgentResult<null>[]) {
  const sockets: FakeSocket[] = [];
  let probed = 0;
  const client: AgentClient = {
    probe: () => {
      probed += 1;
      return Promise.resolve(probes.shift() ?? { kind: 'unreachable' });
    },
    listDirectories: () => Promise.reject(new Error('not used')),
    openChannel: () => {
      const socket = new FakeSocket();
      sockets.push(socket);
      return socket as unknown as WebSocket;
    },
  };
  const clock = manualTimers();
  const monitor = createConnectionMonitor(client, {
    timers: clock.timers,
    backoff: createBackoff({
      initialMs: 100,
      maxMs: 1000,
      random: () => 0.999999,
    }),
  });
  const seen: ConnectionState[] = [];
  monitor.subscribe(() => seen.push(monitor.current()));
  return { monitor, sockets, clock, seen, probes: () => probed };
}

const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('createConnectionMonitor', () => {
  it('is connected once the channel opens', () => {
    const { monitor, sockets } = setup([]);
    expect(monitor.current()).toEqual({ status: 'connecting' });
    monitor.start();
    sockets[0]?.open();
    expect(monitor.current()).toEqual({ status: 'connected' });
  });

  it('reads a drop with nothing answering as unreachable, and reconnects', async () => {
    const { monitor, sockets, clock } = setup([{ kind: 'unreachable' }]);
    monitor.start();
    sockets[0]?.open();
    sockets[0]?.drop();
    await settle();
    expect(monitor.current()).toEqual({
      status: 'unreachable',
      retryInMs: 100,
    });
    expect(clock.fire()).toBe(100);
    expect(sockets).toHaveLength(2);
    sockets[1]?.open();
    expect(monitor.current()).toEqual({ status: 'connected' });
  });

  it('backs off on repeated failures, up to the bound and no further', async () => {
    const { monitor, sockets, clock } = setup([]);
    monitor.start();
    const delays: number[] = [];
    for (let attempt = 0; attempt < 12; attempt += 1) {
      sockets.at(-1)?.drop();
      await settle();
      delays.push(clock.fire());
    }
    expect(delays).toEqual([
      100, 200, 400, 800, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000,
    ]);
    expect(Math.max(...delays)).toBeLessThanOrEqual(1000);
  });

  it('starts the backoff over once a connection succeeds', async () => {
    const { monitor, sockets, clock } = setup([]);
    monitor.start();
    sockets[0]?.drop();
    await settle();
    clock.fire();
    sockets[1]?.drop();
    await settle();
    expect(clock.fire()).toBe(200);
    sockets[2]?.open();
    sockets[2]?.drop();
    await settle();
    expect(clock.fire()).toBe(100);
  });

  it('reads a drop the agent answers with the rejection as unauthenticated, and stops', async () => {
    const { monitor, sockets, clock } = setup([{ kind: 'rejected' }]);
    monitor.start();
    sockets[0]?.drop();
    await settle();
    expect(monitor.current()).toEqual({
      status: 'unauthenticated',
      reason: 'rejected',
    });
    // Waiting will not make a stale token valid, so nothing is scheduled.
    expect(clock.pending).toHaveLength(0);
  });

  it('tells a refusing agent from an absent one', async () => {
    const refused = setup([{ kind: 'rejected' }]);
    const absent = setup([{ kind: 'unreachable' }]);
    for (const s of [refused, absent]) {
      s.monitor.start();
      s.sockets[0]?.drop();
    }
    await settle();
    expect(refused.monitor.current().status).toBe('unauthenticated');
    expect(absent.monitor.current().status).toBe('unreachable');
  });

  it('reads another answer as failed, and keeps trying', async () => {
    const { monitor, sockets, clock } = setup([
      { kind: 'failed', status: 500 },
    ]);
    monitor.start();
    sockets[0]?.drop();
    await settle();
    expect(monitor.current()).toEqual({
      status: 'failed',
      httpStatus: 500,
      retryInMs: 100,
    });
    expect(clock.pending).toHaveLength(1);
  });

  it('reconnects when only the channel dropped and the agent is fine', async () => {
    const { monitor, sockets, clock } = setup([{ kind: 'ok', value: null }]);
    monitor.start();
    sockets[0]?.open();
    sockets[0]?.drop();
    await settle();
    expect(monitor.current()).toEqual({ status: 'connecting', retryInMs: 100 });
    clock.fire();
    expect(monitor.current()).toEqual({ status: 'connecting' });
  });

  it('with no token is unauthenticated from the start and asks the agent nothing', () => {
    const monitor = createConnectionMonitor(null);
    monitor.start();
    expect(monitor.current()).toEqual({
      status: 'unauthenticated',
      reason: 'missing-token',
    });
  });

  it('stops: closes the channel and cancels the pending reconnect', async () => {
    const { monitor, sockets, clock, probes } = setup([]);
    monitor.start();
    sockets[0]?.drop();
    await settle();
    expect(clock.pending).toHaveLength(1);
    monitor.stop();
    expect(clock.pending).toHaveLength(0);

    const second = setup([]);
    second.monitor.start();
    second.monitor.stop();
    expect(second.sockets[0]?.closed).toBe(true);
    second.sockets[0]?.drop();
    await settle();
    expect(second.probes()).toBe(0);
    expect(probes()).toBe(1);
  });
});
