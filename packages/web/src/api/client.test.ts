import { describe, expect, it } from 'vitest';

import {
  createAgentClient,
  type AgentClient,
  type ClientEnvironment,
} from './client.ts';

const token = 'the-session-token';
const origin = 'http://127.0.0.1:5173';

const listing = {
  path: '/home/user',
  entries: [
    {
      name: 'code',
      path: '/home/user/code',
      kind: 'directory',
      unreadable: false,
    },
  ],
  truncated: false,
  limit: 1000,
  looksLikeProject: false,
  evidence: [],
};

// The agent's uniform rejection (#4): 404, a fixed plain-text body.
const rejection = () =>
  new Response('not found\n', {
    status: 404,
    headers: { 'Content-Type': 'text/plain; charset=utf-8' },
  });
const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json; charset=utf-8' },
  });

interface Sent {
  url: string;
  init: RequestInit | undefined;
}

class FakeSocket {
  static opened: string[] = [];
  constructor(readonly url: string) {
    FakeSocket.opened.push(url);
  }
}

function client(answer: (url: string) => Response | Promise<Response>) {
  const sent: Sent[] = [];
  const environment: ClientEnvironment = {
    origin,
    fetch: (input, init) => {
      const url = input instanceof Request ? input.url : String(input);
      sent.push({ url, init });
      return Promise.resolve(answer(url));
    },
    WebSocket: FakeSocket as unknown as ClientEnvironment['WebSocket'],
  };
  return { api: createAgentClient(token, environment), sent };
}

// Every method of the client that makes a request, called once.
const everyRequest: [string, (api: AgentClient) => Promise<unknown>][] = [
  ['probe', (api) => api.probe()],
  ['listDirectories', (api) => api.listDirectories()],
  [
    'listDirectories with a query',
    (api) => api.listDirectories({ path: '/tmp', limit: 5 }),
  ],
];

describe('createAgentClient', () => {
  it.each(everyRequest)(
    '%s carries the token as a bearer header',
    async (_name, call) => {
      const { api, sent } = client(() => json(200, listing));
      await call(api);
      expect(sent).toHaveLength(1);
      const headers = new Headers(sent[0]?.init?.headers);
      expect(headers.get('Authorization')).toBe(`Bearer ${token}`);
      // And not in the URL, which is what gets logged and kept in history.
      expect(sent[0]?.url).not.toContain(token);
      // Nothing that would make a browser send a cookie or follow a redirect
      // somewhere the token should not go.
      expect(sent[0]?.init?.credentials).toBe('omit');
      expect(sent[0]?.init?.redirect).toBe('error');
    },
  );

  it('exposes nothing but the typed calls, so no caller can send its own request', () => {
    const { api } = client(() => json(200, listing));
    expect(Object.keys(api).sort()).toEqual([
      'listDirectories',
      'openChannel',
      'probe',
    ]);
  });

  it('asks the page origin, so the browser request is same-origin and never preflighted', async () => {
    const { api, sent } = client(() => json(200, listing));
    await api.listDirectories({ path: '/home/user', limit: 5 });
    const url = new URL(sent[0]?.url ?? '');
    expect(url.origin).toBe(origin);
    expect(url.pathname).toBe('/api/directories');
    expect(url.searchParams.get('path')).toBe('/home/user');
    expect(url.searchParams.get('limit')).toBe('5');
    // A GET with only an Authorization header to its own origin is not a
    // CORS request at all; no content type, no custom method.
    expect(sent[0]?.init?.method).toBe('GET');
    expect(sent[0]?.init?.body).toBeUndefined();
  });

  it('parses a listing with the generated schema', async () => {
    const { api } = client(() => json(200, listing));
    expect(await api.listDirectories()).toEqual({ kind: 'ok', value: listing });
  });

  it('reads the uniform 404 as a rejection, an authentication problem', async () => {
    const { api } = client(rejection);
    expect(await api.listDirectories()).toEqual({ kind: 'rejected' });
    expect(await api.probe()).toEqual({ kind: 'rejected' });
  });

  it('reads no answer at all as an agent that is not there', async () => {
    const { api } = client(() =>
      Promise.reject(new TypeError('Failed to fetch')),
    );
    expect(await api.listDirectories()).toEqual({ kind: 'unreachable' });
    expect(await api.probe()).toEqual({ kind: 'unreachable' });
  });

  it("reads the endpoint's own typed 404 as that error, not as a rejection", async () => {
    // GET /api/directories answers a directory that does not exist with a 404
    // too, but with a DirectoryError body: the agent accepted us.
    const notFound = {
      error: 'notFound',
      message: 'this directory does not exist',
    };
    const { api } = client(() => json(404, notFound));
    expect(await api.listDirectories({ path: '/nope' })).toEqual({
      kind: 'error',
      status: 404,
      error: notFound,
    });
  });

  it('reads a route this agent never served as a rejection, which is the known consequence', async () => {
    // The agent answers a path it does not serve exactly as it answers a
    // refused request, so the client cannot tell them apart and does not try.
    // net/http's own routing 404 (what an authorized request to an unknown
    // path gets) is a 404 that is not the endpoint's typed error either.
    const routing = () => new Response('404 page not found\n', { status: 404 });
    const { api } = client(routing);
    expect(await api.listDirectories()).toEqual({ kind: 'rejected' });
  });

  it('reads any other answer as a failure that is neither', async () => {
    const { api } = client(() => new Response('boom', { status: 500 }));
    expect(await api.listDirectories()).toEqual({
      kind: 'failed',
      status: 500,
    });
    expect(await api.probe()).toEqual({ kind: 'failed', status: 500 });
  });

  it('reads a 200 the schema does not accept as a failure, not as data', async () => {
    const { api } = client(() => json(200, { path: '' }));
    expect(await api.listDirectories()).toEqual({
      kind: 'failed',
      status: 200,
    });
  });

  it('opens the channel on the page origin with the token in the handshake URL', () => {
    FakeSocket.opened = [];
    const { api } = client(() => json(200, listing));
    api.openChannel();
    expect(FakeSocket.opened).toHaveLength(1);
    const url = new URL(FakeSocket.opened[0] ?? '');
    expect(url.protocol).toBe('ws:');
    expect(url.host).toBe('127.0.0.1:5173');
    expect(url.pathname).toBe('/api/channel');
    expect(url.searchParams.get('token')).toBe(token);
  });
});
