// The connection form's model (#24), without rendering anything.

import { describe, expect, it } from 'vitest';

import type { ConnectionSummary } from '@kpv/schema';

import {
  connectionRequest,
  credentialState,
  draftFromSummary,
  draftIssues,
  droppedOnSave,
  emptyConnectionDraft,
  refusalIssues,
  visibleGroups,
  withoutTypedCredentials,
  type ConnectionDraft,
} from './form.ts';
import formSource from './form.ts?raw';
import screenSource from './ConnectionsScreen.tsx?raw';

const stored: ConnectionSummary = {
  name: 'eu',
  bootstrapServers: ['broker-1:9093', 'broker-2:9093'],
  securityProtocol: 'SASL_SSL',
  saslMechanism: 'SCRAM-SHA-512',
  saslUsername: 'svc',
  saslPasswordSet: true,
  tlsCaCertificate:
    '-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n',
  tlsClientKeySet: false,
};

const typed = (change: Partial<ConnectionDraft>): ConnectionDraft => ({
  ...emptyConnectionDraft(),
  name: 'local',
  bootstrapServers: 'localhost:9092',
  securityProtocol: 'PLAINTEXT',
  ...change,
});

describe('a new connection', () => {
  it('preselects no protocol and cannot be saved empty', () => {
    const draft = emptyConnectionDraft();
    expect(draft.securityProtocol).toBe('');
    expect(connectionRequest(draft)).toBeNull();
    expect(Object.keys(draftIssues(draft))).toEqual(
      expect.arrayContaining(['name', 'bootstrapServers', 'securityProtocol']),
    );
  });

  it('shows only the fields the protocol allows', () => {
    expect(visibleGroups(typed({ securityProtocol: 'PLAINTEXT' }))).toEqual({
      sasl: false,
      tls: false,
    });
    expect(visibleGroups(typed({ securityProtocol: 'SSL' }))).toEqual({
      sasl: false,
      tls: true,
    });
    expect(
      visibleGroups(typed({ securityProtocol: 'SASL_PLAINTEXT' })),
    ).toEqual({ sasl: true, tls: false });
    expect(visibleGroups(typed({ securityProtocol: 'SASL_SSL' }))).toEqual({
      sasl: true,
      tls: true,
    });
  });

  it('checks the combination rules as it is typed, at the field', () => {
    const issues = draftIssues(typed({ securityProtocol: 'SASL_SSL' }));
    expect(issues.saslMechanism).toContain('SASL_SSL');
    expect(issues.saslUsername).toBeDefined();
    expect(issues.saslPassword).toBeDefined();
    const pair = draftIssues(
      typed({ securityProtocol: 'SSL', tlsClientCertificate: 'pem' }),
    );
    expect(pair.tlsClientKey).toBeDefined();
  });

  it('checks each bootstrap server and refuses a duplicate', () => {
    expect(
      draftIssues(typed({ bootstrapServers: 'broker:0' })).bootstrapServers,
    ).toBeDefined();
    expect(
      draftIssues(typed({ bootstrapServers: 'a:1\nA:1' })).bootstrapServers,
    ).toBeDefined();
    expect(
      draftIssues(typed({ bootstrapServers: '[::1]:9092\nb:65535' })),
    ).toEqual({});
  });

  it('sends only the fields the protocol shows, and the typed credentials', () => {
    const draft = typed({
      securityProtocol: 'SASL_PLAINTEXT',
      saslMechanism: 'PLAIN',
      saslUsername: 'u',
      saslPassword: 'typed-password',
      tlsCaCertificate: 'left over from SSL',
    });
    expect(connectionRequest(draft)).toEqual({
      kind: 'create',
      request: {
        name: 'local',
        bootstrapServers: ['localhost:9092'],
        securityProtocol: 'SASL_PLAINTEXT',
        saslMechanism: 'PLAIN',
        saslUsername: 'u',
        saslPassword: 'typed-password',
      },
    });
  });
});

