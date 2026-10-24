import { describe, expect, it } from 'vitest';

import { describeWorkspace } from './index.js';

describe('describeWorkspace', () => {
  it('names the schema package it is wired to', () => {
    expect(describeWorkspace()).toBe('kpv web is wired to @kpv/schema');
  });
});
