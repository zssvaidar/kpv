// Starting the agent for development, and learning where this run of it is.
//
// The agent binds an ephemeral port per run (#4) and says which on stdout, so
// the address cannot be configuration: it is read from the startup lines of
// the process this starts. The same lines carry the session token, in the URL
// they say to open, which is how the dev page gets it from a clean clone with
// nothing committed.

import { spawn, type ChildProcess } from 'node:child_process';
import { execFile } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

/** The agent's Go module, from this file. */
export const agentModule = fileURLToPath(
  new URL('../../../agent/', import.meta.url),
);

/** What the startup URL says: where the agent is, and the token it minted. */
export interface AgentAddress {
  /** The agent's own origin, for example `http://127.0.0.1:41234`. */
  origin: string;
  /** The session token for this run of the agent. */
  token: string;
}

/**
 * Reads one startup line. The agent prints `open <url>` with the token in the
 * `token` query parameter; any other line is not the one this wants.
 */
export function parseStartupLine(line: string): AgentAddress | undefined {
  const match = /^open (\S+)$/.exec(line.trim());
  if (match?.[1] === undefined) {
    return undefined;
  }
  let url: URL;
  try {
    url = new URL(match[1]);
  } catch {
    return undefined;
  }
  const token = url.searchParams.get('token');
  if (url.protocol !== 'http:' || token === null || token === '') {
    return undefined;
  }
  return { origin: url.origin, token };
}

/** A running agent this started. */
export interface RunningAgent extends AgentAddress {
  /** The agent's startup lines other than the one carrying the token. */
  banner: string[];
  /** Stops the agent and removes the binary built for it. */
  stop(): Promise<void>;
  /** Settles when the agent exits, for whatever reason. */
  exited: Promise<number | null>;
}

/**
 * Builds the agent into a temporary directory and starts it.
 *
 * It is built and then run rather than `go run`, so that the process stopped
 * is the agent itself and not a `go` command standing in front of it.
 * `stderr` is the agent's log and goes to `log`; `stdout` is read for the
 * startup lines and nothing else of it is passed on, because one of them is a
 * URL carrying the token that points at the agent rather than at the page.
 */
export async function startAgent(
  log: (chunk: Buffer) => void = (chunk) => process.stderr.write(chunk),
): Promise<RunningAgent> {
  const dir = await mkdtemp(join(tmpdir(), 'kpv-agent-'));
  const binary = join(dir, 'kpv-agent');
  try {
    await promisify(execFile)('go', ['build', '-o', binary, './cmd/agent'], {
      cwd: agentModule,
    });
  } catch (err) {
    await rm(dir, { recursive: true, force: true });
    throw err;
  }

  const child: ChildProcess = spawn(binary, [], {
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  child.stderr?.on('data', log);
  const exited = new Promise<number | null>((resolve) => {
    child.on('exit', (code) => resolve(code));
    child.on('error', () => resolve(null));
  });
  const stop = async () => {
    if (child.exitCode === null && child.signalCode === null) {
      child.kill('SIGTERM');
    }
    await exited;
    await rm(dir, { recursive: true, force: true });
  };

  const banner: string[] = [];
  const lines = createInterface({ input: child.stdout! });
  try {
    const address = await new Promise<AgentAddress>((resolve, reject) => {
      lines.on('line', (line) => {
        const parsed = parseStartupLine(line);
        if (parsed === undefined) {
          banner.push(line);
          return;
        }
        resolve(parsed);
      });
      void exited.then((code) =>
        reject(new Error(`the agent exited (${code}) before it was ready`)),
      );
    });
    return { ...address, banner, stop, exited };
  } catch (err) {
    await stop();
    throw err;
  }
}
