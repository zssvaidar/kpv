// No CORS from the dev server on `/api` (#65), against the real `pnpm dev`
// stack: the agent built from this checkout and the Vite dev server in front
// of it, both on ports they chose.
//
// The rule from #8 is that dev traffic is one origin, so nothing on the dev
// server may answer a cross-origin request for `/api`: no `2xx` to a
// preflight and no `Access-Control-*` header on anything. Vite's own CORS
// middleware, which allows localhost and 127.0.0.1 origins by default and
// runs ahead of every plugin's middleware, broke that until `server.cors` was
// turned off in vite.config.ts. These tests are what fails if it is turned
// back on, or if the proxy ends up behind it.

import { request, type IncomingHttpHeaders } from 'node:http';

import { afterAll, beforeAll, describe, expect, it } from 'vitest';

import { startDev, type DevSetup } from './dev-server.ts';

interface Answer {
  status: number;
  headers: IncomingHttpHeaders;
  rawHeaders: string[];
  body: string;
}

function send(
  method: string,
  url: string,
  headers: Record<string, string>,
): Promise<Answer> {
  return new Promise((resolve, reject) => {
    const req = request(url, { method, headers }, (res) => {
      let body = '';
      res.setEncoding('utf8');
      res.on('data', (chunk: string) => (body += chunk));
      res.on('end', () =>
        resolve({
          status: res.statusCode ?? 0,
          headers: res.headers,
          rawHeaders: res.rawHeaders,
          body,
        }),
      );
      res.on('error', reject);
    });
    req.on('error', reject);
    req.end();
  });
}

// Every response header whose name starts `access-control-`, in any case.
// By prefix on purpose: a header nobody listed here, such as
// Access-Control-Expose-Headers or Access-Control-Allow-Credentials, fails
// just the same.
function corsHeaders(answer: Answer): string[] {
  const names = answer.rawHeaders.filter((_, index) => index % 2 === 0);
  return [...names, ...Object.keys(answer.headers)].filter((name) =>
    name.toLowerCase().startsWith('access-control-'),
  );
}

const apiPaths = [
  '/api',
  '/api/',
  '/api?x=1',
  '/api/directories',
  '/api/health',
  '/api/channel',
  '/api/no-such-route',
];

// Localhost origins on a port that is not the dev server's are the ones
// Vite's default CORS actually allows, so they are what this has to try.
// `undefined` is a request with no Origin at all.
const origins = (setup: DevSetup): (string | undefined)[] => [
  'http://localhost:3000',
  'http://127.0.0.1:3000',
  'http://evil.example',
  'null',
  setup.origin,
  undefined,
];

const withOrigin = (
  origin: string | undefined,
  headers: Record<string, string> = {},
): Record<string, string> =>
  origin === undefined ? headers : { ...headers, Origin: origin };

const preflight = {
  'Access-Control-Request-Method': 'GET',
  'Access-Control-Request-Headers': 'authorization',
};

let setup: DevSetup;
beforeAll(async () => {
  setup = await startDev({ port: 0 });
}, 180_000);
afterAll(async () => {
  await setup.stop();
});

describe('pnpm dev answers no CORS for /api', { timeout: 180_000 }, () => {
  it('turns server.cors off, and preview.cors with it', () => {
    expect(setup.vite.config.server.cors).toBe(false);
    expect(setup.vite.config.preview.cors).toBe(false);
  });

  it('does not answer the preflight that used to get 204 and an allow header', async () => {
    const answer = await send('OPTIONS', `${setup.origin}/api/directories`, {
      Origin: 'http://localhost:3000',
      'Access-Control-Request-Method': 'GET',
    });
    expect(answer.status).not.toBe(204);
    expect(answer.headers['access-control-allow-origin']).toBeUndefined();
    expect(corsHeaders(answer)).toEqual([]);
  });

  for (const [kind, extra] of [
    ['a bare OPTIONS', {}],
    ['a full preflight', preflight],
  ] as const) {
    it(`gives ${kind} on every /api path, from every origin, the agent's uniform 404`, async () => {
      for (const path of apiPaths) {
        for (const origin of origins(setup)) {
          const label = `OPTIONS ${path} Origin=${origin ?? '(none)'}`;
          const answer = await send(
            'OPTIONS',
            `${setup.origin}${path}`,
            withOrigin(origin, { ...extra }),
          );
          expect(corsHeaders(answer), label).toEqual([]);
          expect(answer.status, label).toBe(404);
          expect(answer.body, label).toBe('not found\n');
          expect(answer.headers['content-type'], label).toBe(
            'text/plain; charset=utf-8',
          );
          expect(answer.headers['cache-control'], label).toBe('no-store');
          expect(answer.headers['x-content-type-options'], label).toBe(
            'nosniff',
          );
        }
      }
    });
  }

  it('adds no Access-Control-* header to GET or HEAD with a valid token, from any origin', async () => {
    const auth = { Authorization: `Bearer ${setup.agent.token}` };
    for (const method of ['GET', 'HEAD']) {
      for (const path of ['/api/directories', '/api/health']) {
        for (const origin of origins(setup)) {
          const label = `${method} ${path} Origin=${origin ?? '(none)'}`;
          const answer = await send(
            method,
            `${setup.origin}${path}`,
            withOrigin(origin, auth),
          );
          expect(corsHeaders(answer), label).toEqual([]);
          // The dev server's own origin is the page and is let through; a
          // foreign origin, or none without fetch metadata, is the agent's 404.
          expect(answer.status, label).toBe(
            origin === setup.origin ? 200 : 404,
          );
        }
      }
    }
  });

  it('still serves the page, the Vite client, the app modules and the HMR socket', async () => {
    const page = await send('GET', setup.openUrl, {});
    expect(page.status).toBe(200);
    expect(page.body).toContain('/@vite/client');
    for (const path of ['/@vite/client', '/src/main.tsx']) {
      const answer = await send('GET', `${setup.origin}${path}`, {});
      expect(answer.status, path).toBe(200);
      expect(corsHeaders(answer), path).toEqual([]);
    }
    const hmr = await new Promise<number>((resolve, reject) => {
      const req = request(
        `${setup.origin}/?token=${encodeURIComponent(setup.vite.config.webSocketToken)}`,
        {
          headers: {
            Connection: 'Upgrade',
            Upgrade: 'websocket',
            'Sec-WebSocket-Version': '13',
            'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ==',
            'Sec-WebSocket-Protocol': 'vite-hmr',
            Origin: setup.origin,
          },
        },
      );
      req.on('upgrade', (res, socket) => {
        socket.destroy();
        resolve(res.statusCode ?? 0);
      });
      req.on('response', (res) => {
        res.resume();
        resolve(res.statusCode ?? 0);
      });
      req.on('error', reject);
      req.end();
    });
    expect(hmr).toBe(101);
  });

  // Last, because it stops the agent: with nothing to forward to, a preflight
  // gets the dropped connection #8 chose for an absent agent, not Vite's 204
  // and not a 502.
  it('drops a preflight once the agent is gone', async () => {
    await setup.agent.stop();
    await expect(
      send('OPTIONS', `${setup.origin}/api/directories`, {
        Origin: 'http://localhost:3000',
        ...preflight,
      }),
    ).rejects.toThrow();
  });
});