describe('editing a stored connection', () => {
  it('never prefills a credential and keeps it when left empty', () => {
    const draft = draftFromSummary(stored);
    expect(draft.saslPassword).toBe('');
    expect(draft.tlsClientKey).toBe('');
    expect(credentialState(draft, 'saslPassword')).toBe('kept');
    expect(draftIssues(draft)).toEqual({});
    const built = connectionRequest(draft);
    expect(built?.kind).toBe('update');
    expect(built?.request).not.toHaveProperty('saslPassword');
    expect(built?.request).toMatchObject({ current: 'eu', name: 'eu' });
    expect(built?.request).not.toHaveProperty('remove');
  });

  it('replaces a credential only when one is typed, and removes it only when asked', () => {
    const replaced = { ...draftFromSummary(stored), saslPassword: 'new' };
    expect(connectionRequest(replaced)?.request).toMatchObject({
      saslPassword: 'new',
    });
    const removed = {
      ...draftFromSummary(stored),
      remove: ['saslPassword' as const],
    };
    expect(credentialState(removed, 'saslPassword')).toBe('removed');
    // SASL needs a password, so removing it is refused at the field.
    expect(draftIssues(removed).saslPassword).toBeDefined();
    expect(connectionRequest(removed)).toBeNull();
  });

  it('says what a protocol change drops before saving', () => {
    const plain = {
      ...draftFromSummary(stored),
      securityProtocol: 'PLAINTEXT' as const,
    };
    expect(droppedOnSave(plain)).toEqual([
      'the SASL mechanism',
      'the SASL username',
      'the stored SASL password',
      'the CA certificate',
    ]);
    const request = connectionRequest(plain)?.request;
    expect(request).toEqual({
      current: 'eu',
      name: 'eu',
      bootstrapServers: ['broker-1:9093', 'broker-2:9093'],
      securityProtocol: 'PLAINTEXT',
    });
    expect(droppedOnSave(draftFromSummary(stored))).toEqual([]);
  });

  it('renames with the credentials kept, and confirms when asked to', () => {
    const renamed = { ...draftFromSummary(stored), name: 'eu-west' };
    expect(connectionRequest(renamed, true)?.request).toMatchObject({
      current: 'eu',
      name: 'eu-west',
      confirm: true,
    });
  });

  it('clears the typed credentials after a send and keeps the rest', () => {
    const draft = {
      ...draftFromSummary(stored),
      saslPassword: 'typed',
      name: 'x',
    };
    expect(withoutTypedCredentials(draft)).toEqual({
      ...draft,
      saslPassword: '',
      tlsClientKey: '',
    });
  });
});

describe("the agent's refusal", () => {
  it('lands on the fields it names', () => {
    expect(
      refusalIssues({
        error: 'invalid',
        message: 'm',
        problems: [{ pointer: '/tlsClientKey', message: 'not a key' }],
      }),
    ).toEqual({ tlsClientKey: 'not a key' });
    expect(refusalIssues({ error: 'duplicateName', message: 'taken' })).toEqual(
      { name: 'taken' },
    );
  });
});

describe('the source', () => {
  it('restates no rule: protocols are asked of @kpv/schema', () => {
    for (const source of [formSource, screenSource]) {
      expect(source).not.toMatch(
        /=== 'SASL_SSL'|=== 'SSL'|=== 'SASL_PLAINTEXT'/,
      );
    }
  });

  it('prefills no credential input and asks the browser not to fill one', () => {
    expect(screenSource.match(/autoComplete="new-password"/g)).toHaveLength(2);
    expect(screenSource).not.toMatch(/defaultValue/);
  });
});

describe('the words', () => {
  it('say a refused pattern in words, not as a regular expression', () => {
    const issues = draftIssues(
      typed({ name: 'my cluster', bootstrapServers: 'broker' }),
    );
    expect(issues.name).toContain('letter or digit');
    expect(issues.bootstrapServers).toContain('host:port');
    expect(JSON.stringify(issues)).not.toContain('^');
  });
});
