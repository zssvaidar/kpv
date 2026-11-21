// The connection names the Kafka form's select is filled from (#12), read
// from a stand-in agent through the real typed client
// (picker/fake-agent.ts).

import { describe, expect, it } from 'vitest';

import { fakeAgent, json, rejection } from '../picker/fake-agent.ts';
import { createConnectionNamesController } from './connection-names.ts';

describe('the connection names', () => {
  it('reads nothing until it is online, then GET /api/connections/names', async () => {
    const agent = fakeAgent();
    const names = createConnectionNamesController(agent.client);
    names.refresh();
    expect(agent.sent).toHaveLength(0);
    names.setOnline(true);
    expect(names.current().loading).toBe(true);
    agent
      .next('GET', '/api/connections/names')
      .answer(json(200, { names: ['billing', 'eu-cluster'] }));
    await agent.settle();
    expect(names.current()).toEqual({
      names: ['billing', 'eu-cluster'],
      loading: false,
      problem: null,
      failure: null,
    });
  });

  it('reads an empty store as no names, not as a problem', async () => {
    const agent = fakeAgent();
    const names = createConnectionNamesController(agent.client);
    names.setOnline(true);
    agent
      .next('GET', '/api/connections/names')
      .answer(json(200, { names: [] }));
    await agent.settle();
    expect(names.current().names).toEqual([]);
    expect(names.current().problem).toBeNull();
  });

  it('reads the names again on each refresh, and tells subscribers', async () => {
    const agent = fakeAgent();
    const names = createConnectionNamesController(agent.client);
    let told = 0;
    names.subscribe(() => told++);
    names.setOnline(true);
    agent
      .next('GET', '/api/connections/names')
      .answer(json(200, { names: ['a'] }));
    await agent.settle();
    names.refresh();
    agent
      .next('GET', '/api/connections/names')
      .answer(json(200, { names: ['a', 'b'] }));
    await agent.settle();
    expect(names.current().names).toEqual(['a', 'b']);
    expect(told).toBeGreaterThanOrEqual(4);
  });

  it('keeps only the latest read when two overlap', async () => {
    const agent = fakeAgent();
    const names = createConnectionNamesController(agent.client);
    names.setOnline(true);
    names.refresh();
    const [first, second] = agent.pending();
    second?.answer(json(200, { names: ['new'] }));
    await agent.settle();
    first?.answer(json(200, { names: ['old'] }));
    await agent.settle();
    expect(names.current().names).toEqual(['new']);
  });

  it('says why a read failed, and keeps the names last read', async () => {
    const agent = fakeAgent();
    const names = createConnectionNamesController(agent.client);
    names.setOnline(true);
    agent
      .next('GET', '/api/connections/names')
      .answer(json(200, { names: ['a'] }));
    await agent.settle();

    names.refresh();
    agent.next('GET', '/api/connections/names').answer(rejection());
    await agent.settle();
    expect(names.current().problem).toEqual({ kind: 'rejected' });
    expect(names.current().names).toEqual(['a']);

    names.refresh();
    agent.next('GET', '/api/connections/names').drop();
    await agent.settle();
    expect(names.current().problem).toEqual({ kind: 'unreachable' });

    names.refresh();
    agent.next('GET', '/api/connections/names').answer(json(500, 'nope'));
    await agent.settle();
    expect(names.current().problem).toEqual({ kind: 'failed', status: 500 });

    names.refresh();
    agent
      .next('GET', '/api/connections/names')
      .answer(
        json(500, { error: 'io', message: 'The store could not be read.' }),
      );
    await agent.settle();
    expect(names.current().problem).toBeNull();
    expect(names.current().failure?.error).toBe('io');

    names.refresh();
    agent
      .next('GET', '/api/connections/names')
      .answer(json(200, { names: [] }));
    await agent.settle();
    expect(names.current()).toMatchObject({ problem: null, failure: null });
  });

  it('drops an answer that arrives after going offline', async () => {
    const agent = fakeAgent();
    const names = createConnectionNamesController(agent.client);
    names.setOnline(true);
    names.setOnline(false);
    expect(names.current().loading).toBe(false);
    agent
      .next('GET', '/api/connections/names')
      .answer(json(200, { names: ['a'] }));
    await agent.settle();
    expect(names.current().names).toBeNull();
  });

  it('sends nothing without a session', () => {
    const names = createConnectionNamesController(null);
    names.setOnline(true);
    names.refresh();
    expect(names.current().loading).toBe(false);
  });
});
