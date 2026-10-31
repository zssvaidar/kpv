// The pipeline list's controller against a stand-in agent: the real typed
// client with an injected fetch (picker/fake-agent.ts).

import { describe, expect, it } from 'vitest';

import { fakeAgent, json, rejection } from '../picker/fake-agent.ts';
import { checkDocument, createPipelinesController } from './controller.ts';
import { fixtureList, pipelinesFixtures } from './fixtures.ts';

const everyState = fixtureList('every-state');
const opened = pipelinesFixtures.documents.valid[0]?.body;
if (opened === undefined) throw new Error('no document fixture');

function setup() {
  const agent = fakeAgent();
  const controller = createPipelinesController(agent.client);
  return { agent, controller };
}

async function listed(
  agent: ReturnType<typeof fakeAgent>,
  body: unknown = everyState,
) {
  agent.next('GET', '/api/pipelines').answer(json(200, body));
  await agent.settle();
}

describe('the pipelines controller', () => {
  it('reads the list when it goes online, and nothing before', async () => {
    const { agent, controller } = setup();
    expect(agent.sent).toHaveLength(0);
    controller.setOnline(true);
    expect(controller.current().loading).toBe(true);
    await listed(agent);
    expect(controller.current().list).toEqual(everyState);
    expect(controller.current().loading).toBe(false);
  });

  it('applies only the latest read', async () => {
    const { agent, controller } = setup();
    controller.setOnline(true);
    const first = agent.next('GET', '/api/pipelines');
    controller.refresh();
    const second = agent.pending().find((r) => r !== first);
    second?.answer(json(200, everyState));
    await agent.settle();
    first.answer(json(200, { roots: [] }));
    await agent.settle();
    expect(controller.current().list).toEqual(everyState);
  });

  it('reads a rejection as a connection problem, not a list error', async () => {
    const { agent, controller } = setup();
    controller.setOnline(true);
    agent.next('GET', '/api/pipelines').answer(rejection());
    await agent.settle();
    expect(controller.current().problem).toEqual({ kind: 'rejected' });
    expect(controller.current().listError).toBeNull();
  });

  it('opens by the root and directory the list gave, and draws the document as sent', async () => {
    const { agent, controller } = setup();
    controller.setOnline(true);
    await listed(agent);
    controller.open({ root: '/home/user/code/app', directory: 'billing' });
    const request = agent.next('GET', '/api/pipelines/open');
    expect(request.query.get('root')).toBe('/home/user/code/app');
    expect(request.query.get('directory')).toBe('billing');
    request.answer(json(200, opened));
    await agent.settle();
    const state = controller.current();
    expect(state.opened?.pipeline).toEqual(opened.pipeline);
    expect(state.opened?.sha256).toBe(opened.sha256);
    // No default the file left out is filled in.
    expect(state.opened?.pipeline.source).not.toHaveProperty('autoOffsetReset');
    controller.close();
    expect(controller.current().opened).toBeNull();
    expect(agent.pending().map((r) => r.path)).toEqual(['/api/pipelines']);
  });

  it('shows a document the page schema refuses as unreadable, never as a pipeline', async () => {
    const { agent, controller } = setup();
    controller.setOnline(true);
    await listed(agent);
    controller.open({ root: '/home/user/code/app', directory: 'billing' });
    // A validate step with no onInvalid, which the schema requires.
    const broken = {
      ...opened,
      pipeline: {
        ...opened.pipeline,
        processingSteps: [
          {
            id: 'check',
            type: 'validate',
            configuration: { rules: [{ field: 'amount' }] },
          },
        ],
      },
    };
    agent.next('GET', '/api/pipelines/open').answer(json(200, broken));
    await agent.settle();
    const state = controller.current();
    expect(state.opened).toBeNull();
    expect(state.openFailure?.failure.kind).toBe('invalid');
    if (state.openFailure?.failure.kind === 'invalid') {
      expect(state.openFailure.failure.problems.length).toBeGreaterThan(0);
      expect(state.openFailure.failure.problems[0]?.pointer).toMatch(
        /^\/processingSteps\/0/,
      );
    }
  });

  it.each(pipelinesFixtures.errors.valid)(
    'shows the agent’s %s answer to an open in place of the graph',
    async ({ body }) => {
      const { agent, controller } = setup();
      controller.setOnline(true);
      await listed(agent);
      controller.open({ root: '/home/user/code/app', directory: 'billing' });
      agent.next('GET', '/api/pipelines/open').answer(json(409, body));
      await agent.settle();
      expect(controller.current().opened).toBeNull();
      expect(controller.current().openFailure?.failure).toEqual({
        kind: 'error',
        error: body,
      });
    },
  );

  it('creates from a draft, refreshes the list and opens the new pipeline', async () => {
    const { agent, controller } = setup();
    controller.setOnline(true);
    await listed(agent, fixtureList('an-empty-root'));
    controller.startCreate();
    // One root to create in, so it is chosen; nothing else is.
    expect(controller.current().draft).toEqual({
      root: '/home/user/code/app',
      name: '',
      language: '',
      sourceTopic: '',
      destinationTopic: '',
      networkMode: '',
    });
    controller.submitCreate();
    expect(agent.pending()).toHaveLength(0);
    for (const [field, value] of [
      ['name', 'Orders to billing'],
      ['language', 'python'],
      ['sourceTopic', 'orders.v1'],
      ['destinationTopic', 'billing.v1'],
      ['networkMode', 'docker'],
    ] as const) {
      controller.edit(field, value);
    }
    controller.submitCreate();
    const request = agent.next('POST', '/api/pipelines');
    expect(request.body).toEqual({
      root: '/home/user/code/app',
      name: 'Orders to billing',
      language: 'python',
      sourceTopic: 'orders.v1',
      destinationTopic: 'billing.v1',
      networkMode: 'docker',
    });
    request.answer(json(200, opened));
    await agent.settle();
    expect(controller.current().opened?.location).toEqual({
      root: opened.root,
      directory: opened.directory,
    });
    expect(controller.current().draft).toBeNull();
    expect(agent.pending().map((r) => r.path)).toEqual(['/api/pipelines']);
  });

  it('keeps the draft and says why when a create is refused', async () => {
    const { agent, controller } = setup();
    controller.setOnline(true);
    await listed(agent, fixtureList('an-empty-root'));
    controller.startCreate();
    for (const [field, value] of [
      ['name', 'x'],
      ['language', 'go'],
      ['sourceTopic', 'a'],
      ['destinationTopic', 'b'],
      ['networkMode', 'host'],
    ] as const) {
      controller.edit(field, value);
    }
    controller.submitCreate();
    const refused = { error: 'rootIsPipeline', message: 'it is a pipeline' };
    agent.next('POST', '/api/pipelines').answer(json(409, refused));
    await agent.settle();
    expect(controller.current().draft?.name).toBe('x');
    expect(controller.current().createFailure).toEqual({
      kind: 'error',
      error: refused,
    });
  });

  it('offers no root a pipeline cannot be created in', async () => {
    const { agent, controller } = setup();
    controller.setOnline(true);
    await listed(agent);
    controller.startCreate();
    // Two roots qualify, so neither is chosen for the user.
    expect(controller.current().draft?.root).toBe('');
    controller.startCreate('/home/user/code/single');
    expect(controller.current().draft?.root).toBe('');
    controller.startCreate('/home/user/code/other');
    expect(controller.current().draft?.root).toBe('/home/user/code/other');
  });

  it('a controller with no client asks nothing', () => {
    const controller = createPipelinesController(null);
    controller.setOnline(true);
    expect(controller.current().session).toBe(false);
    expect(controller.current().list).toBeNull();
  });
});

describe('checkDocument', () => {
  it('accepts every valid document fixture', () => {
    for (const { body } of pipelinesFixtures.documents.valid) {
      expect(checkDocument(body).ok).toBe(true);
    }
  });
});
