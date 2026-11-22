import type { Connection } from '@kpv/schema';
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
  ['listRoots', (api) => api.listRoots()],
  ['authorizeRoot', (api) => api.authorizeRoot('/home/user/code')],
  ['revokeRoot', (api) => api.revokeRoot('/home/user/code')],
  ['listPipelines', (api) => api.listPipelines()],
  [
    'openPipeline',
    (api) => api.openPipeline({ root: '/home/user/code', directory: 'a' }),
  ],
  [
    'createPipeline',
    (api) =>
      api.createPipeline({
        root: '/home/user/code',
        name: 'Orders',
        language: 'go',
        sourceTopic: 'in',
        destinationTopic: 'out',
        networkMode: 'docker',
      }),
  ],
  [
    'readSource',
    (api) =>
      api.readSource({
        root: '/home/user/code',
        directory: 'a',
        path: 'src/main.py',
      }),
  ],
  [
    'writeSource',
    (api) =>
      api.writeSource({
        root: '/home/user/code',
        path: 'src/main.py',
        text: '',
        expectedAbsent: true,
      }),
  ],
  ['listConnections', (api) => api.listConnections()],
  ['listConnectionNames', (api) => api.listConnectionNames()],
  [
    'createConnection',
    (api) =>
      api.createConnection({
        name: 'local',
        bootstrapServers: ['localhost:9092'],
        securityProtocol: 'PLAINTEXT',
      }),
  ],
  [
    'updateConnection',
    (api) =>
      api.updateConnection({
        current: 'local',
        name: 'local',
        bootstrapServers: ['localhost:9092'],
        securityProtocol: 'PLAINTEXT',
      }),
  ],
  ['deleteConnection', (api) => api.deleteConnection({ name: 'local' })],
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
      'authorizeRoot',
      'createConnection',
      'createPipeline',
      'deleteConnection',
      'listConnectionNames',
      'listConnections',
      'listDirectories',
      'listPipelines',
      'listRoots',
      'openChannel',
      'openPipeline',
      'probe',
      'readSource',
      'revokeRoot',
      'updateConnection',
      'writeSource',
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

describe('the roots methods', () => {
  const roots = {
    roots: [
      { path: '/home/user/code/app', available: true },
      { path: '/home/user/gone', available: false, reason: 'notFound' },
    ],
  };

  it('lists the roots with GET /api/roots and the generated schema', async () => {
    const { api, sent } = client(() => json(200, roots));
    expect(await api.listRoots()).toEqual({ kind: 'ok', value: roots });
    const url = new URL(sent[0]?.url ?? '');
    expect(url.pathname).toBe('/api/roots');
    expect(url.search).toBe('');
    expect(sent[0]?.init?.method).toBe('GET');
    expect(sent[0]?.init?.body).toBeUndefined();
  });

  it.each([
    ['authorizeRoot', 'POST', { path: '/home/user/code/app' }],
    ['revokeRoot', 'DELETE', roots],
  ] as const)(
    '%s sends %s with a JSON RootRequest and nothing else',
    async (method, verb, answer) => {
      const { api, sent } = client(() => json(200, answer));
      const result = await api[method]('/home/user/code/app');
      expect(result).toEqual({ kind: 'ok', value: answer });
      const url = new URL(sent[0]?.url ?? '');
      expect(url.origin).toBe(origin);
      expect(url.pathname).toBe('/api/roots');
      expect(url.search).toBe('');
      expect(sent[0]?.init?.method).toBe(verb);
      const headers = new Headers(sent[0]?.init?.headers);
      expect(headers.get('Content-Type')).toBe('application/json');
      expect(headers.get('Authorization')).toBe(`Bearer ${token}`);
      const body = sent[0]?.init?.body;
      expect(typeof body).toBe('string');
      expect(JSON.parse(body as string)).toEqual({
        path: '/home/user/code/app',
      });
    },
  );

  it("reads a revoke's typed 404 as notFound, not as a rejection", async () => {
    const notFound = {
      error: 'notFound',
      message: 'this path is not an authorized project directory',
    };
    const { api } = client(() => json(404, notFound));
    expect(await api.revokeRoot('/home/user/code/app')).toEqual({
      kind: 'error',
      status: 404,
      error: notFound,
    });
  });

  it('reads an overlaps refusal with the root it names', async () => {
    const overlaps = {
      error: 'overlaps',
      message:
        'this directory is inside, or contains, a project directory that is already authorized',
      root: '/home/user/code/app',
    };
    const { api } = client(() => json(409, overlaps));
    expect(await api.authorizeRoot('/home/user/code')).toEqual({
      kind: 'error',
      status: 409,
      error: overlaps,
    });
  });

  it('reads the uniform rejection, no answer and a bad answer the same way as every other call', async () => {
    expect(await client(rejection).api.listRoots()).toEqual({
      kind: 'rejected',
    });
    expect(await client(rejection).api.authorizeRoot('/x')).toEqual({
      kind: 'rejected',
    });
    expect(
      await client(() =>
        Promise.reject(new TypeError('Failed to fetch')),
      ).api.revokeRoot('/x'),
    ).toEqual({ kind: 'unreachable' });
    expect(
      await client(() =>
        json(200, { roots: [{ path: '/x' }] }),
      ).api.listRoots(),
    ).toEqual({ kind: 'failed', status: 200 });
    expect(
      await client(() =>
        json(200, { path: '/x', available: true }),
      ).api.authorizeRoot('/x'),
    ).toEqual({ kind: 'failed', status: 200 });
  });
});

