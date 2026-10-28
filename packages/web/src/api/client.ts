// The one typed client for the agent. Every call the page makes to the agent
// goes through here: ESLint refuses `fetch` and `WebSocket` anywhere else in
// this package.
//
// The token is given to `createAgentClient` once, held in its closure, and put
// on every request by the one function that sends anything. No method takes
// headers, a URL or an init object, and nothing that sends a request is
// exported, so there is no call site that could leave the token off.
//
// What an answer means
//
// The agent answers every failed check -- missing token, wrong token,
// disallowed origin -- with one uniform `404` and a fixed body, never `401`
// or `403` (#4). So the client tells "refusing us" from "not there" by the
// only thing that differs: whether a response arrived at all.
//
//   - a response: the agent is there. A `404` that is not the endpoint's own
//     typed error is a rejection, which the page shows as an authentication
//     problem.
//   - no response, the request itself failing: the agent is not there. In
//     development the proxy keeps this true by dropping the connection rather
//     than answering for an agent that is gone.
//
// A known consequence, not a bug to chase: the rejection is deliberately the
// same answer the agent gives a path it never served, so a request to a route
// this agent does not have -- a typo, or a route newer than the agent -- is
// reported here as an authentication problem too. The agent's design is that
// nothing can tell the two apart without already being authorized, and this
// client does not try: it keys on the status and on the endpoint's own error
// shape, never on the wording of either 404's body.

import type {
  AuthorizedRoot,
  DirectoryError,
  DirectoryListing,
  RootList,
  RootsError,
} from '@kpv/schema';
import {
  authorizedRootSchema,
  directoryErrorSchema,
  directoryListingSchema,
  rootListSchema,
  rootsErrorSchema,
} from '@kpv/schema';

/**
 * What the client needs of a generated Zod schema: to parse an answer. The
 * schemas are @kpv/schema's, generated from schema/ like the types are; this
 * names only the part of them used, so the package needs no Zod of its own.
 */
interface Parser<T> {
  safeParse(
    value: unknown,
  ): { success: true; data: T } | { success: false; error: unknown };
}

/** What any call to the agent comes back as. */
export type AgentResult<T, E = never> =
  | { kind: 'ok'; value: T }
  /** An answer arrived and it was the uniform rejection: an auth problem. */
  | { kind: 'rejected' }
  /** No answer arrived at all: the agent is not there. */
  | { kind: 'unreachable' }
  /** The endpoint answered with its own typed error. */
  | { kind: 'error'; status: number; error: E }
  /** An answer arrived that is none of the above. */
  | { kind: 'failed'; status: number };

/** The rejection the agent answers every failed check with (#4). */
export const rejectionStatus = 404;

/** The query parameter a WebSocket handshake carries the token in. */
const tokenParameter = 'token';

/** What the client needs from the page, so a test can supply its own. */
export interface ClientEnvironment {
  /** The page's origin; requests go to its `/api`. */
  origin: string;
  fetch: typeof fetch;
  WebSocket: new (url: string) => WebSocket;
}

export interface DirectoryQuery {
  /** An absolute path; the agent's default is the home directory. */
  path?: string;
  /** How many entries at most, 1 to 1000. */
  limit?: number;
}

/** The agent's API, as the page sees it. */
export interface AgentClient {
  /**
   * Whether the agent is there and accepting us: `GET /api/health`, with the
   * body ignored. Its answer is not described in schema/, so nothing of it is
   * read beyond the status.
   */
  probe(): Promise<AgentResult<null>>;
  /** One page of a directory: `GET /api/directories` (#7). */
  listDirectories(
    query?: DirectoryQuery,
  ): Promise<AgentResult<DirectoryListing, DirectoryError>>;
  /** The authorized project roots: `GET /api/roots` (#63). */
  listRoots(): Promise<AgentResult<RootList, RootsError>>;
  /**
   * Authorizes a directory as a project root: `POST /api/roots` (#63). The
   * answer is the real path that was authorized.
   */
  authorizeRoot(path: string): Promise<AgentResult<AuthorizedRoot, RootsError>>;
  /**
   * Revokes a project root: `DELETE /api/roots` (#63). The answer is the set
   * of roots left. Nothing on disk is touched.
   */
  revokeRoot(path: string): Promise<AgentResult<RootList, RootsError>>;
  /** Opens the push channel: `GET /api/channel` (#5), the token in the URL. */
  openChannel(): WebSocket;
}

/** The methods that send something; the body of each is a RootRequest. */
type Method = 'GET' | 'POST' | 'DELETE';

