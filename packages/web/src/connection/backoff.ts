// How long to wait before reconnecting, after one failure and after many.
//
// Exponential from `initialMs`, doubling, and never more than `maxMs`: the
// bound is what keeps a page left open on a stopped agent from drifting into
// waits so long that restarting the agent appears to change nothing. Each
// delay is jittered down into the upper half of its step, so that tabs
// reconnecting to one agent do not do it in lockstep; jitter only ever
// shortens a delay, so it cannot push one past the bound.

export interface BackoffOptions {
  /** The first delay. */
  initialMs: number;
  /** No delay is ever longer than this. */
  maxMs: number;
  /** A number in [0, 1); `Math.random` by default. */
  random?: () => number;
}

export const defaultBackoff: BackoffOptions = {
  initialMs: 250,
  maxMs: 10_000,
};

export interface Backoff {
  /** The delay before the next attempt, and counts that attempt. */
  next(): number;
  /** Back to the first delay, after a connection succeeded. */
  reset(): void;
}

export function createBackoff(
  options: BackoffOptions = defaultBackoff,
): Backoff {
  const random = options.random ?? Math.random;
  let attempt = 0;
  return {
    next() {
      // Capped before it is computed, so a long outage cannot overflow it.
      const step = Math.min(
        options.maxMs,
        options.initialMs * 2 ** Math.min(attempt, 30),
      );
      attempt += 1;
      return Math.round(step / 2 + (step / 2) * random());
    },
    reset() {
      attempt = 0;
    },
  };
}