describe('the pipeline methods', () => {
  const list = {
    roots: [
      {
        path: '/home/user/code',
        available: true,
        truncated: false,
        skippedLinks: 0,
        pipelines: [
          {
            root: '/home/user/code',
            directory: 'orders',
            readable: true,
            id: 'orders',
            language: 'go',
          },
        ],
      },
    ],
  };
  const document = {
    root: '/home/user/code',
    directory: 'orders',
    pipeline: { id: 'orders' },
    sha256: 'a'.repeat(64),
  };

  it('lists the pipelines with GET /api/pipelines and the generated schema', async () => {
    const { api, sent } = client(() => json(200, list));
    expect(await api.listPipelines()).toEqual({ kind: 'ok', value: list });
    const url = new URL(sent[0]?.url ?? '');
    expect(url.pathname).toBe('/api/pipelines');
    expect(url.search).toBe('');
    expect(sent[0]?.init?.method).toBe('GET');
  });

  it('opens by root and directory only, and leaves the directory out for a root pipeline', async () => {
    const { api, sent } = client(() => json(200, document));
    expect(
      await api.openPipeline({ root: '/home/user/code', directory: 'orders' }),
    ).toEqual({ kind: 'ok', value: document });
    await api.openPipeline({ root: '/home/user/single' });
    const first = new URL(sent[0]?.url ?? '');
    expect(first.pathname).toBe('/api/pipelines/open');
    expect([...first.searchParams.keys()]).toEqual(['root', 'directory']);
    expect(first.searchParams.get('root')).toBe('/home/user/code');
    expect(first.searchParams.get('directory')).toBe('orders');
    const second = new URL(sent[1]?.url ?? '');
    expect([...second.searchParams.keys()]).toEqual(['root']);
  });

  it('creates with a JSON body of exactly the request fields', async () => {
    const { api, sent } = client(() => json(200, document));
    const request = {
      root: '/home/user/code',
      name: 'Orders',
      language: 'go',
      sourceTopic: 'in',
      destinationTopic: 'out',
      networkMode: 'docker',
    };
    expect(await api.createPipeline({ ...request })).toEqual({
      kind: 'ok',
      value: document,
    });
    expect(sent[0]?.init?.method).toBe('POST');
    const headers = new Headers(sent[0]?.init?.headers);
    expect(headers.get('Content-Type')).toBe('application/json');
    expect(JSON.parse(sent[0]?.init?.body as string)).toEqual(request);
  });

  it("reads a typed error as the endpoint's own, and anything else as a rejection or failure", async () => {
    const notFound = { error: 'notFound', message: 'there is no pipeline' };
    expect(
      await client(() => json(404, notFound)).api.openPipeline({ root: '/x' }),
    ).toEqual({ kind: 'error', status: 404, error: notFound });
    expect(await client(rejection).api.listPipelines()).toEqual({
      kind: 'rejected',
    });
    expect(
      await client(() =>
        json(200, { roots: [{ path: '/x' }] }),
      ).api.listPipelines(),
    ).toEqual({ kind: 'failed', status: 200 });
  });
});

