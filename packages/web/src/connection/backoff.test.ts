import { describe, expect, it } from 'vitest';

import { createBackoff, defaultBackoff } from './backoff.ts';

describe('createBackoff', () => {
  it('doubles from the first delay', () => {
    const backoff = createBackoff({
      initialMs: 100,
      maxMs: 10_000,
      random: () => 0.999999,
    });
    expect([1, 2, 3, 4].map(() => backoff.next())).toEqual([
      100, 200, 400, 800,
    ]);
  });

  it('never waits longer than the bound, however long the outage', () => {
    for (const random of [() => 0, () => 0.5, () => 0.999999]) {
      const backoff = createBackoff({ ...defaultBackoff, random });
      for (let attempt = 0; attempt < 10_000; attempt += 1) {
        const delay = backoff.next();
        expect(delay).toBeGreaterThan(0);
        expect(delay).toBeLessThanOrEqual(defaultBackoff.maxMs);
        expect(Number.isFinite(delay)).toBe(true);
      }
    }
  });

  it('reaches the bound and stays there', () => {
    const backoff = createBackoff({
      initialMs: 250,
      maxMs: 10_000,
      random: () => 0.999999,
    });
    const delays = Array.from({ length: 20 }, () => backoff.next());
    expect(delays.slice(-5)).toEqual([10_000, 10_000, 10_000, 10_000, 10_000]);
  });

  it('jitters down into the upper half of a step, never up', () => {
    const low = createBackoff({
      initialMs: 1000,
      maxMs: 1000,
      random: () => 0,
    });
    const high = createBackoff({
      initialMs: 1000,
      maxMs: 1000,
      random: () => 0.999999,
    });
    expect(low.next()).toBe(500);
    expect(high.next()).toBe(1000);
  });

  it('starts over after a reset', () => {
    const backoff = createBackoff({
      initialMs: 100,
      maxMs: 10_000,
      random: () => 0.999999,
    });
    backoff.next();
    backoff.next();
    backoff.reset();
    expect(backoff.next()).toBe(100);
  });
});
