// What the page says about each connection state. One function, so every
// state has words and a test can hold the states apart.

import type { ConnectionState } from './monitor.ts';

export type Tone = 'ok' | 'pending' | 'auth' | 'down';

export interface ConnectionDescription {
  /** The short label in the status bar. */
  label: string;
  /** Whether the page has a working connection at all. */
  tone: Tone;
  /** A sentence or two for the person looking at it; empty when connected. */
  detail: string;
}

/** The way back from an authentication problem, which is always the same. */
export const reopenAdvice =
  'Re-open the URL the agent printed when it started (in development, the ' +
  '"open" URL that pnpm dev printed). This page does not keep the session ' +
  'token anywhere a reload could find it, so a reload cannot recover it.';

function seconds(ms: number): string {
  const s = Math.max(1, Math.round(ms / 1000));
  return s === 1 ? '1 second' : `${s} seconds`;
}

export function describeConnection(
  state: ConnectionState,
): ConnectionDescription {
  switch (state.status) {
    case 'connected':
      return { label: 'Connected', tone: 'ok', detail: '' };
    case 'connecting':
      return {
        label: 'Connecting',
        tone: 'pending',
        detail:
          state.retryInMs === undefined
            ? 'Connecting to the agent.'
            : `The agent is up but the channel to it dropped. Reconnecting in ${seconds(state.retryInMs)}.`,
      };
    case 'unauthenticated':
      return {
        label: 'Not authorized',
        tone: 'auth',
        detail:
          state.reason === 'missing-token'
            ? `This page has no session token, which is what a reload does once the token has been taken out of the address bar. ${reopenAdvice}`
            : `The agent answered and refused this page: the session token it holds is not the one the agent is running with, which is what happens after the agent restarts. ${reopenAdvice}`,
      };
    case 'unreachable':
      return {
        label: 'Agent not reachable',
        tone: 'down',
        detail: `Nothing answered: the agent is not running, or not where it was. Retrying in ${seconds(state.retryInMs)}.`,
      };
    case 'failed':
      return {
        label: 'Agent error',
        tone: 'down',
        detail: `The agent answered with HTTP ${state.httpStatus}, which this page does not understand. Retrying in ${seconds(state.retryInMs)}.`,
      };
  }
}
