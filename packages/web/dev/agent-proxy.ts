// The development proxy: `/api` on the Vite dev server, forwarded to the agent.
//
// Why it exists (#8): the agent answers no CORS and no preflight, by design
// (#4), so the page has to reach it same-origin. In production the agent
// serves the page itself (#55); in development the page comes from Vite, so
// Vite forwards `/api` and the browser only ever talks to one origin. A
// same-origin request is never preflighted and needs no CORS header.
//
// It is a small hand-written proxy rather than Vite's `server.proxy`, because
// of two things the built-in one does that this one must not:
//
//   - with the agent gone, it answers `502`. An HTTP answer is exactly what
//     the page reads as "the agent is there", so here the browser's connection
//     is destroyed instead, and an absent agent stays a connection error, the
//     same thing the page sees in production when nothing is listening.
//   - it logs the URL of a failed request. The WebSocket handshake carries the
//     session token in its query string, so nothing here logs a URL.
//
// The one thing it changes on the way through is the `Origin` header, by
// translating rather than by forging. The agent requires an allowed `Origin`
// on everything but the narrow #58 exception, and a browser sends none on a
// same-origin `GET`, so the page's own requests would be rejected as they
// stand. They are also, as far as the agent can tell, from the wrong origin:
// the dev server's, which the agent does not allow and is not asked to.
//
// So a request that is same-origin to the dev server is presented to the agent
// as same-origin to the agent, and anything else is passed through exactly as
// it came, for the agent to judge by its own unchanged rules:
//
//   - `Origin` is the dev server's own origin, and so is `Host`: the page's
//     WebSocket handshake, or any non-`GET` it makes. Presented with the
//     agent's origin.
//   - no `Origin`, `Sec-Fetch-Site: same-origin`, and `Host` is the dev
//     server: the page's own `GET`. Presented with the agent's origin. A page
//     cannot set `Sec-Fetch-Site`, it is a forbidden header name.
//   - anything else: forwarded untouched. A hostile page's `Origin` reaches
//     the agent as the hostile origin and is rejected there; no `Origin` and no
//     fetch metadata reaches it as nothing and is rejected there.
//
// `Host` is part of the test because of DNS rebinding: a hostile name that
// resolves to 127.0.0.1 makes a hostile page "same-origin" to this socket, but
// its `Host` is the hostile name, not one of the dev server's own.
//
// None of this touches the token. It is never added, read or checked here: a
// request without it is forwarded without it and the agent rejects it.

import type {
  IncomingHttpHeaders,
  IncomingMessage,
  Server,
  ServerResponse,
} from 'node:http';
import { request as httpRequest } from 'node:http';
import { connect } from 'node:net';
import type { Duplex } from 'node:stream';

import type { Plugin, ViteDevServer } from 'vite';

/** The path prefix the agent serves and the dev server forwards. */
export const apiPrefix = '/api';

/** Whether a request path belongs to the agent. */
export function isAgentPath(url: string | undefined): boolean {
  if (url === undefined) {
    return false;
  }
  return (
    url === apiPrefix ||
    url.startsWith(`${apiPrefix}/`) ||
    url.startsWith(`${apiPrefix}?`)
  );
}

/** The dev server's own origins for a port: the two loopback names. */
export function devOrigins(port: number): string[] {
  return [`http://127.0.0.1:${port}`, `http://localhost:${port}`];
}

function single(value: string | string[] | undefined): string | undefined {
  if (Array.isArray(value)) {
    return value.length === 1 ? value[0] : undefined;
  }
  return value;
}

/**
 * The `Origin` the agent should see for a request that reached the dev
 * server, or `undefined` to send none. See the comment at the top of this file
 * for the rule; this function is all of it.
 */
export function forwardedOrigin(
  headers: IncomingHttpHeaders,
  ownOrigins: readonly string[],
  agentOrigin: string,
): string | undefined {
  const origin = headers.origin;
  const host = single(headers.host)?.toLowerCase();
  const ownHost =
    host !== undefined && ownOrigins.some((own) => own === `http://${host}`);
  if (origin === undefined) {
    const site = single(headers['sec-fetch-site']);
    return ownHost && site === 'same-origin' ? agentOrigin : undefined;
  }
  return ownHost && ownOrigins.includes(origin.toLowerCase())
    ? agentOrigin
    : origin;
}

