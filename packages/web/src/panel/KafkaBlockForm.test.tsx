// The Kafka configuration form rendered (#12): what it shows for a block, an
// error next to the field it is about, the connection chosen by name from a
// select (and a name that no longer exists shown as it is), and nothing that
// takes a credential.

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import type { Pipeline } from '@kpv/schema';

import { graphFixtures } from '../graph/fixtures.ts';
import type { ConnectionNamesState } from './connection-names.ts';
import { Shell } from '../shell/App.tsx';
import type { KafkaDraft, KafkaRole } from './kafka-form.ts';
import { draftFor } from './kafka-form.ts';
import { KafkaBlockForm, UnsavedElsewhere } from './KafkaBlockForm.tsx';

const everyType = (): Pipeline => graphFixtures['every-block-type'];

const listed = (names: string[] | null): ConnectionNamesState => ({
  names,
  loading: false,
  problem: null,
  failure: null,
});

const render = (
  role: KafkaRole,
  draft: KafkaDraft | null = null,
  connections: ConnectionNamesState = listed(['billing', 'eu-cluster']),
  pipeline: Pipeline = everyType(),
) =>
  renderToStaticMarkup(
    <KafkaBlockForm
      pipeline={pipeline}
      role={role}
      draft={draft}
      onDraft={() => undefined}
      onSave={() => undefined}
      connections={connections}
      onReloadConnections={() => undefined}
    />,
  );

/** The every-block-type fixture with both blocks naming a connection. */
const named = (source: string, destination: string): Pipeline => {
  const pipeline = structuredClone(everyType());
  pipeline.source = { ...pipeline.source, connection: source };
  pipeline.destination = { ...pipeline.destination, connection: destination };
  return pipeline;
};

/** The connection select's options: value, label, and whether selected. */
const options = (html: string) => {
  const select =
    /<select id="kafka-[a-z]+-connection"[^>]*>(.*?)<\/select>/s.exec(html);
  expect(select).not.toBeNull();
  return [
    ...(select?.[1] ?? '').matchAll(
      /<option value="([^"]*)"([^>]*)>([^<]*)<\/option>/g,
    ),
  ].map((match) => ({
    value: match[1],
    label: match[3],
    selected: (match[2] ?? '').includes('selected'),
  }));
};

const typed = (
  role: KafkaRole,
  change: Record<string, string>,
): KafkaDraft => ({ ...draftFor(everyType(), role), ...change });

/** The markup of one field, from its wrapper to the next one. */
const field = (html: string, name: string): string => {
  const start = html.indexOf(`data-field="${name}"`);
  expect(start).toBeGreaterThan(-1);
  const end = html.indexOf('data-field=', start + 1);
  return html.slice(start, end === -1 ? undefined : end);
};

