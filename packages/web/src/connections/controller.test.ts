// The connection screen's controller against a stand-in agent: the real
// typed client with an injected fetch (picker/fake-agent.ts).

import { describe, expect, it } from 'vitest';

import type { ConnectionSummary } from '@kpv/schema';

import { fakeAgent, json, rejection } from '../picker/fake-agent.ts';
import { createConnectionsController } from './controller.ts';

const summary: ConnectionSummary = {
  name: 'eu',
  bootstrapServers: ['broker:9093'],
  securityProtocol: 'SASL_PLAINTEXT',
  saslMechanism: 'PLAIN',
  saslUsername: 'svc',
  saslPasswordSet: true,
  tlsClientKeySet: false,
};

async function online() {
  const agent = fakeAgent();
  const controller = createConnectionsController(agent.client);
  controller.setOnline(true);
  agent
    .next('GET', '/api/connections')
    .answer(json(200, { connections: [summary] }));
  await agent.settle();
  return { agent, controller };
}

const referenced = {
  error: 'referenced',
  message: 'pipelines name this connection',
  references: [
    {
      root: '/home/u/code',
      directory: 'orders',
      pipelineId: 'orders',
      block: 'source',
    },
  ],
  unchecked: [{ root: '/home/u/code', directory: 'broken', reason: 'parse' }],
};

describe('the connections controller', () => {
  it('lists the connections when it goes online, and nothing before', async () => {
    const agent = fakeAgent();
    const controller = createConnectionsController(agent.client);
    expect(agent.sent).toHaveLength(0);
    controller.setOnline(true);
    agent
      .next('GET', '/api/connections')
      .answer(json(200, { connections: [summary] }));
    await agent.settle();
    expect(controller.current().list).toEqual([summary]);
  });

  it('reads a rejection as a connection problem', async () => {
    const agent = fakeAgent();
    const controller = createConnectionsController(agent.client);
    controller.setOnline(true);
    agent.next('GET', '/api/connections').answer(rejection());
    await agent.settle();
    expect(controller.current().problem).toEqual({ kind: 'rejected' });
  });

  it('creates from a valid form, and clears the typed password the moment it is sent', async () => {
    const { agent, controller } = await online();
    controller.startCreate();
    controller.edit('name', 'local');
    controller.edit('bootstrapServers', 'localhost:9092');
    controller.edit('securityProtocol', 'SASL_PLAINTEXT');
    controller.edit('saslMechanism', 'PLAIN');
    controller.edit('saslUsername', 'u');
    controller.edit('saslPassword', 'typed-secret');
    controller.save();
    // Sent, and already gone from the state.
    expect(controller.current().draft?.saslPassword).toBe('');
    expect(JSON.stringify(controller.current())).not.toContain('typed-secret');
    const request = agent.next('POST', '/api/connections');
    expect(request.body).toMatchObject({
      name: 'local',
      saslPassword: 'typed-secret',
    });
    request.answer(json(200, { ...summary, name: 'local' }));
    await agent.settle();
    expect(controller.current().draft).toBeNull();
    agent.next('GET', '/api/connections');
  });

  it('sends nothing for an invalid form', async () => {
    const { agent, controller } = await online();
    controller.startCreate();
    controller.edit('name', 'my cluster');
    controller.save();
    expect(agent.pending()).toHaveLength(0);
    expect(controller.current().draft?.name).toBe('my cluster');
  });

  it('shows a refusal on the field it names and keeps what was typed but the credentials', async () => {
    const { agent, controller } = await online();
    controller.startCreate();
    controller.edit('name', 'local');
    controller.edit('bootstrapServers', 'localhost:9092');
    controller.edit('securityProtocol', 'SSL');
    controller.edit('tlsClientCertificate', 'not really a certificate');
    controller.edit('tlsClientKey', 'typed key');
    controller.save();
    agent.next('POST', '/api/connections').answer(
      json(400, {
        error: 'invalid',
        message: 'm',
        problems: [
          {
            pointer: '/tlsClientCertificate',
            message: 'this is not PEM text holding one or more certificates',
          },
        ],
      }),
    );
    await agent.settle();
    const state = controller.current();
    expect(state.refusal.tlsClientCertificate).toContain('PEM');
    expect(state.draft?.tlsClientCertificate).toBe('not really a certificate');
    expect(state.draft?.name).toBe('local');
    expect(state.draft?.tlsClientKey).toBe('');
    // Editing the field clears the refusal on it.
    controller.edit('tlsClientCertificate', 'another');
    expect(controller.current().refusal.tlsClientCertificate).toBeUndefined();
  });

  it('shows a duplicate name on the name', async () => {
    const { agent, controller } = await online();
    controller.startCreate();
    controller.edit('name', 'EU');
    controller.edit('bootstrapServers', 'b:1');
    controller.edit('securityProtocol', 'PLAINTEXT');
    controller.save();
    agent
      .next('POST', '/api/connections')
      .answer(json(409, { error: 'duplicateName', message: 'taken' }));
    await agent.settle();
    expect(controller.current().refusal.name).toBe('taken');
  });

  it('edits without the stored password and keeps it', async () => {
    const { agent, controller } = await online();
    controller.startEdit('eu');
    expect(controller.current().draft?.saslPassword).toBe('');
    controller.edit('saslUsername', 'other');
    controller.save();
    const request = agent.next('PUT', '/api/connections');
    expect(request.body).toEqual({
      current: 'eu',
      name: 'eu',
      bootstrapServers: ['broker:9093'],
      securityProtocol: 'SASL_PLAINTEXT',
      saslMechanism: 'PLAIN',
      saslUsername: 'other',
    });
  });

  it('shows the report of a referenced rename and sends the confirmation only when asked', async () => {
    const { agent, controller } = await online();
    controller.startEdit('eu');
    controller.edit('name', 'eu-west');
    controller.save();
    agent.next('PUT', '/api/connections').answer(json(409, referenced));
    await agent.settle();
    const report = controller.current().report;
    expect(report?.action).toBe('rename');
    expect(report?.error.references).toHaveLength(1);
    expect(agent.pending()).toHaveLength(0);
    controller.confirm();
    const confirmed = agent.next('PUT', '/api/connections');
    expect(confirmed.body).toMatchObject({
      current: 'eu',
      name: 'eu-west',
      confirm: true,
    });
  });

  it('deletes with no report when nothing names the connection', async () => {
    const { agent, controller } = await online();
    controller.remove('eu');
    const request = agent.next('DELETE', '/api/connections');
    expect(request.body).toEqual({ name: 'eu' });
    request.answer(json(200, { connections: [] }));
    await agent.settle();
    expect(controller.current().list).toEqual([]);
    expect(controller.current().report).toBeNull();
  });

  it('asks before deleting a referenced connection, and can be left alone', async () => {
    const { agent, controller } = await online();
    controller.remove('eu');
    agent.next('DELETE', '/api/connections').answer(json(409, referenced));
    await agent.settle();
    expect(controller.current().report?.action).toBe('delete');
    controller.cancelReport();
    expect(controller.current().report).toBeNull();
    expect(agent.pending()).toHaveLength(0);
    controller.remove('eu');
    agent.next('DELETE', '/api/connections').answer(json(409, referenced));
    await agent.settle();
    controller.confirm();
    expect(agent.next('DELETE', '/api/connections').body).toEqual({
      name: 'eu',
      confirm: true,
    });
  });
});
