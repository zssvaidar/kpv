import { describe, expect, it } from 'vitest';

import {
  createFields,
  createRequest,
  emptyDraft,
  fieldProblems,
  type CreateDraft,
} from './create-form.ts';

const filled: CreateDraft = {
  root: '/home/user/code',
  name: 'Orders to billing',
  language: 'python',
  sourceTopic: 'orders.v1',
  destinationTopic: 'billing.v1',
  networkMode: 'docker',
};
const roots = ['/home/user/code'];

describe('the create form', () => {
  it('preselects nothing the schema gives no default, so a new draft cannot be sent', () => {
    const draft = emptyDraft('/home/user/code');
    expect(draft.language).toBe('');
    expect(draft.networkMode).toBe('');
    expect(draft.sourceTopic).toBe('');
    expect(draft.destinationTopic).toBe('');
    const problems = fieldProblems(draft);
    for (const field of createFields) {
      expect(problems[field], field).not.toBeNull();
    }
    expect(problems.language).toBe('Choose one.');
    expect(problems.networkMode).toBe('Choose one.');
    expect(createRequest(draft, roots)).toBeNull();
  });

  it('accepts a filled draft and sends exactly its fields', () => {
    expect(Object.values(fieldProblems(filled))).toEqual(
      createFields.map(() => null),
    );
    expect(createRequest(filled, roots)).toEqual(filled);
  });

  it.each([
    ['name', '   '],
    ['name', 'x'.repeat(257)],
    ['language', 'cobol'],
    ['sourceTopic', 'orders v1'],
    ['destinationTopic', 'billing/v1'],
    ['networkMode', 'bridge'],
  ] as const)(
    'checks %s with the generated schema as it is typed: %j',
    (field, value) => {
      const draft = { ...filled, [field]: value };
      const problems = fieldProblems(draft);
      expect(problems[field]).not.toBeNull();
      for (const other of createFields.filter((f) => f !== field)) {
        expect(problems[other], other).toBeNull();
      }
      expect(createRequest(draft, roots)).toBeNull();
    },
  );

  it('takes a name in any script, since the agent derives the id', () => {
    expect(
      fieldProblems({ ...filled, name: 'Заказы → счета' }).name,
    ).toBeNull();
  });

  it('cannot be sent to a root that is not offered', () => {
    expect(createRequest({ ...filled, root: '' }, roots)).toBeNull();
    expect(createRequest({ ...filled, root: '/etc' }, roots)).toBeNull();
  });
});
