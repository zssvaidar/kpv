// The pipeline list rendered for every state the agent can answer: every list
// and error in schema/fixtures/pipelines-responses.json, the empty states, the
// create form, and a failed open in place of the graph. Rendered to a string,
// as the shell is.

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import type { PipelineList, UnreadableKind } from '@kpv/schema';

import { unavailableWords } from '../picker/words.ts';
import { emptyDraft } from './create-form.ts';
import type { PipelinesActions, PipelinesState } from './controller.ts';
import { fixtureList, pipelinesFixtures } from './fixtures.ts';
import { OpenFailureView, PipelinesView } from './PipelinesScreen.tsx';
import {
  emptyRootWords,
  noRootsWords,
  pipelinesErrorTitles,
  truncatedNote,
  unreadableWords,
} from './words.ts';

const text = (html: string) =>
  html
    .replace(/<[^>]*>/g, ' ')
    .replace(/&quot;/g, '"')
    .replace(/&#x27;/g, "'")
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')
    .replace(/\s+/g, ' ');

const nothing = () => undefined;
const actions: PipelinesActions = {
  refresh: nothing,
  open: nothing,
  close: nothing,
  startCreate: nothing,
  edit: nothing,
  cancelCreate: nothing,
  submitCreate: nothing,
};

const base: PipelinesState = {
  session: true,
  online: true,
  list: null,
  listError: null,
  loading: false,
  problem: null,
  opening: null,
  opened: null,
  openFailure: null,
  draft: null,
  creating: false,
  createFailure: null,
};

const render = (state: Partial<PipelinesState>) =>
  renderToStaticMarkup(
    <PipelinesView state={{ ...base, ...state }} actions={actions} />,
  );

/** The rendered markup of one entry, by its directory. */
function entryHtml(html: string, directory: string): string {
  const items = html.split('<li class="pipeline-entry"');
  const found = items.find((item) => item.includes(`>${directory}<`));
  if (found === undefined) throw new Error(`no entry ${directory}`);
  return found;
}

describe('the pipeline list', () => {
  it.each(pipelinesFixtures.lists.valid)(
    'renders every root and entry of the $name fixture',
    ({ body }) => {
      const html = render({ list: body });
      for (const root of body.roots) {
        expect(text(html)).toContain(root.path);
      }
    },
  );

  const html = render({ list: fixtureList('every-state') });

  it('offers to open a readable entry, with its name, id and language', () => {
    const billing = entryHtml(html, 'Orders to billing');
    expect(text(billing)).toContain('orders-to-billing');
    expect(text(billing)).toContain('python');
    expect(billing).toContain('>Open<');
  });

  it('shows an unreadable entry with its kind and every problem with pointer and line, and no open', () => {
    const kinds: [string, UnreadableKind][] = [
      ['broken-yaml', 'parse'],
      ['hand-edited', 'schema'],
      ['huge', 'tooLarge'],
      ['linked', 'link'],
      ['not-a-file', 'notRegularFile'],
      ['private', 'unreadable'],
    ];
    for (const [directory, kind] of kinds) {
      const entry = entryHtml(html, directory);
      expect(entry, directory).toContain(`data-kind="${kind}"`);
      expect(text(entry), directory).toContain(unreadableWords[kind]);
      expect(entry, directory).not.toContain('>Open<');
    }
    const schema = text(entryHtml(html, 'hand-edited'));
    expect(schema).toContain('/processingSteps/0/configuration');
    expect(schema).toContain('line 12, column 7');
    expect(schema).toContain("missing property 'onInvalid'");
    expect(text(entryHtml(html, 'broken-yaml'))).toContain('line 3');
  });

  it('names the other locations of a duplicate id', () => {
    expect(text(entryHtml(html, 'Orders to billing'))).toContain(
      'Another pipeline has this id: /home/user/code/other/copy.',
    );
  });

  it('says an unavailable root in the directory picker’s words, with no pipelines', () => {
    expect(text(html)).toContain(unavailableWords.notFound);
    for (const reason of Object.keys(
      unavailableWords,
    ) as (keyof typeof unavailableWords)[]) {
      const list: PipelineList = {
        roots: [
          {
            path: '/home/user/x',
            available: false,
            reason,
            truncated: false,
            skippedLinks: 0,
            pipelines: [],
          },
        ],
      };
      expect(text(render({ list }))).toContain(unavailableWords[reason]);
    }
  });

  it('says a truncated root was not looked at in full, and counts skipped links', () => {
    expect(text(html)).toContain(truncatedNote);
    expect(text(html)).toContain('2 symbolic links here were not followed.');
  });

  it('lists a root that is itself the pipeline, and offers no create in it', () => {
    expect(text(html)).toContain('this project directory itself');
  });

  it('with no authorized root, says so and leads to the directory picker', () => {
    const empty = render({ list: fixtureList('no-roots') });
    expect(text(empty)).toContain(noRootsWords);
    expect(empty).toContain('href="#directory-picker"');
    expect(empty).not.toContain('New pipeline');
  });

  it('a root with no pipelines says so and offers create', () => {
    const empty = render({ list: fixtureList('an-empty-root') });
    expect(text(empty)).toContain(emptyRootWords);
    expect(empty).toContain('Create a pipeline here');
  });

  it('offers no create when no root is available', () => {
    const list: PipelineList = {
      roots: [
        {
          path: '/home/user/gone',
          available: false,
          reason: 'notFound',
          truncated: false,
          skippedLinks: 0,
          pipelines: [],
        },
      ],
    };
    const shown = render({ list, draft: null });
    expect(shown).not.toContain('New pipeline');
    expect(shown).not.toContain('Create a pipeline here');
  });

  it.each(pipelinesFixtures.errors.valid)(
    'shows a $name list error with its title and message',
    ({ body }) => {
      const shown = text(render({ listError: body }));
      expect(shown).toContain(pipelinesErrorTitles[body.error]);
      expect(shown).toContain(body.message);
    },
  );

  it('says a request problem in the connection’s words', () => {
    expect(text(render({ problem: { kind: 'rejected' } }))).toContain(
      'Not authorized',
    );
    expect(text(render({ problem: { kind: 'unreachable' } }))).toContain(
      'not reachable',
    );
  });

  it('says when it is reading, and when there is no session', () => {
    expect(text(render({ loading: true }))).toContain('Reading the pipelines');
    expect(text(render({ session: false }))).toContain('no session');
  });
});

describe('the create form', () => {
  const list = fixtureList('every-state');

  it('asks for every field with none of them chosen, and cannot be sent', () => {
    const form = render({ list, draft: emptyDraft() });
    expect(form).toContain('aria-label="New pipeline"');
    for (const label of [
      'Name',
      'Language',
      'Source topic',
      'Destination topic',
      'Network mode',
    ]) {
      expect(text(form)).toContain(label);
    }
    expect(form).toMatch(/<button type="submit" disabled="">/);
    expect(text(form)).toContain('Choose one.');
  });

  it('with several roots lets the user pick one, and never asks for a path', () => {
    const form = render({ list, draft: emptyDraft() });
    expect(form).toContain('<option value="/home/user/code/app">');
    expect(form).toContain('<option value="/home/user/code/other">');
    // A root that is itself a pipeline is not offered.
    expect(form).not.toContain('<option value="/home/user/code/single">');
  });

  it('with one root names it, and can be sent once every field is valid', () => {
    const form = render({
      list: fixtureList('an-empty-root'),
      draft: {
        root: '/home/user/code/app',
        name: 'Orders',
        language: 'go',
        sourceTopic: 'in',
        destinationTopic: 'out',
        networkMode: 'host',
      },
    });
    expect(text(form)).toContain('In /home/user/code/app');
    expect(form).toMatch(/<button type="submit">Create</);
    expect(form).not.toContain('field-problem');
  });

  it('marks a field the schema refuses as it is typed', () => {
    const form = render({
      list: fixtureList('an-empty-root'),
      draft: { ...emptyDraft('/home/user/code/app'), sourceTopic: 'a b' },
    });
    expect(form).toMatch(/data-field="sourceTopic"[^]*aria-invalid="true"/);
  });

  it('shows why a create was refused, and whether its directory is left behind', () => {
    const failure = pipelinesFixtures.errors.valid.find(
      (c) => c.name === 'changed',
    )?.body;
    if (failure === undefined) throw new Error('no changed fixture');
    const form = text(
      render({
        list,
        draft: emptyDraft(),
        createFailure: { kind: 'error', error: failure },
      }),
    );
    expect(form).toContain(failure.message);
    expect(form).toContain('The directory it made is still there');
  });
});

describe('a failed open', () => {
  it.each(pipelinesFixtures.errors.valid)(
    'shows the $name answer in place of the graph, with a way back',
    ({ body }) => {
      const html = renderToStaticMarkup(
        <OpenFailureView
          state={{
            ...base,
            openFailure: {
              location: { root: '/home/user/code/app', directory: 'billing' },
              failure: { kind: 'error', error: body },
            },
          }}
          actions={actions}
        />,
      );
      expect(text(html)).toContain('/home/user/code/app/billing');
      expect(text(html)).toContain(body.message);
      expect(text(html)).toContain('Back to the pipelines');
      if (body.unreadable !== undefined) {
        expect(text(html)).toContain(unreadableWords[body.unreadable.kind]);
        expect(text(html)).toContain('line 9, column 3');
      }
      if (body.reason !== undefined) {
        expect(text(html)).toContain(unavailableWords[body.reason]);
      }
    },
  );

  it('shows a document the page refused as unreadable with its problems', () => {
    const html = renderToStaticMarkup(
      <OpenFailureView
        state={{
          ...base,
          openFailure: {
            location: { root: '/home/user/code/single' },
            failure: {
              kind: 'invalid',
              problems: [
                {
                  pointer: '/processingSteps/0/configuration',
                  message: 'Required',
                },
              ],
            },
          },
        }}
        actions={actions}
      />,
    );
    expect(text(html)).toContain(unreadableWords.schema);
    expect(text(html)).toContain('/processingSteps/0/configuration');
  });
});
