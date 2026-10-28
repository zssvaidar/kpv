// A stand-in agent for the picker's tests: the real typed client with an
// injected fetch, as #8's tests do, so every answer goes through the same
// parsing and the same rejection rules the page uses. Each request is held
// until the test answers it, which is how ordering and races are tested.

import { createAgentClient, type AgentClient } from '../api/client.ts';

export interface HeldRequest {
  method: string;
  /** The path, without the query. */
  path: string;
  /** The `path` query parameter, `null` when there was none. */
  queryPath: string | null;
  /** The JSON body, parsed, when there was one. */
  body: unknown;
  answer(response: Response): void;
  /** No answer at all: what the page sees when the agent is gone. */
  drop(): void;
}

export const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json; charset=utf-8' },
  });

/** The agent's uniform rejection (#4). */
export const rejection = () =>
  new Response('not found\n', {
    status: 404,
    headers: { 'Content-Type': 'text/plain; charset=utf-8' },
  });

export interface FakeAgent {
  client: AgentClient;
  /** Every request sent, answered or not, in order. */
  sent: HeldRequest[];
  /** The requests not answered yet. */
  pending(): HeldRequest[];
  /** The oldest unanswered request matching, or a thrown error. */
  next(method: string, path: string): HeldRequest;
  /** Lets every answered request's promise chain run. */
  settle(): Promise<void>;
}

export function fakeAgent(): FakeAgent {
  const sent: HeldRequest[] = [];
  const open = new Set<HeldRequest>();
  const client = createAgentClient('test-token', {
    origin: 'http://127.0.0.1:5173',
    fetch: (input, init) =>
      new Promise<Response>((resolve, reject) => {
        const url = new URL(
          input instanceof Request ? input.url : String(input),
        );
        const text = typeof init?.body === 'string' ? init.body : undefined;
        const request: HeldRequest = {
          method: init?.method ?? 'GET',
          path: url.pathname,
          queryPath: url.searchParams.get('path'),
          body: text === undefined ? undefined : (JSON.parse(text) as unknown),
          answer(response) {
            open.delete(request);
            resolve(response);
          },
          drop() {
            open.delete(request);
            reject(new TypeError('Failed to fetch'));
          },
        };
        sent.push(request);
        open.add(request);
      }),
    WebSocket: class {
      constructor() {
        throw new Error('the picker opens no channel');
      }
    } as unknown as new (url: string) => WebSocket,
  });
  return {
    client,
    sent,
    pending: () => [...open],
    next(method, path) {
      const found = [...open].find(
        (request) => request.method === method && request.path === path,
      );
      if (found === undefined) {
        throw new Error(`no pending ${method} ${path}`);
      }
      return found;
    },
    async settle() {
      for (let i = 0; i < 10; i++) {
        await new Promise((resolve) => setTimeout(resolve, 0));
      }
    },
  };
}
