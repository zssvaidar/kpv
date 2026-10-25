import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { reopenAdvice } from '../connection/describe.ts';
import { createConnectionMonitor } from '../connection/monitor.ts';
import { App, Shell } from './App.tsx';
import { DirectoryView } from './DirectoryStub.tsx';

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

describe('DirectoryView', () => {
  it('renders the listing the agent answered', () => {
    const html = renderToStaticMarkup(
      <DirectoryView
        listing={{
          kind: 'ok',
          value: {
            path: '/home/user',
            entries: [
              {
                name: 'code',
                path: '/home/user/code',
                kind: 'directory',
                unreadable: false,
              },
              {
                name: 'notes.txt',
                path: '/home/user/notes.txt',
                kind: 'file',
                unreadable: false,
              },
            ],
            truncated: false,
            limit: 100,
            looksLikeProject: false,
            evidence: [],
          },
        }}
      />,
    );
    expect(html).toContain('/home/user');
    expect(html).toContain('code/');
    expect(html).toContain('notes.txt');
  });

  it("renders the endpoint's own error in its words", () => {
    const html = renderToStaticMarkup(
      <DirectoryView
        listing={{
          kind: 'error',
          status: 403,
          error: { error: 'forbidden', message: 'not browsable' },
        }}
      />,
    );
    expect(html).toContain('not browsable');
  });
});
