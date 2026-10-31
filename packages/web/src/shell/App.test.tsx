import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { reopenAdvice } from '../connection/describe.ts';
import { createConnectionMonitor } from '../connection/monitor.ts';
import { fakeAgent, json } from '../picker/fake-agent.ts';
import { createPipelinesController } from '../pipelines/controller.ts';
import { fixtureList, pipelinesFixtures } from '../pipelines/fixtures.ts';
import { pipelinesErrorTitles, unreadableWords } from '../pipelines/words.ts';
import { App, Shell } from './App.tsx';

// What a person reads, without the markup: React escapes quotes in text.
const text = (html: string) =>
  html
    .replace(/<[^>]*>/g, ' ')
    .replace(/&quot;/g, '"')
    .replace(/&#x27;/g, "'");

describe('Shell', () => {
  it('renders the three regions', () => {
    const html = renderToStaticMarkup(
      <Shell state={{ status: 'connected' }} client={null} />,
    );
    expect(html).toContain('aria-label="Graph area"');
    expect(html).toContain('aria-label="Side panel"');
    expect(html).toContain('aria-label="Bottom panel"');
  });

  it('shows the connection state in the status bar whatever it is', () => {
    const html = renderToStaticMarkup(
      <Shell
        state={{ status: 'unreachable', retryInMs: 1000 }}
        client={null}
      />,
    );
    expect(html).toMatch(/role="status"[^>]*>Agent not reachable</);
  });

  it('says in the graph area why there is nothing, rather than showing it empty', () => {
    const refused = renderToStaticMarkup(
      <Shell
        state={{ status: 'unauthenticated', reason: 'rejected' }}
        client={null}
      />,
    );
    const absent = renderToStaticMarkup(
      <Shell
        state={{ status: 'unreachable', retryInMs: 1000 }}
        client={null}
      />,
    );
    expect(refused).toContain('data-state="unauthenticated"');
    expect(absent).toContain('data-state="unreachable"');
    expect(refused).not.toBe(absent);
  });
});

describe('the pipeline list as the entry screen', () => {
  async function listedController() {
    const agent = fakeAgent();
    const controller = createPipelinesController(agent.client);
    controller.setOnline(true);
    agent
      .next('GET', '/api/pipelines')
      .answer(json(200, fixtureList('every-state')));
    await agent.settle();
    return { agent, controller };
  }

  it('shows the list in the graph area when connected and nothing is open', async () => {
    const { controller } = await listedController();
    const html = renderToStaticMarkup(
      <Shell
        state={{ status: 'connected' }}
        client={null}
        pipelines={controller}
      />,
    );
    expect(html).toContain('id="pipelines-heading"');
    expect(text(html)).toContain('/home/user/code/app');
    expect(html).toContain('id="directory-picker"');
    expect(html).not.toContain('react-flow');
  });

  it('draws an opened entry in the graph, with a way back to the list', async () => {
    const { agent, controller } = await listedController();
    controller.open({ root: '/home/user/code/app', directory: 'billing' });
    agent
      .next('GET', '/api/pipelines/open')
      .answer(json(200, pipelinesFixtures.documents.valid[0]?.body));
    await agent.settle();
    const html = renderToStaticMarkup(
      <Shell
        state={{ status: 'connected' }}
        client={null}
        pipelines={controller}
      />,
    );
    expect(html).toContain('react-flow');
    expect(text(html)).toContain('Back to the pipelines');
    expect(html).not.toContain('id="pipelines-heading"');
  });

  it('shows a failed open in place of the graph, and draws nothing', async () => {
    const { agent, controller } = await listedController();
    controller.open({ root: '/home/user/code/app', directory: 'hand-edited' });
    const unreadable = pipelinesFixtures.errors.valid.find(
      (c) => c.name === 'unreadable',
    )?.body;
    agent.next('GET', '/api/pipelines/open').answer(json(422, unreadable));
    await agent.settle();
    const html = renderToStaticMarkup(
      <Shell
        state={{ status: 'connected' }}
        client={null}
        pipelines={controller}
      />,
    );
    expect(html).toContain('aria-label="Pipeline not opened"');
    expect(html).not.toContain('react-flow');
    // No block is drawn; the status bar's way to the connection screen is
    // the only Kafka on the page.
    expect(html.replace('Kafka connections</button>', '')).not.toContain(
      'Kafka',
    );
  });
});

describe('an entry that can no longer be searched', () => {
  it('shows the unreadable words in place of the graph, not "No pipeline here"', async () => {
    const agent = fakeAgent();
    const controller = createPipelinesController(agent.client);
    controller.setOnline(true);
    agent
      .next('GET', '/api/pipelines')
      .answer(json(200, fixtureList('every-state')));
    await agent.settle();
    controller.open({ root: '/home/user/code/app', directory: 'billing' });
    // What the agent answers for a directory chmod 644 after it was listed.
    agent.next('GET', '/api/pipelines/open').answer(
      json(422, {
        error: 'unreadable',
        message: "this pipeline's pipeline.yaml did not load",
        unreadable: { kind: 'unreadable', problems: [] },
      }),
    );
    await agent.settle();
    const html = renderToStaticMarkup(
      <Shell
        state={{ status: 'connected' }}
        client={null}
        pipelines={controller}
      />,
    );
    expect(html).toContain('aria-label="Pipeline not opened"');
    expect(text(html)).toContain(unreadableWords.unreadable);
    expect(text(html)).toContain(pipelinesErrorTitles.unreadable);
    expect(text(html)).not.toContain(pipelinesErrorTitles.notFound);
    expect(html).not.toContain('react-flow');
  });
});

describe('App', () => {
  it('after a reload with the token stripped, shows the authentication state and the way back', () => {
    // main.tsx with no token in the URL: no client, so a monitor that asks
    // the agent nothing.
    const html = renderToStaticMarkup(
      <App client={null} monitor={createConnectionMonitor(null)} />,
    );
    expect(html).toContain('data-state="unauthenticated"');
    expect(text(html)).toContain('Not authorized');
    expect(text(html)).toContain(reopenAdvice);
  });
});

describe('the way to the connection screen (#24)', () => {
  it('is in the status bar, and the screen takes the graph area, not the side panel', () => {
    const pipelinesHtml = renderToStaticMarkup(
      <Shell state={{ status: 'connected' }} client={null} />,
    );
    expect(pipelinesHtml).toMatch(
      /<header class="status-bar">.*Kafka connections<\/button><\/header>/s,
    );
    const html = renderToStaticMarkup(
      <Shell
        state={{ status: 'connected' }}
        client={null}
        screen="connections"
      />,
    );
    const graphArea =
      /<main class="graph-area"[^>]*>(.*)<\/main>/s.exec(html)?.[1] ?? '';
    const sidePanel =
      /<aside class="side-panel"[^>]*>(.*)<\/aside>/s.exec(html)?.[1] ?? '';
    expect(graphArea).toContain('Kafka connections</h1>');
    expect(sidePanel).not.toContain('Kafka connections');
  });

  it('is not shown while there is no working connection to the agent', () => {
    const html = renderToStaticMarkup(
      <Shell
        state={{ status: 'unreachable', retryInMs: 1000 }}
        client={null}
        screen="connections"
      />,
    );
    expect(html).not.toContain('Kafka connections</h1>');
    expect(html).toContain('data-state="unreachable"');
  });
});
