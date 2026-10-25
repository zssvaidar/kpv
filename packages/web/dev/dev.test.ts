// The development topology against a real agent: the agent built from this
// checkout and started on its ephemeral port, the Vite dev server in front of
// it on one of its own, and requests shaped the way a browser shapes them.
// No fixed port and no sleep: every address is read from the process that
// chose it, and every wait is on an event.

import { request, type IncomingHttpHeaders } from 'node:http';

import { directoryListingSchema } from '@kpv/schema';
import { afterEach, describe, expect, it } from 'vitest';

import { startDev, type DevSetup } from './dev-server.ts';

interface Answer {
  status: number;
  headers: IncomingHttpHeaders;
  body: string;
}

// One request through the dev server, with exactly the headers given: Node's
// own fetch would add an Origin-less request too, but this way nothing is
// added that the test did not write down.
function get(url: string, headers: Record<string, string>): Promise<Answer> {
  return new Promise((resolve, reject) => {
    const req = request(url, { headers }, (res) => {
      let body = '';
      res.setEncoding('utf8');
      res.on('data', (chunk: string) => (body += chunk));
      res.on('end', () =>
        resolve({ status: res.statusCode ?? 0, headers: res.headers, body }),
      );
      res.on('error', reject);
    });
    req.on('error', reject);
    req.end();
  });
}

// A WebSocket handshake through the dev server; resolves with the status the
// handshake got, 101 when it was upgraded.
function handshake(url: string, origin: string): Promise<number> {
  return new Promise((resolve, reject) => {
    const req = request(url, {
      headers: {
        Connection: 'Upgrade',
        Upgrade: 'websocket',
        'Sec-WebSocket-Version': '13',
        'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ==',
        Origin: origin,
      },
    });
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
}

// What the page's own GET looks like to the dev server: same-origin, so no
// Origin header, and the fetch metadata a browser adds that a page cannot.
const pageGet = (token: string) => ({
  Authorization: `Bearer ${token}`,
  'Sec-Fetch-Site': 'same-origin',
  'Sec-Fetch-Mode': 'cors',
});

const running: DevSetup[] = [];
let agentLog = '';
const start = async () => {
  const setup = await startDev({
    port: 0,
    agentLog: (chunk) => (agentLog += chunk.toString()),
  });
  running.push(setup);
  return setup;
};
afterEach(async () => {
  await Promise.all(running.splice(0).map((setup) => setup.stop()));
});

describe('pnpm dev', { timeout: 180_000 }, () => {
  it('renders live data twice in a row, with the agent restarted on another port in between', async () => {
    const seen: { agent: string; token: string }[] = [];
    for (let run = 0; run < 2; run += 1) {
      const setup = await start();
      const answer = await get(
        `${setup.origin}/api/directories?limit=5`,
        pageGet(setup.agent.token),
      );
      expect(answer.status).toBe(200);
      expect(answer.headers['access-control-allow-origin']).toBeUndefined();
      expect(
        directoryListingSchema.safeParse(JSON.parse(answer.body)).success,
      ).toBe(true);
      seen.push({ agent: setup.agent.origin, token: setup.agent.token });
      await setup.stop();
      running.splice(0);
    }
    expect(seen[0]?.agent).not.toBe(seen[1]?.agent);
    expect(seen[0]?.token).not.toBe(seen[1]?.token);
  });

  it('serves the page at the URL it prints, with the token in it', async () => {
    const setup = await start();
    const url = new URL(setup.openUrl);
    expect(url.origin).toBe(setup.origin);
    expect(url.searchParams.get('token')).toBe(setup.agent.token);
    const page = await get(setup.openUrl, {});
    expect(page.status).toBe(200);
    expect(page.body).toContain('<div id="root">');
    expect(page.body).toContain(
      '<meta name="referrer" content="no-referrer" />',
    );
  });

  it("presents the agent's own origin for the page's requests, and nothing for anyone else's", async () => {
    const setup = await start();
    const { token } = setup.agent;
    const health = `${setup.origin}/api/health`;
    const auth = { Authorization: `Bearer ${token}` };

    // The page: a same-origin GET with no Origin, and a request carrying the
    // dev server's own origin, which is what its handshake and any non-GET do.
    expect((await get(health, pageGet(token))).status).toBe(200);
    expect((await get(health, { ...auth, Origin: setup.origin })).status).toBe(
      200,
    );

    // Anyone else is passed through as they came, and the agent's unchanged
    // rules reject them with its uniform 404.
    for (const headers of [
      { ...auth, Origin: 'http://evil.example' },
      {
        ...auth,
        Origin: 'http://evil.example',
        'Sec-Fetch-Site': 'cross-site',
      },
      { ...auth, 'Sec-Fetch-Site': 'cross-site' },
      auth,
    ]) {
      const refused = await get(health, headers);
      expect(refused.status, JSON.stringify(headers)).toBe(404);
      expect(refused.body).toBe('not found\n');
    }

    // The token is still the agent's to check: the proxy adds none.
    const noToken = await get(health, { 'Sec-Fetch-Site': 'same-origin' });
    expect(noToken.status).toBe(404);
    expect(noToken.body).toBe('not found\n');
  });

  it('forwards the WebSocket handshake from the page, and refuses a hostile one', async () => {
    const setup = await start();
    const channel = `${setup.origin}/api/channel?token=${encodeURIComponent(setup.agent.token)}`;
    expect(await handshake(channel, setup.origin)).toBe(101);
    expect(await handshake(channel, 'http://evil.example')).toBe(404);
    // The handshake is the one request with the token in its URL; neither
    // the proxy nor the agent logs a URL.
    expect(agentLog).not.toContain(setup.agent.token);
    expect(agentLog).toContain('the agent API is bound');
  });

  it('turns a token from an earlier run into the rejection, not into an absent agent', async () => {
    const first = await start();
    const stale = first.agent.token;
    await first.stop();
    running.splice(0);
    const second = await start();
    const answer = await get(`${second.origin}/api/health`, pageGet(stale));
    expect(answer.status).toBe(404);
    expect(answer.body).toBe('not found\n');
  });

  it('answers nothing at all once the agent is gone, so an absent agent stays a connection error', async () => {
    const setup = await start();
    await setup.agent.stop();
    await expect(
      get(`${setup.origin}/api/health`, pageGet(setup.agent.token)),
    ).rejects.toThrow();
    const ws = `${setup.origin}/api/channel?token=${encodeURIComponent(setup.agent.token)}`;
    await expect(handshake(ws, setup.origin)).rejects.toThrow();
  });
});
