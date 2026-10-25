import { describe, expect, it } from 'vitest';

import { describeConnection, reopenAdvice } from './describe.ts';
import type { ConnectionState } from './monitor.ts';

const every: ConnectionState[] = [
  { status: 'connecting' },
  { status: 'connecting', retryInMs: 2000 },
  { status: 'connected' },
  { status: 'unauthenticated', reason: 'missing-token' },
  { status: 'unauthenticated', reason: 'rejected' },
  { status: 'unreachable', retryInMs: 2000 },
  { status: 'failed', httpStatus: 500, retryInMs: 2000 },
];

describe('describeConnection', () => {
  it('says something about every state but connected', () => {
    for (const state of every) {
      const description = describeConnection(state);
      expect(description.label).not.toBe('');
      expect(description.detail === '').toBe(state.status === 'connected');
    }
  });

  it('shows a refusing agent and an absent agent differently', () => {
    const refused = describeConnection({
      status: 'unauthenticated',
      reason: 'rejected',
    });
    const absent = describeConnection({
      status: 'unreachable',
      retryInMs: 1000,
    });
    expect(refused.tone).toBe('auth');
    expect(absent.tone).toBe('down');
    expect(refused.label).not.toBe(absent.label);
  });

  it('shows a page with no token as the same authentication problem, with the way back', () => {
    const missing = describeConnection({
      status: 'unauthenticated',
      reason: 'missing-token',
    });
    const refused = describeConnection({
      status: 'unauthenticated',
      reason: 'rejected',
    });
    expect(missing.tone).toBe('auth');
    expect(missing.label).toBe(refused.label);
    expect(missing.detail).toContain(reopenAdvice);
    expect(refused.detail).toContain(reopenAdvice);
  });

  it('says when it will try again', () => {
    expect(
      describeConnection({ status: 'unreachable', retryInMs: 4000 }).detail,
    ).toContain('4 seconds');
  });
});