// Hop-by-hop headers belong to one connection and are not forwarded on an
// ordinary request. An upgrade keeps Connection and Upgrade, which are what
// make it one.
const hopByHop = new Set([
  'connection',
  'keep-alive',
  'proxy-connection',
  'transfer-encoding',
  'upgrade',
  'te',
  'trailer',
]);

/** The headers to send the agent for a request, with Origin and Host settled. */
export function forwardedHeaders(
  headers: IncomingHttpHeaders,
  ownOrigins: readonly string[],
  agent: URL,
  upgrade: boolean,
): Record<string, string | string[]> {
  const out: Record<string, string | string[]> = {};
  for (const [name, value] of Object.entries(headers)) {
    if (value === undefined || name === 'origin' || name === 'host') {
      continue;
    }
    if (!upgrade && hopByHop.has(name)) {
      continue;
    }
    out[name] = value;
  }
  out.host = agent.host;
  const origin = forwardedOrigin(headers, ownOrigins, agent.origin);
  if (origin !== undefined) {
    out.origin = origin;
  }
  return out;
}

function ownOriginsOf(server: Server | null): string[] {
  const address = server?.address();
  if (
    address === null ||
    address === undefined ||
    typeof address === 'string'
  ) {
    return [];
  }
  return devOrigins(address.port);
}

function forwardRequest(
  agent: URL,
  ownOrigins: readonly string[],
  req: IncomingMessage,
  res: ServerResponse,
): void {
  const upstream = httpRequest({
    host: agent.hostname,
    port: agent.port,
    method: req.method ?? 'GET',
    path: req.url ?? '/',
    headers: forwardedHeaders(req.headers, ownOrigins, agent, false),
  });
  upstream.on('response', (answer) => {
    res.writeHead(answer.statusCode ?? 502, answer.headers);
    answer.pipe(res);
    answer.on('error', () => res.destroy());
  });
  // No answer at all: the agent is not there, or it hung up. The browser gets
  // no answer either, so "not there" stays a connection error.
  upstream.on('error', () => res.destroy());
  req.on('error', () => upstream.destroy());
  res.on('close', () => {
    if (!res.writableFinished) {
      upstream.destroy();
    }
  });
  req.pipe(upstream);
}

function forwardUpgrade(
  agent: URL,
  ownOrigins: readonly string[],
  req: IncomingMessage,
  socket: Duplex,
  head: Buffer,
): void {
  const upstream = connect(Number(agent.port), agent.hostname);
  const close = () => {
    socket.destroy();
    upstream.destroy();
  };
  upstream.on('error', close);
  socket.on('error', close);
  upstream.on('close', close);
  socket.on('close', close);
  upstream.on('connect', () => {
    const headers = forwardedHeaders(req.headers, ownOrigins, agent, true);
    let preamble = `${req.method ?? 'GET'} ${req.url ?? '/'} HTTP/1.1\r\n`;
    for (const [name, value] of Object.entries(headers)) {
      for (const one of Array.isArray(value) ? value : [value]) {
        preamble += `${name}: ${one}\r\n`;
      }
    }
    upstream.write(`${preamble}\r\n`);
    if (head.length > 0) {
      upstream.write(head);
    }
    upstream.pipe(socket);
    socket.pipe(upstream);
  });
}

/**
 * The Vite plugin that forwards `/api` to the agent at `agentOrigin`, the
 * origin the agent printed at startup, so the target is whatever port this
 * run of the agent was given.
 */
export function agentProxy(agentOrigin: string): Plugin {
  const agent = new URL(agentOrigin);
  return {
    name: 'kpv-agent-proxy',
    apply: 'serve',
    configureServer(server: ViteDevServer) {
      const httpServer = server.httpServer as Server | null;
      // Registered directly rather than returned, so it runs ahead of Vite's
      // own middlewares and nothing of Vite's answers for `/api`.
      server.middlewares.use((req, res, next) => {
        if (!isAgentPath(req.url)) {
          next();
          return;
        }
        forwardRequest(agent, ownOriginsOf(httpServer), req, res);
      });
      httpServer?.on(
        'upgrade',
        (req: IncomingMessage, socket: Duplex, head: Buffer) => {
          if (isAgentPath(req.url)) {
            forwardUpgrade(agent, ownOriginsOf(httpServer), req, socket, head);
          }
        },
      );
    },
  };
}