describe('KafkaBlockForm', () => {
  it('shows the source topic, consumer group and offset reset', () => {
    const html = render('source');
    expect(html).toContain('id="kafka-source-topic"');
    expect(html).toContain('value="orders.v1"');
    expect(html).toContain('value="kpv-orders-enriched"');
    expect(html).toMatch(/<select id="kafka-source-autoOffsetReset"/);
    expect(html).toMatch(/<option value="earliest" selected="">/);
    expect(html).toContain('Default: latest');
    expect(html).not.toContain('kafka-source-acks');
  });

  it('shows the destination topic and producer acknowledgements', () => {
    const html = render('destination');
    expect(html).toContain('value="billing.v1"');
    expect(html).toMatch(/<select id="kafka-destination-acks"/);
    expect(html).toMatch(/<option value="all" selected="">/);
    expect(html).toContain('<option value="0">');
    expect(html).not.toContain('consumerGroupId');
  });

  it('puts an error inline next to the field it is about, and only there', () => {
    const html = render('source', typed('source', { topic: 'orders v1' }));
    const topic = field(html, 'topic');
    expect(topic).toContain('aria-invalid="true"');
    expect(topic).toContain(
      'aria-describedby="kafka-source-topic-hint kafka-source-topic-error"',
    );
    expect(topic).toMatch(
      /class="field-error" id="kafka-source-topic-error"[^>]*>[^<]*does not allow/,
    );
    expect(field(html, 'consumerGroupId')).not.toContain('field-error');
    // What was typed is what the field shows.
    expect(topic).toContain('value="orders v1"');
  });

  it('blocks saving while the form is invalid, keeping what was typed', () => {
    const invalid = render('source', typed('source', { topic: '' }));
    expect(invalid).toMatch(/<button type="submit" disabled="">Save/);
    expect(invalid).toContain('what you typed is kept');
    expect(invalid).toContain('data-dirty="true"');

    const valid = render('source', typed('source', { topic: 'orders.v2' }));
    expect(valid).toMatch(/<button type="submit">Save/);
    expect(valid).toContain('Unsaved changes.');

    const clean = render('source');
    expect(clean).toMatch(/<button type="submit" disabled="">Save/);
    expect(clean).toContain('data-dirty="false"');
  });

  it('shows a blank consumer group as the generated per-pipeline value', () => {
    const html = render('source', typed('source', { consumerGroupId: '' }));
    const group = field(html, 'consumerGroupId');
    expect(group).toContain('placeholder="Generated per pipeline"');
    expect(group).toContain(
      'the agent generates a consumer group for this pipeline',
    );
    expect(group).not.toContain('field-error');
  });

  it('renders no credential input, only the connection by name', () => {
    for (const html of [render('source'), render('destination')]) {
      const inputs = [...html.matchAll(/<input [^>]*name="([^"]+)"/g)].map(
        (match) => match[1],
      );
      expect(
        inputs.every((name) => name === 'topic' || name === 'consumerGroupId'),
      ).toBe(true);
      expect(html).not.toMatch(/type="password"/);
      expect(html).toContain('aria-label="Kafka connection"');
    }
  });
});

