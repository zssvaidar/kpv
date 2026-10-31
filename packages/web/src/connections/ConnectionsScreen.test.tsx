// The connection screen rendered for its states (#24), to a string as the
// shell is.

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import type { ConnectionSummary } from '@kpv/schema';

import type { ConnectionsActions, ConnectionsState } from './controller.ts';
import { ConnectionsView, uncheckedWords } from './ConnectionsScreen.tsx';
import { draftFromSummary, emptyConnectionDraft } from './form.ts';

const nothing = () => undefined;
const actions: ConnectionsActions = {
  refresh: nothing,
  startCreate: nothing,
  startEdit: nothing,
  edit: nothing,
  toggleRemove: nothing,
  cancel: nothing,
  save: nothing,
  remove: nothing,
  confirm: nothing,
  cancelReport: nothing,
};

const base: ConnectionsState = {
  session: true,
  online: true,
  list: [],
  loading: false,
  problem: null,
  draft: null,
  saving: false,
  refusal: {},
  failure: null,
  report: null,
};

const stored: ConnectionSummary = {
  name: 'eu',
  bootstrapServers: ['broker:9093'],
  securityProtocol: 'SASL_SSL',
  saslMechanism: 'SCRAM-SHA-256',
  saslUsername: 'svc',
  saslPasswordSet: true,
  tlsClientKeySet: false,
};

const render = (state: Partial<ConnectionsState>) =>
  renderToStaticMarkup(
    <ConnectionsView
      state={{ ...base, ...state }}
      actions={actions}
      onClose={nothing}
    />,
  );

const inputOf = (html: string, name: string) =>
  new RegExp(`<(input|textarea|select)[^>]*name="${name}"[^>]*>`).exec(
    html,
  )?.[0] ?? '';

describe('the connection screen', () => {
  it('lists every connection by name with its protocol and mechanism', () => {
    const plain: ConnectionSummary = {
      ...stored,
      name: 'local',
      securityProtocol: 'PLAINTEXT',
      saslPasswordSet: false,
    };
    delete plain.saslMechanism;
    const html = render({ list: [stored, plain] });
    expect(html).toMatch(
      /<th scope="row">eu<\/th><td>SASL_SSL<\/td><td>SCRAM-SHA-256<\/td>/,
    );
    expect(html).toMatch(
      /<th scope="row">local<\/th><td>PLAINTEXT<\/td><td>—<\/td>/,
    );
  });

  it('says when there are none', () => {
    expect(render({ list: [] })).toContain('No connections yet.');
  });

  it('shows only the protocol choice until one is made, preselecting none', () => {
    const html = render({ draft: emptyConnectionDraft() });
    expect(html).toContain(
      '<option value="" selected="">Choose a protocol</option>',
    );
    expect(html).not.toContain('data-group="sasl"');
    expect(html).not.toContain('data-group="tls"');
  });

  it('shows the SASL fields only for SASL_* and the TLS fields only for SSL and SASL_SSL', () => {
    const groups = (
      protocol: 'PLAINTEXT' | 'SSL' | 'SASL_PLAINTEXT' | 'SASL_SSL',
    ) => {
      const html = render({
        draft: { ...emptyConnectionDraft(), securityProtocol: protocol },
      });
      return [
        html.includes('data-group="sasl"'),
        html.includes('data-group="tls"'),
      ];
    };
    expect(groups('PLAINTEXT')).toEqual([false, false]);
    expect(groups('SSL')).toEqual([false, true]);
    expect(groups('SASL_PLAINTEXT')).toEqual([true, false]);
    expect(groups('SASL_SSL')).toEqual([true, true]);
  });

  it('puts each error next to its field with aria-invalid and aria-describedby, and cannot save', () => {
    const html = render({
      draft: {
        ...emptyConnectionDraft(),
        name: 'my cluster',
        bootstrapServers: 'b:1',
        securityProtocol: 'SASL_SSL',
      },
    });
    const name = inputOf(html, 'name');
    expect(name).toContain('aria-invalid="true"');
    expect(name).toContain('aria-describedby="connection-name-error"');
    expect(html).toContain('id="connection-name-error"');
    expect(inputOf(html, 'saslMechanism')).toContain('aria-invalid="true"');
    expect(html).toMatch(
      /data-error-for="saslMechanism">SASL_SSL needs a SASL mechanism/,
    );
    expect(html).toMatch(/<button type="submit" disabled="">/);
  });

  it("shows the agent's refusal on the field it names", () => {
    const html = render({
      draft: {
        ...emptyConnectionDraft(),
        name: 'local',
        bootstrapServers: 'b:1',
        securityProtocol: 'PLAINTEXT',
      },
      refusal: { name: 'another connection already has this name' },
    });
    expect(html).toMatch(
      /data-error-for="name">another connection already has this name/,
    );
    expect(html).toMatch(/<button type="submit" disabled="">/);
  });

  it('never prefills a credential, says only set or not set, and asks the browser not to fill it', () => {
    const draft = { ...draftFromSummary(stored), saslPassword: '' };
    const html = render({ list: [stored], draft });
    const password = inputOf(html, 'saslPassword');
    expect(password).toContain('type="password"');
    expect(password.toLowerCase()).toContain('autocomplete="new-password"');
    expect(password).toContain('value=""');
    expect(inputOf(html, 'tlsClientKey').toLowerCase()).toContain(
      'autocomplete="new-password"',
    );
    expect(html).toContain('data-stored="set"');
    expect(html).toContain('data-stored="not set"');
    expect(html).toContain('Stored: set. Leave this empty to keep it.');
    expect(html).toContain('Remove the stored value');
  });

  it('says before saving what a protocol change drops', () => {
    const html = render({
      list: [stored],
      draft: { ...draftFromSummary(stored), securityProtocol: 'SSL' },
    });
    expect(html).toContain(
      'Saving with SSL removes the SASL mechanism, the SASL username, the stored SASL password.',
    );
    expect(html).toContain('typing them again');
  });

  it('shows the report of a referenced delete and asks before confirming', () => {
    const html = render({
      list: [stored],
      report: {
        action: 'delete',
        name: 'eu',
        retype: [],
        error: {
          error: 'referenced',
          message: 'm',
          references: [
            {
              root: '/home/u/code',
              directory: 'orders',
              pipelineId: 'orders',
              block: 'source',
            },
          ],
          unchecked: [
            { root: '/home/u/code', directory: 'broken', reason: 'parse' },
            { root: '/home/u/gone', reason: 'rootUnavailable' },
          ],
        },
      },
    });
    expect(html).toContain('role="alertdialog"');
    expect(html).toContain('Delete eu?');
    expect(html).toContain('orders (/home/u/code/orders), its source');
    expect(html).toContain(`/home/u/code/broken: ${uncheckedWords.parse}`);
    expect(html).toContain(`/home/u/gone: ${uncheckedWords.rootUnavailable}`);
    expect(html).toContain('Delete anyway');
    expect(html).toContain('Keep it');
  });

  it('asks for a cleared credential again before confirming a rename', () => {
    const html = render({
      list: [stored],
      draft: draftFromSummary(stored),
      report: {
        action: 'rename',
        name: 'eu',
        retype: ['saslPassword'],
        error: {
          error: 'referenced',
          message: 'm',
          references: [],
          unchecked: [{ root: '/r', reason: 'truncated' }],
        },
      },
    });
    expect(html).toContain('Rename eu?');
    expect(html).toContain('SASL password was cleared');
  });
});
