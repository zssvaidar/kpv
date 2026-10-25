import { describe, expect, it } from 'vitest';

import {
  devOrigins,
  forwardedHeaders,
  forwardedOrigin,
  isAgentPath,
} from './agent-proxy.ts';
import { parseStartupLine } from './agent-process.ts';

const own = devOrigins(5173);
const agent = 'http://127.0.0.1:41234';
const host = '127.0.0.1:5173';

describe('forwardedOrigin', () => {
  it("presents the page's same-origin GET with the agent's origin", () => {
    expect(
      forwardedOrigin({ host, 'sec-fetch-site': 'same-origin' }, own, agent),
    ).toBe(agent);
    expect(
      forwardedOrigin(
        { host: 'localhost:5173', 'sec-fetch-site': 'same-origin' },
        own,
        agent,
      ),
    ).toBe(agent);
  });

  it("presents the dev server's own Origin with the agent's", () => {
    expect(
      forwardedOrigin({ host, origin: 'http://127.0.0.1:5173' }, own, agent),
    ).toBe(agent);
  });

  it('passes any other Origin through untouched, for the agent to reject', () => {
    expect(
      forwardedOrigin({ host, origin: 'http://evil.example' }, own, agent),
    ).toBe('http://evil.example');
    expect(
      forwardedOrigin({ host, origin: 'http://127.0.0.1:5174' }, own, agent),
    ).toBe('http://127.0.0.1:5174');
  });

  it('adds no Origin to a request a browser does not vouch for', () => {
    expect(forwardedOrigin({ host }, own, agent)).toBeUndefined();
    expect(
      forwardedOrigin({ host, 'sec-fetch-site': 'cross-site' }, own, agent),
    ).toBeUndefined();
    expect(
      forwardedOrigin({ host, 'sec-fetch-site': 'none' }, own, agent),
    ).toBeUndefined();
  });

  it('translates nothing for a Host that is not the dev server, which is DNS rebinding', () => {
    const rebound = 'evil.example:5173';
    expect(
      forwardedOrigin(
        { host: rebound, 'sec-fetch-site': 'same-origin' },
        own,
        agent,
      ),
    ).toBeUndefined();
    expect(
      forwardedOrigin(
        { host: rebound, origin: 'http://evil.example:5173' },
        own,
        agent,
      ),
    ).toBe('http://evil.example:5173');
  });
});

describe('forwardedHeaders', () => {
  it('points Host at the agent, keeps the token header, and drops hop-by-hop headers', () => {
    const out = forwardedHeaders(
      {
        host,
        authorization: 'Bearer t',
        connection: 'keep-alive',
        'sec-fetch-site': 'same-origin',
      },
      own,
      new URL(agent),
      false,
    );
    expect(out).toEqual({
      host: '127.0.0.1:41234',
      authorization: 'Bearer t',
      'sec-fetch-site': 'same-origin',
      origin: agent,
    });
  });

  it('keeps Connection and Upgrade on a handshake', () => {
    const out = forwardedHeaders(
      {
        host,
        origin: 'http://127.0.0.1:5173',
        connection: 'Upgrade',
        upgrade: 'websocket',
      },
      own,
      new URL(agent),
      true,
    );
    expect(out).toMatchObject({
      connection: 'Upgrade',
      upgrade: 'websocket',
      origin: agent,
    });
  });
});

describe('isAgentPath', () => {
  it('is /api and below, and nothing else', () => {
    for (const path of [
      '/api',
      '/api/health',
      '/api?x',
      '/api/channel?token=t',
    ]) {
      expect(isAgentPath(path), path).toBe(true);
    }
    for (const path of [
      '/',
      '/apix',
      '/src/api/client.ts',
      '/@vite/client',
      undefined,
    ]) {
      expect(isAgentPath(path), String(path)).toBe(false);
    }
  });
});

describe('parseStartupLine', () => {
  it('reads the origin and the token from the line the agent prints', () => {
    expect(
      parseStartupLine('open http://127.0.0.1:41234/?token=abc_-1'),
    ).toEqual({
      origin: 'http://127.0.0.1:41234',
      token: 'abc_-1',
    });
  });

  it('ignores every other line', () => {
    for (const line of [
      'kpv agent 0.0.0-dev',
      'listening on 127.0.0.1:41234',
      'open http://127.0.0.1:41234/',
      'open http://127.0.0.1:41234/?token=',
      'open not a url',
    ]) {
      expect(parseStartupLine(line), line).toBeUndefined();
    }
  });
});