describe('the connection select', () => {
  it('offers no connection and every listed name, and is a select, not text', () => {
    for (const role of ['source', 'destination'] as const) {
      const html = render(role);
      expect(html).toMatch(
        new RegExp(`<select id="kafka-${role}-connection" name="connection"`),
      );
      expect(html).not.toMatch(/<input [^>]*name="connection"/);
      expect(html).not.toContain('<datalist');
      expect(options(html)).toEqual([
        { value: '', label: 'No connection', selected: true },
        { value: 'billing', label: 'billing', selected: false },
        { value: 'eu-cluster', label: 'eu-cluster', selected: false },
      ]);
      expect(html).toContain(`<label for="kafka-${role}-connection">`);
    }
  });

  it('shows the connection the block names as chosen', () => {
    const html = render(
      'source',
      null,
      undefined,
      named('eu-cluster', 'billing'),
    );
    expect(options(html).find((option) => option.selected)?.value).toBe(
      'eu-cluster',
    );
    expect(html).not.toContain('data-dangling');
    const destination = render(
      'destination',
      null,
      undefined,
      named('eu-cluster', 'billing'),
    );
    expect(options(destination).find((option) => option.selected)?.value).toBe(
      'billing',
    );
  });

  it('shows a typed choice as chosen and dirty, ready to save', () => {
    const html = render(
      'destination',
      typed('destination', { connection: 'billing' }),
    );
    expect(options(html).find((option) => option.selected)?.value).toBe(
      'billing',
    );
    expect(html).toContain('data-dirty="true"');
    expect(html).toMatch(/<button type="submit">Save/);
  });

  it('shows a dangling name as it is, says so, and still lets the form save', () => {
    const pipeline = named('gone', 'billing');
    const html = render('source', null, undefined, pipeline);
    expect(options(html)).toEqual([
      { value: '', label: 'No connection', selected: false },
      { value: 'gone', label: 'gone (not defined)', selected: true },
      { value: 'billing', label: 'billing', selected: false },
      { value: 'eu-cluster', label: 'eu-cluster', selected: false },
    ]);
    const connection = field(html, 'connection');
    expect(connection).toContain('data-dangling="gone"');
    expect(connection).toContain(
      'No Kafka connection named <code>gone</code> exists',
    );
    expect(connection).toContain(
      'aria-describedby="kafka-source-connection-hint kafka-source-connection-dangling"',
    );
    // Kept, not an error: the form is clean and the schema accepts it.
    expect(connection).not.toContain('aria-invalid');
    expect(html).toContain('data-dirty="false"');
    const edited = render(
      'source',
      { ...draftFor(pipeline, 'source'), topic: 'orders.v2' },
      undefined,
      pipeline,
    );
    expect(edited).toMatch(/<button type="submit">Save/);
    expect(options(edited).find((option) => option.selected)?.value).toBe(
      'gone',
    );
  });

  it('keeps offering the dangling name after another is chosen', () => {
    const pipeline = named('gone', 'billing');
    const html = render(
      'source',
      { ...draftFor(pipeline, 'source'), connection: 'billing' },
      undefined,
      pipeline,
    );
    expect(options(html).map((option) => option.value)).toEqual([
      '',
      'gone',
      'billing',
      'eu-cluster',
    ]);
    expect(options(html).find((option) => option.selected)?.value).toBe(
      'billing',
    );
    expect(html).not.toContain('data-dangling');
  });

  it('says when no connection is defined, and offers only no connection', () => {
    const html = render('source', null, listed([]));
    expect(options(html)).toEqual([
      { value: '', label: 'No connection', selected: true },
    ]);
    expect(html).toContain('data-connections="none"');
    expect(html).toContain('No Kafka connections are defined yet');
  });

  it('calls a held name unchecked, not dangling, until the names are read', () => {
    const pipeline = named('eu-cluster', 'billing');
    const html = render('source', null, listed(null), pipeline);
    expect(options(html)).toEqual([
      { value: '', label: 'No connection', selected: false },
      {
        value: 'eu-cluster',
        label: 'eu-cluster (not checked)',
        selected: true,
      },
    ]);
    expect(html).toContain('data-unchecked="eu-cluster"');
    expect(html).not.toContain('data-dangling');
    expect(html).toContain('The connection names have not been read.');
    const loading = render(
      'source',
      null,
      { ...listed(null), loading: true },
      pipeline,
    );
    expect(loading).toContain('Reading the connection names.');
    expect(loading).not.toContain('Read the names again');
  });

  it('says why the names could not be read, and offers to read them again', () => {
    const rejected = render('source', null, {
      ...listed(null),
      problem: { kind: 'rejected' },
    });
    expect(rejected).toContain('The connection names could not be read:');
    expect(rejected).toContain('Read the names again</button>');
    const failed = render('source', null, {
      ...listed(['billing']),
      failure: { error: 'io', message: 'The store could not be read.' },
    });
    expect(failed).toContain(
      'The connection names could not be read: The store could not be read.',
    );
    // The names last read are still offered.
    expect(options(failed).map((option) => option.value)).toEqual([
      '',
      'billing',
    ]);
  });
});

describe('UnsavedElsewhere', () => {
  it('names each Kafka block left with unsaved changes', () => {
    const html = renderToStaticMarkup(
      <UnsavedElsewhere
        pipeline={everyType()}
        nodes={['destination']}
        onShow={() => undefined}
      />,
    );
    expect(html).toContain('data-unsaved="destination"');
    expect(html).toContain('The Kafka destination has unsaved changes');
  });

  it('names a processing step left with unsaved changes, by its id', () => {
    const html = renderToStaticMarkup(
      <UnsavedElsewhere
        pipeline={everyType()}
        nodes={['step:only-paid', 'step:gone']}
        onShow={() => undefined}
      />,
    );
    expect(html).toContain('data-unsaved="step:only-paid"');
    expect(html).toContain('The Filter step <code>only-paid</code>');
    // A block no longer in the pipeline has nothing to show.
    expect(html).not.toContain('step:gone');
  });

  it('says nothing when there are none', () => {
    expect(
      renderToStaticMarkup(
        <UnsavedElsewhere
          pipeline={everyType()}
          nodes={[]}
          onShow={() => undefined}
        />,
      ),
    ).toBe('');
  });
});

describe('the side panel', () => {
  it('has no Kafka form until a pipeline is open and a Kafka block is selected', () => {
    const html = renderToStaticMarkup(
      <Shell
        state={{ status: 'connected' }}
        client={null}
        pipeline={everyType()}
      />,
    );
    expect(html).not.toContain('class="kafka-form"');
  });
});