describe('the source file methods', () => {
  const sha = 'a'.repeat(64);
  const file = {
    root: '/home/user/code',
    directory: 'orders',
    path: 'src/main.py',
    text: 'a = 1\r\n',
    sha256: sha,
    generated: false,
  };
  const written = {
    root: '/home/user/code',
    directory: 'orders',
    path: 'src/main.py',
    sha256: 'b'.repeat(64),
  };

  it('reads by root, directory and path only, and leaves the directory out for a root pipeline', async () => {
    const { api, sent } = client(() => json(200, file));
    expect(
      await api.readSource({
        root: '/home/user/code',
        directory: 'orders',
        path: 'src/main.py',
      }),
    ).toEqual({ kind: 'ok', value: file });
    await api.readSource({ root: '/home/user/single', path: 'src/a.py' });
    const first = new URL(sent[0]?.url ?? '');
    expect(first.pathname).toBe('/api/pipelines/source');
    expect([...first.searchParams.keys()]).toEqual([
      'root',
      'directory',
      'path',
    ]);
    expect(first.searchParams.get('path')).toBe('src/main.py');
    expect(sent[0]?.init?.method).toBe('GET');
    const second = new URL(sent[1]?.url ?? '');
    expect([...second.searchParams.keys()]).toEqual(['root', 'path']);
  });

  it('writes with PUT and a JSON body of exactly the request fields', async () => {
    const { api, sent } = client(() => json(200, written));
    expect(
      await api.writeSource({
        root: '/home/user/code',
        directory: 'orders',
        path: 'src/main.py',
        text: 'a = 2\n',
        expectedSha256: sha,
      }),
    ).toEqual({ kind: 'ok', value: written });
    await api.writeSource({
      root: '/home/user/single',
      path: 'src/new.py',
      text: '',
      expectedAbsent: true,
    });
    expect(sent[0]?.init?.method).toBe('PUT');
    expect(new URL(sent[0]?.url ?? '').pathname).toBe('/api/pipelines/source');
    expect(new Headers(sent[0]?.init?.headers).get('Content-Type')).toBe(
      'application/json',
    );
    expect(JSON.parse(sent[0]?.init?.body as string)).toEqual({
      root: '/home/user/code',
      directory: 'orders',
      path: 'src/main.py',
      text: 'a = 2\n',
      expectedSha256: sha,
    });
    expect(JSON.parse(sent[1]?.init?.body as string)).toEqual({
      root: '/home/user/single',
      path: 'src/new.py',
      text: '',
      expectedAbsent: true,
    });
  });

  it('takes exactly one of the hash and expectedAbsent, by its type', () => {
    const { api } = client(() => json(200, written));
    const location = { root: '/x', path: 'a', text: '' };
    // @ts-expect-error neither expectation
    void api.writeSource({ ...location });
    const both = {
      ...location,
      expectedSha256: sha,
      expectedAbsent: true as const,
    };
    // @ts-expect-error both expectations
    void api.writeSource(both);
  });

  it('reads each kind as a typed error, a changed one with its current hash', async () => {
    const changed = {
      error: 'changed',
      message:
        'the file or a directory on its path changed since it was read, so nothing was written',
      sha256: 'c'.repeat(64),
    };
    const answer = await client(() => json(409, changed)).api.writeSource({
      root: '/x',
      path: 'a',
      text: '',
      expectedSha256: sha,
    });
    expect(answer).toEqual({ kind: 'error', status: 409, error: changed });
    if (answer.kind === 'error' && answer.error.error === 'changed') {
      expect(answer.error.sha256).toBe('c'.repeat(64));
    }
    const notFound = { error: 'notFound', message: 'there is no such file' };
    expect(
      await client(() => json(404, notFound)).api.readSource({
        root: '/x',
        path: 'a',
      }),
    ).toEqual({ kind: 'error', status: 404, error: notFound });
    // An error the schema does not name is not trusted as one.
    expect(
      await client(() =>
        json(500, { error: 'EACCES', message: '/etc/shadow' }),
      ).api.readSource({ root: '/x', path: 'a' }),
    ).toEqual({ kind: 'failed', status: 500 });
    expect(
      await client(rejection).api.readSource({ root: '/x', path: 'a' }),
    ).toEqual({ kind: 'rejected' });
    // A read answer without its hash is not data.
    expect(
      await client(() =>
        json(200, { ...file, sha256: undefined }),
      ).api.readSource({ root: '/x', path: 'a' }),
    ).toEqual({ kind: 'failed', status: 200 });
  });
});

