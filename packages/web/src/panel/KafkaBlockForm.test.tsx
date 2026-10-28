// The Kafka configuration form rendered (#12): what it shows for a block, an
// error next to the field it is about, and nothing that takes a credential.

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import type { Pipeline } from '@kpv/schema';

import { graphFixtures } from '../graph/fixtures.ts';
import { Shell } from '../shell/App.tsx';
import type { KafkaDraft, KafkaRole } from './kafka-form.ts';
import { draftFor } from './kafka-form.ts';
import { KafkaBlockForm, UnsavedElsewhere } from './KafkaBlockForm.tsx';

const everyType = (): Pipeline => graphFixtures['every-block-type'];

const render = (role: KafkaRole, draft: KafkaDraft | null = null) =>
  renderToStaticMarkup(
    <KafkaBlockForm
      pipeline={everyType()}
      role={role}
      draft={draft}
      onDraft={() => undefined}
      onSave={() => undefined}
    />,
  );

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