/** The environment of the page this runs in. */
export function browserEnvironment(): ClientEnvironment {
  return {
    origin: window.location.origin,
    fetch: window.fetch.bind(window),
    WebSocket: window.WebSocket,
  };
}

/** Creates the client. `token` is held here and nowhere else. */
export function createAgentClient(
  token: string,
  environment: ClientEnvironment,
): AgentClient {
  // The one function that sends a request. The token goes in a header rather
  // than the URL, so it is not in any URL a request was made to. A request
  // with a body is the roots routes' JSON RootRequest (#63), which the agent
  // takes only as application/json; the browser adds the page's Origin to a
  // POST or DELETE itself, which those routes require.
  async function send(
    path: string,
    method: Method = 'GET',
    body?: { path: string },
  ): Promise<Response | undefined> {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${token}`,
    };
    if (body !== undefined) {
      headers['Content-Type'] = 'application/json';
    }
    try {
      return await environment.fetch(new URL(path, environment.origin), {
        method,
        headers,
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
        credentials: 'omit',
        cache: 'no-store',
        redirect: 'error',
      });
    } catch {
      return undefined;
    }
  }

  async function call<T, E>(
    path: string,
    value: Parser<T>,
    error: Parser<E> | undefined,
    method: Method = 'GET',
    request?: { path: string },
  ): Promise<AgentResult<T, E>> {
    const answer = await send(path, method, request);
    if (answer === undefined) {
      return { kind: 'unreachable' };
    }
    const body = await readJSON(answer);
    if (answer.ok) {
      const parsed = value.safeParse(body);
      return parsed.success
        ? { kind: 'ok', value: parsed.data }
        : { kind: 'failed', status: answer.status };
    }
    const typed = error?.safeParse(body);
    if (typed?.success === true) {
      return { kind: 'error', status: answer.status, error: typed.data };
    }
    if (answer.status === rejectionStatus) {
      return { kind: 'rejected' };
    }
    return { kind: 'failed', status: answer.status };
  }

  return {
    async probe() {
      const answer = await send('/api/health');
      if (answer === undefined) {
        return { kind: 'unreachable' };
      }
      await answer.body?.cancel();
      if (answer.ok) {
        return { kind: 'ok', value: null };
      }
      return answer.status === rejectionStatus
        ? { kind: 'rejected' }
        : { kind: 'failed', status: answer.status };
    },

    listDirectories(query = {}) {
      const params = new URLSearchParams();
      if (query.path !== undefined) {
        params.set('path', query.path);
      }
      if (query.limit !== undefined) {
        params.set('limit', String(query.limit));
      }
      const search = params.size > 0 ? `?${params.toString()}` : '';
      return call(`/api/directories${search}`, listingParser, errorParser);
    },

    listRoots() {
      return call('/api/roots', rootListParser, rootsErrorParser);
    },

    authorizeRoot(path) {
      return call(
        '/api/roots',
        authorizedRootParser,
        rootsErrorParser,
        'POST',
        {
          path,
        },
      );
    },

    revokeRoot(path) {
      return call('/api/roots', rootListParser, rootsErrorParser, 'DELETE', {
        path,
      });
    },

    openChannel() {
      const url = new URL('/api/channel', environment.origin);
      url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
      // A browser's WebSocket cannot set a header, so the handshake is the
      // one request that carries the token in its URL (#5).
      url.searchParams.set(tokenParameter, token);
      return new environment.WebSocket(url.href);
    },
  };
}

// The listing's generated Zod schema parses to its own inferred type, which
// differs from the generated interface only in how an optional field is
// spelled under exactOptionalPropertyTypes (`parent?: string | undefined`
// against `parent?: string`), so it is asserted rather than assigned. Both are
// generated from schema/directory.schema.json, and a parse succeeds only on a
// value that schema accepts, which never has a key set to undefined. The
// error's schema has no optional field and is assigned unasserted. The roots
// answers are the same: a root's reason and a roots error's root are
// optional, so those two are asserted for the same reason, from
// schema/roots.schema.json.
const listingParser =
  directoryListingSchema as unknown as Parser<DirectoryListing>;
const errorParser: Parser<DirectoryError> = directoryErrorSchema;
const rootListParser = rootListSchema as unknown as Parser<RootList>;
const authorizedRootParser: Parser<AuthorizedRoot> = authorizedRootSchema;
const rootsErrorParser = rootsErrorSchema as unknown as Parser<RootsError>;

async function readJSON(answer: Response): Promise<unknown> {
  try {
    return (await answer.json()) as unknown;
  } catch {
    return undefined;
  }
}