describe('the connection methods', () => {
  const summary = {
    name: 'local',
    bootstrapServers: ['localhost:9092'],
    securityProtocol: 'SASL_PLAINTEXT',
    saslMechanism: 'PLAIN',
    saslUsername: 'u',
    saslPasswordSet: true,
    tlsClientKeySet: false,
  };

  it('lists with GET /api/connections and the names with GET /api/connections/names', async () => {
    const { api, sent } = client((url) =>
      url.endsWith('/names')
        ? json(200, { names: [] })
        : json(200, { connections: [summary] }),
    );
    expect(await api.listConnections()).toEqual({
      kind: 'ok',
      value: { connections: [summary] },
    });
    expect(await api.listConnectionNames()).toEqual({
      kind: 'ok',
      value: { names: [] },
    });
    expect(new URL(sent[0]?.url ?? '').pathname).toBe('/api/connections');
    expect(new URL(sent[1]?.url ?? '').pathname).toBe('/api/connections/names');
  });

  it('refuses an answer that carries a credential', async () => {
    const leaking = { connections: [{ ...summary, saslPassword: 'p' }] };
    expect(
      await client(() => json(200, leaking)).api.listConnections(),
    ).toEqual({ kind: 'failed', status: 200 });
  });

  it('creates, updates and deletes with POST, PUT and DELETE and a JSON body', async () => {
    const { api, sent } = client(() => json(200, summary));
    const record: Connection = {
      name: 'local',
      bootstrapServers: ['localhost:9092'],
      securityProtocol: 'SASL_PLAINTEXT',
      saslMechanism: 'PLAIN',
      saslUsername: 'u',
      saslPassword: 'typed',
    };
    expect(await api.createConnection(record)).toEqual({
      kind: 'ok',
      value: summary,
    });
    await api.updateConnection({
      current: 'local',
      name: record.name,
      bootstrapServers: record.bootstrapServers,
      securityProtocol: record.securityProtocol,
      remove: ['saslPassword'],
    });
    expect(sent[0]?.init?.method).toBe('POST');
    expect(JSON.parse(sent[0]?.init?.body as string)).toEqual(record);
    expect(sent[1]?.init?.method).toBe('PUT');
    expect(JSON.parse(sent[1]?.init?.body as string)).not.toHaveProperty(
      'saslPassword',
    );
    const deleted = client(() => json(200, { connections: [] }));
    expect(
      await deleted.api.deleteConnection({ name: 'local', confirm: true }),
    ).toEqual({ kind: 'ok', value: { connections: [] } });
    expect(deleted.sent[0]?.init?.method).toBe('DELETE');
    expect(JSON.parse(deleted.sent[0]?.init?.body as string)).toEqual({
      name: 'local',
      confirm: true,
    });
  });

  it("reads a referenced refusal as the endpoint's own error, with its report", async () => {
    const referenced = {
      error: 'referenced',
      message: 'm',
      references: [
        {
          root: '/home/u/code',
          directory: 'orders',
          pipelineId: 'orders',
          block: 'source',
        },
      ],
      unchecked: [],
    };
    expect(
      await client(() => json(409, referenced)).api.deleteConnection({
        name: 'local',
      }),
    ).toEqual({ kind: 'error', status: 409, error: referenced });
    const notFound = { error: 'notFound', message: 'm' };
    expect(
      await client(() => json(404, notFound)).api.deleteConnection({
        name: 'x',
      }),
    ).toEqual({ kind: 'error', status: 404, error: notFound });
  });
});
