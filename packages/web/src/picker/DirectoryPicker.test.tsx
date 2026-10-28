// The picker against a stand-in agent: the real typed client with an injected
// fetch, every fixture in schema/fixtures/directory-responses.json and
// schema/fixtures/roots-responses.json, and every error kind of both. The
// picker is rendered to a string, as #8's shell is, so these run in Node.

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import type {
  DirectoryErrorKind,
  DirectoryListing,
  RootList,
  RootsErrorKind,
} from '@kpv/schema';

import { reopenAdvice } from '../connection/describe.ts';
import { createPickerController, type PickerController } from './controller.ts';
import { DirectoryPicker, PickerView } from './DirectoryPicker.tsx';
import { fakeAgent, json, rejection, type FakeAgent } from './fake-agent.ts';
import { directoryFixtures, rootsFixtures } from './fixtures.ts';
import {
  directoryErrorWords,
  pickerProblemScope,
  rootsErrorWords,
} from './words.ts';

// What a person reads, without the markup: React escapes text.
const text = (html: string) =>
  html
    .replace(/<[^>]*>/g, ' ')
    .replace(/&quot;/g, '"')
    .replace(/&#x27;/g, "'")
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')
    .replace(/\s+/g, ' ');

const fixtureListing = (name: string): DirectoryListing => {
  const found = directoryFixtures.listings.valid.find((c) => c.name === name);
  if (found === undefined) throw new Error(name);
  return found.body;
};
const fixtureRoots = (name: string): RootList => {
  const found = rootsFixtures.lists.valid.find((c) => c.name === name);
  if (found === undefined) throw new Error(name);
  return found.body;
};

const project = fixtureListing('a-project-directory');
const topOfHome = fixtureListing('the-top-of-home');
const truncated = fixtureListing('a-truncated-page');
const someRoots = fixtureRoots('available-and-unavailable');
const noRoots = fixtureRoots('no-roots');

const directoryMessages: Record<DirectoryErrorKind, string> = {
  invalid: 'the request is not valid',
  forbidden:
    'only the authorized project directories and the home directory can be browsed',
  notFound: 'this directory does not exist',
  unreadable: 'the agent is not permitted to read this directory',
  notDirectory: 'this path is not a directory',
  io: 'the directory could not be read',
};
const directoryStatus: Record<DirectoryErrorKind, number> = {
  invalid: 400,
  forbidden: 403,
  notFound: 404,
  unreadable: 403,
  notDirectory: 409,
  io: 500,
};
const rootsMessages: Record<RootsErrorKind, string> = {
  invalid: 'the request is not valid',
  forbidden:
    'only a directory inside the home directory can be a project directory',
  tooBroad:
    'the home directory, the directories above it and the filesystem root are too broad to be a project directory',
  notFound: 'this directory does not exist',
  notDirectory: 'a project directory must be a directory',
  unreadable: 'the agent is not permitted to read and search this directory',
  overlaps:
    'this directory is inside, or contains, a project directory that is already authorized',
  io: 'the project directories could not be changed',
};
const rootsStatus: Record<RootsErrorKind, number> = {
  invalid: 400,
  forbidden: 403,
  tooBroad: 403,
  notFound: 404,
  notDirectory: 409,
  unreadable: 403,
  overlaps: 409,
  io: 500,
};
const directoryKinds = Object.keys(directoryMessages) as DirectoryErrorKind[];
const rootsKinds = Object.keys(rootsMessages) as RootsErrorKind[];

function render(picker: PickerController) {
  return renderToStaticMarkup(
    <PickerView state={picker.current()} actions={picker} />,
  );
}

/** The `<button …>label</button>` whose text contains `label`. */
function button(html: string, label: string): string {
  const found = html
    .match(/<button[^>]*>.*?<\/button>/g)
    ?.find((b) => text(b).includes(label));
  if (found === undefined) {
    throw new Error(`no button "${label}" in ${text(html)}`);
  }
  return found;
}
const isDisabled = (b: string) => /^<button[^>]* disabled=""/.test(b);

/** A picker that has opened on `home` with `roots`. */
async function opened(
  home: DirectoryListing = project,
  roots: RootList = someRoots,
): Promise<{ agent: FakeAgent; picker: PickerController }> {
  const agent = fakeAgent();
  const picker = createPickerController(agent.client);
  picker.setOnline(true);
  agent.next('GET', '/api/directories').answer(json(200, home));
  agent.next('GET', '/api/roots').answer(json(200, roots));
  await agent.settle();
  return { agent, picker };
}

describe('opening', () => {
  it('opens on the home directory, asking for no path, and reads the roots', async () => {
    const agent = fakeAgent();
    const picker = createPickerController(agent.client);
    expect(agent.sent).toHaveLength(0);
    picker.setOnline(true);
    expect(agent.sent.map((r) => [r.method, r.path, r.queryPath])).toEqual([
      ['GET', '/api/directories', null],
      ['GET', '/api/roots', null],
    ]);
    agent.next('GET', '/api/directories').answer(json(200, topOfHome));
    agent.next('GET', '/api/roots').answer(json(200, noRoots));
    await agent.settle();
    const html = render(picker);
    expect(text(html)).toContain('Current directory /home/user');
    expect(html).toContain(
      '<p class="picker-path" data-path="current">/home/user</p>',
    );
  });

  it('says so when the home directory cannot be listed, and the roots can still be opened', async () => {
    const agent = fakeAgent();
    const picker = createPickerController(agent.client);
    picker.setOnline(true);
    agent.next('GET', '/api/directories').answer(
      json(403, {
        error: 'unreadable',
        message: directoryMessages.unreadable,
      }),
    );
    agent.next('GET', '/api/roots').answer(json(200, someRoots));
    await agent.settle();
    const html = render(picker);
    expect(text(html)).toContain('The agent could not list the home directory');
    expect(text(html)).toContain(directoryMessages.unreadable);
    // Still somewhere to go: home again, a typed path, or a root.
    expect(isDisabled(button(html, 'Home directory'))).toBe(false);
    expect(isDisabled(button(html, 'Open /home/user/code/app'))).toBe(false);
    picker.open('/home/user/code/app');
    expect(agent.next('GET', '/api/directories').queryPath).toBe(
      '/home/user/code/app',
    );
  });

  it('renders nothing that asks the agent without a client', () => {
    const html = renderToStaticMarkup(
      <DirectoryPicker client={null} connection={{ status: 'connected' }} />,
    );
    expect(text(html)).toContain('no session');
  });
});

describe('every directory listing fixture', () => {
  it.each(
    directoryFixtures.listings.valid.map((c) => [c.name, c.body] as const),
  )('%s', async (_name, listing) => {
    const { picker } = await opened(listing, noRoots);
    const html = render(picker);
    const read = text(html);
    expect(read).toContain(listing.path);
    for (const entry of listing.entries) {
      expect(html).toContain(
        `<span class="picker-entry-name">${entry.name}</span>`,
      );
    }
    expect(html.includes('Up to parent')).toBe(listing.parent !== undefined);
    expect(html.includes('data-truncated="true"')).toBe(listing.truncated);
    expect(html).toContain(`data-project="${listing.looksLikeProject}"`);
  });

  it('says what kind every entry is in words, and which can be opened and why not', async () => {
    const { picker } = await opened(project, noRoots);
    const html = render(picker);
    const entry = (name: string) => text(button(html, name)).trim();
    expect(entry('.git')).toBe('.git directory');
    expect(entry('go.mod')).toBe(
      'go.mod file (cannot be opened: it is a file)',
    );
    expect(entry('latest')).toBe('latest symlink to a directory');
    expect(entry('locked')).toBe(
      'locked directory, unreadable (cannot be opened: it is unreadable)',
    );
    expect(entry('opaque')).toBe(
      'opaque unknown: the agent could not describe it, unreadable (cannot be opened: it is unreadable)',
    );
    // Outside: marked, and nothing more about where it leads.
    expect(entry('to-etc')).toBe(
      'to-etc symlink leading outside the browsable area (cannot be opened: it leads outside the browsable area)',
    );
    expect(button(html, '.git')).toContain('aria-disabled="false"');
    expect(button(html, 'latest')).toContain('aria-disabled="false"');
    for (const closed of ['go.mod', 'locked', 'opaque', 'to-etc']) {
      // Not opened, but not skipped: still a focusable button.
      expect(button(html, closed)).toContain('aria-disabled="true"');
      expect(isDisabled(button(html, closed))).toBe(false);
    }
  });

  it('opens a symlink to a directory and shows the real path the agent answered', async () => {
    const { agent, picker } = await opened(project, noRoots);
    picker.open('/home/user/code/latest');
    const request = agent.next('GET', '/api/directories');
    expect(request.queryPath).toBe('/home/user/code/latest');
    request.answer(
      json(200, {
        ...topOfHome,
        path: '/home/user/releases/v2',
        parent: '/home/user/releases',
      }),
    );
    await agent.settle();
    const html = render(picker);
    expect(html).toContain('data-path="current">/home/user/releases/v2<');
    expect(html).not.toContain('data-path="current">/home/user/code/latest<');
  });

  it('goes up to exactly the parent the listing named', async () => {
    const { agent, picker } = await opened(
      { ...topOfHome, path: '/home/user/a/b', parent: '/home/user/a' },
      noRoots,
    );
    expect(render(picker)).toContain('Up to parent');
    picker.open('/home/user/a');
    expect(agent.next('GET', '/api/directories').queryPath).toBe(
      '/home/user/a',
    );
  });

  it('describes a truncated page as a sample, not as the whole directory', async () => {
    const { picker } = await opened(truncated, noRoots);
    const read = text(render(picker));
    expect(read).toContain('more entries than the 1 shown');
    expect(read).toContain('not the alphabetical start');
    expect(picker.current().announcement).toContain(
      'more entries than the 1 shown',
    );
  });

  it('shows the evidence of a project, each marker with what it describes, and nothing about languages', async () => {
    const { picker } = await opened(project, noRoots);
    const html = render(picker);
    expect(text(html)).toContain('Looks like a project');
    expect(html).toContain('<code>.git</code>: a Git repository');
    expect(html).toContain('<code>go.mod</code>: a Go module');
    expect(text(html)).not.toMatch(/language|entry point/i);
  });

  it('says a directory without markers does not look like one, and still offers to authorize it', async () => {
    const agent = fakeAgent();
    const picker = createPickerController(agent.client);
    picker.setOnline(true);
    agent.next('GET', '/api/directories').answer(
      json(200, {
        ...topOfHome,
        path: '/home/user/plain',
        parent: '/home/user',
      }),
    );
    agent.next('GET', '/api/roots').answer(json(200, noRoots));
    await agent.settle();
    const html = render(picker);
    expect(text(html)).toContain('No project markers here');
    expect(isDisabled(button(html, 'Authorize this directory'))).toBe(false);
  });
});

describe('names', () => {
  const awkward = [
    'has spaces',
    'ünïcødé 名前',
    '-rf',
    '<script>alert(1)</script>',
    'a & b "quoted" \'single\'',
    'x'.repeat(255),
  ];

  it('renders every name as text, never as markup', async () => {
    const listing: DirectoryListing = {
      ...topOfHome,
      entries: awkward.map((name) => ({
        name,
        path: `/home/user/${name}`,
        kind: 'directory',
        unreadable: false,
      })),
    };
    const { picker } = await opened(listing, noRoots);
    const html = render(picker);
    expect(html).not.toContain('<script>');
    expect(html).toContain('&lt;script&gt;alert(1)&lt;/script&gt;');
    expect(html).toContain('a &amp; b');
    for (const name of awkward) {
      expect(text(html)).toContain(name);
    }
  });
});

describe('browsing errors', () => {
  it.each(directoryKinds)(
    '%s gets its own wording and the agent message, and the last listing stays',
    async (kind) => {
      const { agent, picker } = await opened(project, noRoots);
      picker.open('/home/user/code/somewhere');
      agent.next('GET', '/api/directories').answer(
        json(directoryStatus[kind], {
          error: kind,
          message: directoryMessages[kind],
        }),
      );
      await agent.settle();
      const html = render(picker);
      const read = text(html);
      expect(html).toContain(`data-error="${kind}"`);
      expect(read).toContain(directoryErrorWords[kind].title);
      expect(read).toContain(directoryMessages[kind]);
      // Still where it was, with a way to act.
      expect(html).toContain('data-path="current">/home/user/code<');
      expect(read).toContain('Still showing /home/user/code');
      // Not an authentication problem and not the agent being down.
      expect(read).not.toContain('Not authorized');
      expect(read).not.toContain('Agent not reachable');
      expect(picker.current().problem).toBeNull();
    },
  );

  it.each(directoryFixtures.errors.valid.map((c) => [c.name, c.body] as const))(
    'fixture %s is shown with its kind and message',
    async (_name, error) => {
      const { agent, picker } = await opened(project, noRoots);
      picker.open('/elsewhere');
      agent
        .next('GET', '/api/directories')
        .answer(json(directoryStatus[error.error], error));
      await agent.settle();
      const read = text(render(picker));
      expect(read).toContain(directoryErrorWords[error.error].title);
      expect(read).toContain(error.message);
    },
  );

  it('words every kind differently, notFound and forbidden included', () => {
    const titles = directoryKinds.map(
      (kind) => directoryErrorWords[kind].title,
    );
    expect(new Set(titles).size).toBe(titles.length);
  });

  it('shows notFound and forbidden differently on screen', async () => {
    const shown = async (kind: DirectoryErrorKind) => {
      const { agent, picker } = await opened(project, noRoots);
      picker.open('/x');
      agent.next('GET', '/api/directories').answer(
        json(directoryStatus[kind], {
          error: kind,
          message: directoryMessages[kind],
        }),
      );
      await agent.settle();
      return text(render(picker));
    };
    const notFound = await shown('notFound');
    const forbidden = await shown('forbidden');
    expect(notFound).toContain('Could not list: Not there');
    expect(forbidden).toContain('Refused: Outside what can be browsed');
    expect(notFound).not.toBe(forbidden);
  });

  it('sends a typed relative path to the agent, shows its invalid message and keeps the listing', async () => {
    const { agent, picker } = await opened(project, noRoots);
    picker.open('code/app');
    const request = agent.next('GET', '/api/directories');
    expect(request.queryPath).toBe('code/app');
    request.answer(
      json(400, { error: 'invalid', message: directoryMessages.invalid }),
    );
    await agent.settle();
    const html = render(picker);
    expect(text(html)).toContain(directoryMessages.invalid);
    expect(html).toContain('data-path="current">/home/user/code<');
  });

  it('reaches a directory typed as an absolute path', async () => {
    const { agent, picker } = await opened(truncated, noRoots);
    picker.open('/home/user/zz-not-on-the-page');
    agent.next('GET', '/api/directories').answer(
      json(200, {
        ...topOfHome,
        path: '/home/user/zz-not-on-the-page',
        parent: '/home/user',
      }),
    );
    await agent.settle();
    expect(picker.current().listing?.path).toBe(
      '/home/user/zz-not-on-the-page',
    );
    expect(picker.current().focusTick).toBe(1);
  });
});

describe('every roots list fixture', () => {
  it.each(rootsFixtures.lists.valid.map((c) => [c.name, c.body] as const))(
    '%s',
    async (_name, roots) => {
      const { picker } = await opened(topOfHome, roots);
      const html = render(picker);
      if (roots.roots.length === 0) {
        expect(text(html)).toContain('None yet');
      }
      for (const root of roots.roots) {
        expect(text(html)).toContain(root.path);
        expect(isDisabled(button(html, `Revoke ${root.path}`))).toBe(false);
        expect(
          html.includes(
            `Open <span class="visually-hidden">${root.path}</span>`,
          ),
        ).toBe(root.available);
      }
    },
  );

  it('lists an unavailable root with its reason in words', async () => {
    const { picker } = await opened(topOfHome, someRoots);
    const read = text(render(picker));
    expect(read).toContain(
      '/home/user/code/gone unavailable: it no longer exists',
    );
    expect(read).toContain(
      '/home/user/code/moved unavailable: it now leads somewhere else than when it was authorized',
    );
    expect(read).toContain('/home/user/code/app available');
  });

  it('says plainly that revoking deletes nothing on disk', async () => {
    const { picker } = await opened(topOfHome, someRoots);
    const html = render(picker);
    expect(text(button(html, 'Revoke /home/user/code/app'))).toContain(
      'deletes nothing on disk',
    );
  });
});

describe('authorizing', () => {
  it('authorizes the current directory, shows the real path, and lists it without a reload', async () => {
    const { agent, picker } = await opened(
      { ...topOfHome, path: '/home/user/link', parent: '/home/user' },
      noRoots,
    );
    picker.authorize();
    const post = agent.next('POST', '/api/roots');
    expect(post.body).toEqual({ path: '/home/user/link' });
    // A symlink is authorized as where it leads.
    post.answer(json(200, { path: '/home/user/real' }));
    await agent.settle();
    expect(text(render(picker))).toContain('Authorized: /home/user/real');
    expect(picker.current().roots).toEqual([
      { path: '/home/user/real', available: true },
    ]);
    // And the list is read from the agent to confirm.
    agent
      .next('GET', '/api/roots')
      .answer(
        json(200, { roots: [{ path: '/home/user/real', available: true }] }),
      );
    await agent.settle();
    expect(text(render(picker))).toContain('/home/user/real available');
  });

  it('sends one request for a double click, the control disabled while it is in flight', async () => {
    const { agent, picker } = await opened(
      { ...topOfHome, path: '/home/user/app', parent: '/home/user' },
      noRoots,
    );
    picker.authorize();
    picker.authorize();
    expect(agent.sent.filter((r) => r.method === 'POST')).toHaveLength(1);
    expect(isDisabled(button(render(picker), 'Authorizing'))).toBe(true);
    agent
      .next('POST', '/api/roots')
      .answer(json(200, { path: '/home/user/app' }));
    await agent.settle();
    agent
      .next('GET', '/api/roots')
      .answer(
        json(200, { roots: [{ path: '/home/user/app', available: true }] }),
      );
    await agent.settle();
    // A repeat that arrives anyway changes nothing.
    expect(picker.current().roots).toHaveLength(1);
  });

  it('shows "already authorized" for a directory in the roots list', async () => {
    const { picker } = await opened(
      { ...topOfHome, path: '/home/user/code/app', parent: '/home/user/code' },
      someRoots,
    );
    const html = render(picker);
    expect(isDisabled(button(html, 'Already authorized'))).toBe(true);
    expect(html).not.toContain('Authorize this directory');
  });

  it.each(rootsKinds)(
    'a %s refusal shows its wording and the agent message and adds no root',
    async (kind) => {
      const { agent, picker } = await opened(
        { ...topOfHome, path: '/home/user/x', parent: '/home/user' },
        noRoots,
      );
      picker.authorize();
      agent.next('POST', '/api/roots').answer(
        json(rootsStatus[kind], {
          error: kind,
          message: rootsMessages[kind],
          ...(kind === 'overlaps' ? { root: '/home/user/x/inner' } : {}),
        }),
      );
      await agent.settle();
      const html = render(picker);
      expect(html).toContain(`data-error="${kind}"`);
      expect(text(html)).toContain(rootsErrorWords[kind].title);
      expect(text(html)).toContain(rootsMessages[kind]);
      expect(picker.current().roots).toEqual([]);
      expect(agent.pending()).toHaveLength(0);
      // A refusal is the agent's answer, not a connection problem.
      expect(picker.current().problem).toBeNull();
    },
  );

  it.each(rootsFixtures.errors.valid.map((c) => [c.name, c.body] as const))(
    'fixture %s is shown with its kind and message',
    async (_name, error) => {
      const { agent, picker } = await opened(topOfHome, noRoots);
      picker.authorize();
      agent
        .next('POST', '/api/roots')
        .answer(json(rootsStatus[error.error], error));
      await agent.settle();
      const read = text(render(picker));
      expect(read).toContain(rootsErrorWords[error.error].title);
      expect(read).toContain(error.message);
    },
  );

  it('explains a tooBroad refusal: pick a directory inside home', async () => {
    const { agent, picker } = await opened(topOfHome, noRoots);
    picker.authorize();
    expect(agent.next('POST', '/api/roots').body).toEqual({
      path: '/home/user',
    });
    agent
      .next('POST', '/api/roots')
      .answer(
        json(403, { error: 'tooBroad', message: rootsMessages.tooBroad }),
      );
    await agent.settle();
    expect(text(render(picker))).toContain(
      'Open a directory inside your home directory and authorize that',
    );
  });

  it('shows the root an overlaps refusal names, and offers to open or revoke it', async () => {
    const { agent, picker } = await opened(
      { ...topOfHome, path: '/home/user/code', parent: '/home/user' },
      someRoots,
    );
    picker.authorize();
    agent.next('POST', '/api/roots').answer(
      json(409, {
        error: 'overlaps',
        message: rootsMessages.overlaps,
        root: '/home/user/code/app',
      }),
    );
    await agent.settle();
    const html = render(picker);
    expect(text(html)).toContain('Already authorized: /home/user/code/app');
    expect(isDisabled(button(html, 'Open /home/user/code/app'))).toBe(false);
    expect(
      isDisabled(
        button(html, 'Revoke /home/user/code/app (deletes nothing on disk)'),
      ),
    ).toBe(false);
    picker.revoke('/home/user/code/app');
    expect(agent.next('DELETE', '/api/roots').body).toEqual({
      path: '/home/user/code/app',
    });
  });
});

describe('revoking', () => {
  it('revokes and shows the set the agent answered', async () => {
    const { agent, picker } = await opened(topOfHome, someRoots);
    picker.revoke('/home/user/code/gone');
    const del = agent.next('DELETE', '/api/roots');
    expect(del.body).toEqual({ path: '/home/user/code/gone' });
    del.answer(
      json(200, { roots: [{ path: '/home/user/code/app', available: true }] }),
    );
    await agent.settle();
    expect(picker.current().roots).toEqual([
      { path: '/home/user/code/app', available: true },
    ]);
    expect(text(render(picker))).toContain(
      'Revoked /home/user/code/gone . Nothing on disk was deleted.',
    );
  });

  it('can revoke an unavailable root, which cannot be opened', async () => {
    const { picker } = await opened(topOfHome, someRoots);
    const html = render(picker);
    expect(html).not.toContain(
      'Open <span class="visually-hidden">/home/user/code/gone</span>',
    );
    expect(isDisabled(button(html, 'Revoke /home/user/code/gone'))).toBe(false);
  });

  it('treats a root another tab already revoked as notFound and reads the list again', async () => {
    const { agent, picker } = await opened(topOfHome, someRoots);
    picker.revoke('/home/user/code/app');
    agent.next('DELETE', '/api/roots').answer(
      json(404, {
        error: 'notFound',
        message: 'this path is not an authorized project directory',
      }),
    );
    await agent.settle();
    const read = text(render(picker));
    expect(read).toContain('Not revoked: Not there');
    expect(read).toContain('perhaps because it was revoked in another tab');
    agent.next('GET', '/api/roots').answer(json(200, noRoots));
    await agent.settle();
    expect(picker.current().roots).toEqual([]);
    expect(picker.current().problem).toBeNull();
  });
});

describe('connection problems', () => {
  const problems = [
    ['the uniform 404', rejection, 'unauthenticated', 'Not authorized'],
    [
      'a schema mismatch',
      () => json(200, { path: '' }),
      'failed',
      'Agent error',
    ],
  ] as const;

  it.each(problems)(
    '%s on a browse shows #8 state, not a picker error',
    async (_name, answer, state, label) => {
      const { agent, picker } = await opened(project, noRoots);
      picker.open('/home/user/code/.git');
      agent.next('GET', '/api/directories').answer(answer());
      await agent.settle();
      const html = render(picker);
      expect(html).toContain(`data-state="${state}"`);
      expect(text(html)).toContain(label);
      expect(html).not.toContain('class="picker-error"');
      if (state === 'unauthenticated') {
        expect(text(html)).toContain(reopenAdvice);
      }
    },
  );

  it('a request with no answer shows the unreachable state', async () => {
    const { agent, picker } = await opened(project, noRoots);
    picker.open('/home/user/code/.git');
    agent.next('GET', '/api/directories').drop();
    await agent.settle();
    const html = render(picker);
    expect(html).toContain('data-state="unreachable"');
    expect(text(html)).toContain('Agent not reachable');
  });

  it.each([
    ['listRoots', 'GET'],
    ['authorize', 'POST'],
    ['revoke', 'DELETE'],
  ] as const)(
    'the uniform 404 on %s is the unauthenticated state too',
    async (_name, method) => {
      const agent = fakeAgent();
      const picker = createPickerController(agent.client);
      picker.setOnline(true);
      agent.next('GET', '/api/directories').answer(json(200, project));
      if (method === 'GET') {
        agent.next('GET', '/api/roots').answer(rejection());
      } else {
        agent.next('GET', '/api/roots').answer(json(200, someRoots));
        await agent.settle();
        if (method === 'POST') picker.authorize();
        else picker.revoke('/home/user/code/app');
        agent.next(method, '/api/roots').answer(rejection());
      }
      await agent.settle();
      const html = render(picker);
      expect(html).toContain('data-state="unauthenticated"');
      expect(html).not.toContain('class="picker-error"');
      // And a change that was rejected is not shown as done.
      expect(html).not.toContain('data-outcome="authorized"');
      expect(html).not.toContain('data-outcome="revoked"');
    },
  );

  it('marks everything last known while disconnected, with authorize and revoke off', async () => {
    const { picker } = await opened(
      { ...topOfHome, path: '/home/user/app', parent: '/home/user' },
      someRoots,
    );
    picker.setOnline(false);
    const html = render(picker);
    expect(text(html)).toContain('Current directory (last known)');
    expect(text(html)).toContain('Authorized project directories (last known)');
    expect(html).toContain('data-stale="true"');
    expect(isDisabled(button(html, 'Authorize this directory'))).toBe(true);
    expect(isDisabled(button(html, 'Revoke /home/user/code/app'))).toBe(true);
    picker.authorize();
    picker.revoke('/home/user/code/app');
    expect(picker.current().busy).toBeNull();
  });

  it('reads the current directory and the roots again on reconnect, not home', async () => {
    const { agent, picker } = await opened(project, someRoots);
    picker.setOnline(false);
    picker.setOnline(true);
    const again = agent.next('GET', '/api/directories');
    expect(again.queryPath).toBe('/home/user/code');
    agent.next('GET', '/api/roots').answer(json(200, noRoots));
    again.answer(json(200, project));
    await agent.settle();
    expect(picker.current().roots).toEqual([]);
    expect(text(render(picker))).not.toContain('last known');
  });

  it('shows notFound when the current directory went while disconnected', async () => {
    const { agent, picker } = await opened(project, noRoots);
    picker.setOnline(false);
    picker.setOnline(true);
    agent
      .next('GET', '/api/directories')
      .answer(
        json(404, { error: 'notFound', message: directoryMessages.notFound }),
      );
    agent.next('GET', '/api/roots').answer(json(200, noRoots));
    await agent.settle();
    const html = render(picker);
    expect(html).toContain('data-error="notFound"');
    expect(isDisabled(button(html, 'Home directory'))).toBe(false);
  });

  it('never shows an authorize in flight when the connection dropped as done, and reads the roots after', async () => {
    const { agent, picker } = await opened(
      { ...topOfHome, path: '/home/user/app', parent: '/home/user' },
      noRoots,
    );
    picker.authorize();
    const post = agent.next('POST', '/api/roots');
    picker.setOnline(false);
    // The answer arrives late, after the drop: not trusted.
    post.answer(json(200, { path: '/home/user/app' }));
    await agent.settle();
    expect(picker.current().outcome).toEqual({
      kind: 'unconfirmed',
      action: 'authorize',
      path: '/home/user/app',
      reason: { kind: 'dropped' },
    });
    expect(picker.current().roots).toEqual([]);
    expect(text(render(picker))).toContain('Not confirmed');
    picker.setOnline(true);
    agent.next('GET', '/api/directories').answer(
      json(200, {
        ...topOfHome,
        path: '/home/user/app',
        parent: '/home/user',
      }),
    );
    agent
      .next('GET', '/api/roots')
      .answer(
        json(200, { roots: [{ path: '/home/user/app', available: true }] }),
      );
    await agent.settle();
    expect(picker.current().roots).toEqual([
      { path: '/home/user/app', available: true },
    ]);
  });

  it('never shows a revoke in flight when the connection dropped as done', async () => {
    const { agent, picker } = await opened(topOfHome, someRoots);
    picker.revoke('/home/user/code/app');
    const del = agent.next('DELETE', '/api/roots');
    picker.setOnline(false);
    del.drop();
    await agent.settle();
    expect(picker.current().outcome?.kind).toBe('unconfirmed');
    expect(picker.current().roots).toEqual(someRoots.roots);
  });
});

describe('latest request wins', () => {
  it('shows only the answer to the latest browse, whatever order the answers come in', async () => {
    const { agent, picker } = await opened(project, noRoots);
    picker.open('/home/user/code/.git');
    picker.open('/home/user/code/latest');
    const [first, second] = agent
      .pending()
      .filter((r) => r.path === '/api/directories');
    if (first === undefined || second === undefined)
      throw new Error('two browses');
    second.answer(
      json(200, {
        ...topOfHome,
        path: '/home/user/releases/v2',
        parent: '/home/user',
      }),
    );
    await agent.settle();
    first.answer(
      json(200, {
        ...topOfHome,
        path: '/home/user/code/.git',
        parent: '/home/user/code',
      }),
    );
    await agent.settle();
    expect(picker.current().listing?.path).toBe('/home/user/releases/v2');
  });

  it('drops a late error for an earlier browse too', async () => {
    const { agent, picker } = await opened(project, noRoots);
    picker.open('/a');
    picker.open('/home/user/code/latest');
    const [first, second] = agent
      .pending()
      .filter((r) => r.path === '/api/directories');
    if (first === undefined || second === undefined)
      throw new Error('two browses');
    second.answer(
      json(200, {
        ...topOfHome,
        path: '/home/user/releases/v2',
        parent: '/home/user',
      }),
    );
    first.answer(
      json(403, { error: 'forbidden', message: directoryMessages.forbidden }),
    );
    await agent.settle();
    expect(picker.current().browseError).toBeNull();
    expect(picker.current().listing?.path).toBe('/home/user/releases/v2');
  });

  it('drops a browse answer that arrives after the connection went', async () => {
    const { agent, picker } = await opened(project, noRoots);
    picker.open('/home/user/code/latest');
    const late = agent.next('GET', '/api/directories');
    picker.setOnline(false);
    late.answer(json(200, { ...topOfHome, path: '/home/user/releases/v2' }));
    await agent.settle();
    expect(picker.current().listing?.path).toBe('/home/user/code');
  });
});

describe('accessibility in the markup', () => {
  it('announces outcomes through a live region and names the lists and controls', async () => {
    const { picker } = await opened(project, someRoots);
    const html = render(picker);
    expect(html).toMatch(
      /role="status" aria-live="polite">Listed \/home\/user\/code: 6 entries\. It looks like a project\.</,
    );
    expect(html).toMatch(
      /<ul aria-labelledby="[^"]+" aria-describedby="[^"]+-keys">/,
    );
    expect(html).toContain('<label for=');
    expect(html).toContain('aria-label="Directory picker"');
  });

  it('makes the entry list one tab stop, at the first entry', async () => {
    const { picker } = await opened(project, noRoots);
    const html = render(picker);
    const entries = html.match(/<button[^>]*class="picker-entry"[^>]*>/g) ?? [];
    expect(entries).toHaveLength(6);
    expect(entries.filter((b) => b.includes('tabindex="0"'))).toHaveLength(1);
    expect(entries[0]).toContain('tabindex="0"');
  });

  it('puts the location heading where focus can be moved to', async () => {
    const { picker } = await opened(project, noRoots);
    expect(render(picker)).toMatch(
      /<h3 id="[^"]+" tabindex="-1">Current directory/,
    );
  });
});

// #66: the picker says why something is unknown or not done, for the reason
// that applies and only while it applies.

type Held = ReturnType<FakeAgent['next']>;
const unreadable = () => json(200, { unexpected: true });
const answerWith = {
  rejected: (r: Held) => r.answer(rejection()),
  unreachable: (r: Held) => r.drop(),
  failed: (r: Held) => r.answer(unreadable()),
} as const;
type ProblemKind = keyof typeof answerWith;
const problemKinds = Object.keys(answerWith) as ProblemKind[];

const staleCause: Record<ProblemKind, string> = {
  rejected: 'the agent rejected the picker’s last request',
  unreachable: 'the agent did not answer the picker’s last request',
  failed: 'the agent’s last answer to the picker could not be read',
};

const app = { ...topOfHome, path: '/home/user/app', parent: '/home/user' };
const appRoot = { path: '/home/user/app', available: true };

/** The text of the element with `attr`, e.g. `data-stale="true"`. */
function element(html: string, attr: string): string {
  const found = new RegExp(`<(\\w+)[^>]*${attr}[^>]*>.*?</\\1>`).exec(html);
  if (found === null) {
    throw new Error(`no element with ${attr} in ${text(html)}`);
  }
  return text(found[0]).trim();
}

/** The live region's text, which must be what is on screen too. */
function announced(html: string): string {
  const found = /role="status" aria-live="polite">(.*?)<\/p>/.exec(html);
  return text(found?.[1] ?? '').trim();
}

describe('#66 part 1: the stale note gives the real reason', () => {
  it.each(problemKinds)(
    'a %s picker request with the channel up says so, not "not connected"',
    async (kind) => {
      const { agent, picker } = await opened(project, someRoots);
      picker.open('/home/user/code/.git');
      answerWith[kind](agent.next('GET', '/api/directories'));
      await agent.settle();
      expect(picker.current().online).toBe(true);
      const html = render(picker);
      const note = element(html, 'data-stale="true"');
      expect(note).toContain(staleCause[kind]);
      expect(note).toContain('may be out of date');
      expect(note).not.toContain('not connected');
      expect(html).toContain(`data-cause="${kind}"`);
      // The notice says it is about the picker's request, not the connection.
      expect(text(html)).toContain('The picker’s last request:');
      expect(text(html)).toContain(pickerProblemScope);
      expect(text(html)).not.toContain('not connected');
      expect(text(html)).toContain('Current directory (last known)');
    },
  );

  it.each(problemKinds)(
    'a %s roots read with the channel up says so too',
    async (kind) => {
      const agent = fakeAgent();
      const picker = createPickerController(agent.client);
      picker.setOnline(true);
      agent.next('GET', '/api/directories').answer(json(200, project));
      await agent.settle();
      answerWith[kind](agent.next('GET', '/api/roots'));
      await agent.settle();
      const note = element(render(picker), 'data-stale="true"');
      expect(note).toContain(staleCause[kind]);
      expect(note).not.toContain('not connected');
    },
  );

  it.each(problemKinds)(
    'a %s problem switches to "not connected" when the channel drops, and the re-read clears it',
    async (kind) => {
      const { agent, picker } = await opened(project, someRoots);
      picker.open('/home/user/code/.git');
      answerWith[kind](agent.next('GET', '/api/directories'));
      await agent.settle();
      picker.setOnline(false);
      let html = render(picker);
      expect(element(html, 'data-stale="true"')).toContain(
        'the agent is not connected',
      );
      expect(html).toContain('data-cause="disconnected"');
      expect(text(html)).not.toContain(staleCause[kind]);
      // The status bar speaks for the connection now; no picker notice.
      expect(html).not.toContain('picker-problem');
      picker.setOnline(true);
      agent.next('GET', '/api/directories').answer(json(200, project));
      agent.next('GET', '/api/roots').answer(json(200, someRoots));
      await agent.settle();
      html = render(picker);
      expect(html).not.toContain('data-stale');
      expect(text(html)).not.toContain('last known');
      expect(html).not.toContain('picker-problem');
    },
  );

  it.each(['unreachable', 'failed'] as const)(
    'after a %s problem, Try again that succeeds clears the note, "(last known)" and the notice, and enables authorize and revoke',
    async (kind) => {
      const { agent, picker } = await opened(app, someRoots);
      picker.open('/home/user/code/.git');
      answerWith[kind](agent.next('GET', '/api/directories'));
      await agent.settle();
      let html = render(picker);
      expect(isDisabled(button(html, 'Authorize this directory'))).toBe(true);
      expect(isDisabled(button(html, 'Revoke /home/user/code/app'))).toBe(true);
      expect(button(html, 'Try again')).toBeTruthy();
      picker.refresh();
      expect(agent.next('GET', '/api/directories').queryPath).toBe(
        '/home/user/app',
      );
      agent.next('GET', '/api/directories').answer(json(200, app));
      agent.next('GET', '/api/roots').answer(json(200, someRoots));
      await agent.settle();
      html = render(picker);
      expect(html).not.toContain('data-stale');
      expect(text(html)).not.toContain('last known');
      expect(html).not.toContain('picker-problem');
      expect(isDisabled(button(html, 'Authorize this directory'))).toBe(false);
      expect(isDisabled(button(html, 'Revoke /home/user/code/app'))).toBe(
        false,
      );
    },
  );
});

describe('#66 part 2: a rejected or unreadable authorize or revoke gives the real reason', () => {
  async function authorizeWith(answer: (r: Held) => void) {
    const { agent, picker } = await opened(app, noRoots);
    picker.authorize();
    answer(agent.next('POST', '/api/roots'));
    await agent.settle();
    return { agent, picker, html: render(picker) };
  }
  async function revokeWith(answer: (r: Held) => void) {
    const { agent, picker } = await opened(topOfHome, someRoots);
    picker.revoke('/home/user/code/app');
    answer(agent.next('DELETE', '/api/roots'));
    await agent.settle();
    return { agent, picker, html: render(picker) };
  }

  it('a rejected authorize says the agent rejected it, so it was not authorized', async () => {
    const { picker, html } = await authorizeWith(answerWith.rejected);
    const outcome = element(html, 'data-outcome="rejected"');
    expect(outcome).toBe(
      'Not authorized: the agent rejected the request, so /home/user/app was not authorized. Nothing was changed on the agent.',
    );
    expect(text(html)).not.toContain('connection went');
    expect(announced(html)).toBe(outcome);
    expect(text(button(html, 'Authorize this directory')).trim()).toBe(
      'Authorize this directory',
    );
    expect(picker.current().roots).toEqual([]);
  });

  it('a rejected revoke says the agent rejected it, so it was not revoked', async () => {
    const { picker, html } = await revokeWith(answerWith.rejected);
    const outcome = element(html, 'data-outcome="rejected"');
    expect(outcome).toBe(
      'Not revoked: the agent rejected the request, so /home/user/code/app was not revoked. Nothing was changed on the agent.',
    );
    expect(text(html)).not.toContain('connection went');
    expect(announced(html)).toBe(outcome);
    expect(text(html)).not.toContain('Revoking…');
    expect(picker.current().roots).toEqual(someRoots.roots);
  });

  it.each([
    ['authorize', 'authorized'],
    ['revoke', 'revoked'],
  ] as const)(
    'an unreadable answer to %s says the agent answered but it could not be read',
    async (action, done) => {
      const { picker, html } =
        action === 'authorize'
          ? await authorizeWith(answerWith.failed)
          : await revokeWith(answerWith.failed);
      const outcome = element(html, 'data-reason="failed"');
      expect(outcome).toContain('Not confirmed: the agent answered');
      expect(outcome).toContain('the answer could not be read');
      expect(outcome).toContain(`it is not known whether`);
      expect(outcome).toContain(`was ${done}`);
      expect(text(html)).not.toContain('connection went');
      expect(html).not.toContain(`data-outcome="${done}"`);
      expect(announced(html)).toBe(outcome);
      expect(picker.current().busy).toBeNull();
      expect(picker.current().roots).toEqual(
        action === 'authorize' ? [] : someRoots.roots,
      );
    },
  );

  it.each([
    ['authorize', 'authorized'],
    ['revoke', 'revoked'],
  ] as const)(
    'no answer to %s with the channel up says the agent did not answer',
    async (action, done) => {
      const { picker, html } =
        action === 'authorize'
          ? await authorizeWith(answerWith.unreachable)
          : await revokeWith(answerWith.unreachable);
      const outcome = element(html, 'data-reason="unreachable"');
      expect(outcome).toContain(
        'Not confirmed: the agent did not answer, so it is not known whether',
      );
      expect(outcome).toContain(`was ${done}`);
      expect(text(html)).not.toContain('connection went');
      expect(announced(html)).toBe(outcome);
      expect(picker.current().busy).toBeNull();
    },
  );

  it.each([
    ['authorize', 'authorized'],
    ['revoke', 'revoked'],
  ] as const)(
    'the connection dropping during %s keeps "the connection went before the agent answered"',
    async (action, done) => {
      const { agent, picker } =
        action === 'authorize'
          ? await opened(app, noRoots)
          : await opened(topOfHome, someRoots);
      if (action === 'authorize') picker.authorize();
      else picker.revoke('/home/user/code/app');
      const held = agent.next(
        action === 'authorize' ? 'POST' : 'DELETE',
        '/api/roots',
      );
      picker.setOnline(false);
      held.drop();
      await agent.settle();
      const html = render(picker);
      const outcome = element(html, 'data-reason="dropped"');
      expect(outcome).toContain(
        'Not confirmed: the connection went before the agent answered',
      );
      expect(outcome).toContain(`is not known to be ${done}`);
      expect(announced(html)).toBe(outcome);
      expect(text(html)).not.toContain('Authorizing…');
      expect(text(html)).not.toContain('Revoking…');
    },
  );

  it('a revoke notFound body is still "Not revoked: Not there", not a rejection', async () => {
    const { html } = await revokeWith((r) =>
      r.answer(
        json(404, {
          error: 'notFound',
          message: 'this path is not an authorized project directory',
        }),
      ),
    );
    expect(text(html)).toContain('Not revoked: Not there');
    expect(text(html)).toContain(
      'perhaps because it was revoked in another tab',
    );
    expect(html).not.toContain('data-outcome="rejected"');
    expect(text(html)).not.toContain('the agent rejected the request');
  });
});

describe('#66 part 3: "Not confirmed" is settled by the re-read', () => {
  /** An authorize or revoke left unconfirmed by a drop, then reconnected. */
  async function unconfirmedThenReconnect(
    action: 'authorize' | 'revoke',
    listing: DirectoryListing,
    roots: RootList,
    path: string,
  ) {
    const { agent, picker } = await opened(listing, roots);
    if (action === 'authorize') picker.authorize();
    else picker.revoke(path);
    picker.setOnline(false);
    expect(picker.current().outcome?.kind).toBe('unconfirmed');
    picker.setOnline(true);
    agent.next('GET', '/api/directories').answer(json(200, listing));
    return { agent, picker };
  }

  it('authorize, re-read lists it: settled as authorized', async () => {
    const { agent, picker } = await unconfirmedThenReconnect(
      'authorize',
      app,
      noRoots,
      app.path,
    );
    agent.next('GET', '/api/roots').answer(json(200, { roots: [appRoot] }));
    await agent.settle();
    const html = render(picker);
    const outcome = element(html, 'data-outcome="settled"');
    expect(outcome).toBe(
      '/home/user/app is authorized, as confirmed by the list of authorized directories the agent returned.',
    );
    expect(text(html)).not.toContain('Not confirmed');
    expect(announced(html)).toBe(outcome);
  });

  it('authorize, re-read does not list it: not in the list the agent returned', async () => {
    const { agent, picker } = await unconfirmedThenReconnect(
      'authorize',
      app,
      noRoots,
      app.path,
    );
    agent.next('GET', '/api/roots').answer(json(200, noRoots));
    await agent.settle();
    const html = render(picker);
    const outcome = element(html, 'data-outcome="settled"');
    expect(outcome).toBe(
      'Not authorized by that path: /home/user/app is not in the list of authorized directories the agent returned.',
    );
    expect(text(html)).not.toContain('Not confirmed');
    expect(announced(html)).toBe(outcome);
  });

  it('revoke, re-read no longer lists it: settled as revoked', async () => {
    const { agent, picker } = await unconfirmedThenReconnect(
      'revoke',
      topOfHome,
      someRoots,
      '/home/user/code/app',
    );
    agent.next('GET', '/api/roots').answer(json(200, noRoots));
    await agent.settle();
    const html = render(picker);
    const outcome = element(html, 'data-outcome="settled"');
    expect(outcome).toContain(
      '/home/user/code/app is revoked: the list of authorized directories the agent returned no longer has it.',
    );
    expect(text(html)).not.toContain('Not confirmed');
    expect(announced(html)).toBe(outcome);
  });

  it('revoke, re-read still lists it: settled as still authorized', async () => {
    const { agent, picker } = await unconfirmedThenReconnect(
      'revoke',
      topOfHome,
      someRoots,
      '/home/user/code/app',
    );
    agent.next('GET', '/api/roots').answer(json(200, someRoots));
    await agent.settle();
    const html = render(picker);
    const outcome = element(html, 'data-outcome="settled"');
    expect(outcome).toBe(
      '/home/user/code/app is still authorized: the list of authorized directories the agent returned still has it.',
    );
    expect(announced(html)).toBe(outcome);
  });

  it.each(problemKinds)(
    'a %s re-read leaves "Not confirmed" until a roots read succeeds',
    async (kind) => {
      const { agent, picker } = await unconfirmedThenReconnect(
        'authorize',
        app,
        noRoots,
        app.path,
      );
      answerWith[kind](agent.next('GET', '/api/roots'));
      await agent.settle();
      let html = render(picker);
      expect(element(html, 'data-reason="dropped"')).toContain(
        'Not confirmed: the connection went before the agent answered',
      );
      if (kind !== 'rejected') {
        picker.refresh();
        agent.next('GET', '/api/directories').answer(json(200, app));
        agent.next('GET', '/api/roots').answer(json(200, { roots: [appRoot] }));
        await agent.settle();
        html = render(picker);
        expect(html).toContain('data-outcome="settled"');
        expect(text(html)).not.toContain('Not confirmed');
      }
    },
  );

  it.each(problemKinds)(
    'a %s roots re-read is not cleared by the directory re-read landing after it',
    async (kind) => {
      const { agent, picker } = await opened(app, noRoots);
      picker.authorize();
      picker.setOnline(false);
      agent.next('POST', '/api/roots').drop();
      picker.setOnline(true);
      // The roots read fails first, then the directory read succeeds.
      answerWith[kind](agent.next('GET', '/api/roots'));
      await agent.settle();
      agent.next('GET', '/api/directories').answer(json(200, app));
      await agent.settle();
      expect(agent.pending()).toHaveLength(0);
      const html = render(picker);
      expect(picker.current().problem?.kind).toBe(kind);
      expect(element(html, 'data-stale="true"')).toContain(staleCause[kind]);
      expect(text(html)).toContain(
        'Authorized project directories (last known)',
      );
      expect(isDisabled(button(html, 'Authorize this directory'))).toBe(true);
      expect(text(html)).toContain('Not confirmed');
    },
  );

  it('the settled sentence stays in the live region when the directory re-read lands after it', async () => {
    const { agent, picker } = await opened(app, noRoots);
    picker.authorize();
    answerWith.failed(agent.next('POST', '/api/roots'));
    await agent.settle();
    picker.refresh();
    agent.next('GET', '/api/roots').answer(json(200, { roots: [appRoot] }));
    await agent.settle();
    agent.next('GET', '/api/directories').answer(json(200, app));
    await agent.settle();
    const html = render(picker);
    const outcome = element(html, 'data-outcome="settled"');
    expect(announced(html)).toContain(outcome);
    expect(announced(html)).toContain('Listed /home/user/app');
  });

  it('an unreadable authorize with the channel up is settled by Try again', async () => {
    const { agent, picker } = await opened(app, noRoots);
    picker.authorize();
    answerWith.failed(agent.next('POST', '/api/roots'));
    await agent.settle();
    expect(text(render(picker))).toContain('Not confirmed');
    picker.refresh();
    agent.next('GET', '/api/directories').answer(json(200, app));
    agent.next('GET', '/api/roots').answer(json(200, noRoots));
    await agent.settle();
    expect(element(render(picker), 'data-outcome="settled"')).toContain(
      'Not authorized by that path',
    );
  });

  it('a new authorize before the re-read lands replaces the old outcome, and the late re-read brings nothing back', async () => {
    const { agent, picker } = await opened(app, someRoots);
    picker.authorize();
    picker.setOnline(false);
    agent.next('POST', '/api/roots').drop();
    picker.setOnline(true);
    agent.next('GET', '/api/directories').answer(json(200, app));
    await agent.settle();
    const lateRoots = agent.next('GET', '/api/roots');
    // Online, no problem: the user authorizes again before the re-read lands.
    picker.authorize();
    answerWith.unreachable(agent.next('POST', '/api/roots'));
    await agent.settle();
    expect(picker.current().outcome).toMatchObject({
      kind: 'unconfirmed',
      reason: { kind: 'unreachable' },
    });
    lateRoots.answer(json(200, { roots: [...someRoots.roots, appRoot] }));
    await agent.settle();
    // The read started before the new outcome: it does not settle it.
    expect(picker.current().outcome).toMatchObject({
      kind: 'unconfirmed',
      reason: { kind: 'unreachable' },
    });
    expect(text(render(picker))).not.toContain('is authorized, as confirmed');
  });

  it('a revoke started before the re-read lands is not overwritten by a message about the old path', async () => {
    const { agent, picker } = await opened(app, someRoots);
    picker.authorize();
    picker.setOnline(false);
    picker.setOnline(true);
    agent.next('GET', '/api/directories').answer(json(200, app));
    await agent.settle();
    const lateRoots = agent.next('GET', '/api/roots');
    picker.revoke('/home/user/code/app');
    agent
      .next('DELETE', '/api/roots')
      .answer(json(200, { roots: [someRoots.roots[1]] }));
    await agent.settle();
    lateRoots.answer(json(200, { roots: [appRoot] }));
    await agent.settle();
    expect(picker.current().outcome).toEqual({
      kind: 'revoked',
      path: '/home/user/code/app',
    });
    expect(text(render(picker))).not.toContain('/home/user/app is');
  });
});

describe('#66 part 4: no Authorize for a directory that is gone', () => {
  it.each(directoryKinds)(
    'a %s on the reconnect re-read of the listed directory disables Authorize and says why',
    async (kind) => {
      const { agent, picker } = await opened(app, noRoots);
      picker.setOnline(false);
      picker.setOnline(true);
      agent.next('GET', '/api/directories').answer(
        json(directoryStatus[kind], {
          error: kind,
          message: directoryMessages[kind],
        }),
      );
      agent.next('GET', '/api/roots').answer(json(200, noRoots));
      await agent.settle();
      const html = render(picker);
      const authorize = button(html, 'Authorize this directory');
      expect(isDisabled(authorize)).toBe(true);
      const gone = element(html, `data-gone="${kind}"`);
      expect(gone).toBe(
        `/home/user/app could not be listed just now: ${directoryErrorWords[kind].title}. Open a directory that can be listed to authorize it.`,
      );
      // Read with the control, not only next to it.
      const describedBy = /aria-describedby="([^"]+)"/.exec(authorize)?.[1];
      expect(html).toContain(`<p id="${describedBy}" class="picker-gone"`);
      expect(element(html, 'data-still-showing="stale"')).toBe(
        'Still showing /home/user/app as it was listed before this failure: it could not be listed again, so the entries below may no longer be there.',
      );
      expect(picker.current().announcement).toContain(gone);
    },
  );

  it('Try again and reopening fail the same way, and listing it again enables Authorize', async () => {
    const { agent, picker } = await opened(app, noRoots);
    const notFound = () =>
      json(404, { error: 'notFound', message: directoryMessages.notFound });
    picker.refresh();
    agent.next('GET', '/api/directories').answer(notFound());
    agent.next('GET', '/api/roots').answer(json(200, noRoots));
    await agent.settle();
    expect(isDisabled(button(render(picker), 'Authorize this directory'))).toBe(
      true,
    );
    // Home works from here, and coming back to the recreated directory
    // enables Authorize again.
    picker.home();
    agent.next('GET', '/api/directories').answer(json(200, topOfHome));
    await agent.settle();
    picker.open('/home/user/app');
    agent.next('GET', '/api/directories').answer(notFound());
    await agent.settle();
    // Home was listed, then the open of app failed: home is still listed and
    // a failed browse of a different path does not disable it.
    expect(picker.current().listing?.path).toBe('/home/user');
    picker.open('/home/user/app');
    agent.next('GET', '/api/directories').answer(json(200, app));
    await agent.settle();
    const html = render(picker);
    expect(isDisabled(button(html, 'Authorize this directory'))).toBe(false);
    expect(html).not.toContain('data-gone');
  });

  it('a failed browse of a different path does not disable Authorize for the listed directory', async () => {
    const { agent, picker } = await opened(app, noRoots);
    picker.open('/home/user/app/locked');
    agent.next('GET', '/api/directories').answer(
      json(403, {
        error: 'unreadable',
        message: directoryMessages.unreadable,
      }),
    );
    await agent.settle();
    const html = render(picker);
    expect(isDisabled(button(html, 'Authorize this directory'))).toBe(false);
    expect(html).not.toContain('data-gone');
    expect(element(html, 'data-still-showing="current"')).toBe(
      'Still showing /home/user/app.',
    );
  });

  it('a different path failing afterwards does not re-enable Authorize for the gone one', async () => {
    const { agent, picker } = await opened(app, noRoots);
    picker.refresh();
    agent
      .next('GET', '/api/directories')
      .answer(
        json(404, { error: 'notFound', message: directoryMessages.notFound }),
      );
    agent.next('GET', '/api/roots').answer(json(200, noRoots));
    await agent.settle();
    picker.open('/typo');
    agent
      .next('GET', '/api/directories')
      .answer(
        json(404, { error: 'notFound', message: directoryMessages.notFound }),
      );
    await agent.settle();
    expect(isDisabled(button(render(picker), 'Authorize this directory'))).toBe(
      true,
    );
  });

  it('a gone authorized root is listed as the agent says, with Revoke enabled, and other roots still open', async () => {
    const gone = {
      ...topOfHome,
      path: '/home/user/code/gone',
      parent: '/home/user/code',
    };
    const { agent, picker } = await opened(gone, {
      roots: [{ path: '/home/user/code/gone', available: true }, appRoot],
    });
    picker.setOnline(false);
    picker.setOnline(true);
    agent
      .next('GET', '/api/directories')
      .answer(
        json(404, { error: 'notFound', message: directoryMessages.notFound }),
      );
    agent.next('GET', '/api/roots').answer(json(200, someRoots));
    await agent.settle();
    const html = render(picker);
    expect(text(html)).toContain(
      '/home/user/code/gone unavailable: it no longer exists',
    );
    expect(isDisabled(button(html, 'Revoke /home/user/code/gone'))).toBe(false);
    expect(isDisabled(button(html, 'Authorize this directory'))).toBe(true);
    picker.open('/home/user/code/app');
    expect(agent.next('GET', '/api/directories').queryPath).toBe(
      '/home/user/code/app',
    );
  });
});

describe('#66 edge cases', () => {
  it('two tabs: authorized elsewhere while this one was unconfirmed is shown as authorized, claiming nothing about who did it', async () => {
    const { agent, picker } = await opened(app, noRoots);
    picker.authorize();
    const post = agent.next('POST', '/api/roots');
    picker.setOnline(false);
    post.drop();
    picker.setOnline(true);
    agent.next('GET', '/api/directories').answer(json(200, app));
    agent.next('GET', '/api/roots').answer(json(200, { roots: [appRoot] }));
    await agent.settle();
    const outcome = element(render(picker), 'data-outcome="settled"');
    expect(outcome).toContain('/home/user/app is authorized');
    expect(outcome).not.toMatch(/this (tab|page|request)|you /i);
  });

  it('symlink: an unconfirmed …/link is not claimed authorized when the list has …/real', async () => {
    const link = {
      ...topOfHome,
      path: '/home/user/link',
      parent: '/home/user',
    };
    const { agent, picker } = await opened(link, noRoots);
    picker.authorize();
    picker.setOnline(false);
    picker.setOnline(true);
    agent.next('GET', '/api/directories').answer(json(200, link));
    agent
      .next('GET', '/api/roots')
      .answer(
        json(200, { roots: [{ path: '/home/user/real', available: true }] }),
      );
    await agent.settle();
    const html = render(picker);
    const outcome = element(html, 'data-outcome="settled"');
    expect(outcome).toBe(
      'Not authorized by that path: /home/user/link is not in the list of authorized directories the agent returned.',
    );
    expect(text(html)).toContain('/home/user/real available');
  });

  it('problem, then drop, then back: the reconnect re-read decides', async () => {
    const { agent, picker } = await opened(app, noRoots);
    picker.open('/home/user/x');
    answerWith.rejected(agent.next('GET', '/api/directories'));
    await agent.settle();
    picker.setOnline(false);
    picker.setOnline(true);
    agent.next('GET', '/api/directories').answer(json(200, app));
    agent.next('GET', '/api/roots').answer(json(200, noRoots));
    await agent.settle();
    const html = render(picker);
    expect(picker.current().problem).toBeNull();
    expect(html).not.toContain('data-stale');
    expect(isDisabled(button(html, 'Authorize this directory'))).toBe(false);
  });

  it('latest wins: late answers from before a drop change neither the note, the outcome nor Authorize', async () => {
    const { agent, picker } = await opened(app, noRoots);
    picker.open('/home/user/app');
    const lateBrowse = agent.next('GET', '/api/directories');
    picker.setOnline(false);
    picker.setOnline(true);
    const again = agent.next('GET', '/api/directories');
    again.answer(json(200, app));
    agent.next('GET', '/api/roots').answer(json(200, noRoots));
    await agent.settle();
    lateBrowse.answer(
      json(404, { error: 'notFound', message: directoryMessages.notFound }),
    );
    await agent.settle();
    let html = render(picker);
    expect(isDisabled(button(html, 'Authorize this directory'))).toBe(false);
    expect(html).not.toContain('data-stale');
    // And a late problem from a superseded browse adds no note.
    picker.open('/home/user/a');
    const older = agent.next('GET', '/api/directories');
    picker.open('/home/user/app');
    const newer = agent
      .pending()
      .filter((r) => r.path === '/api/directories')[1];
    if (newer === undefined) throw new Error('two browses');
    newer.answer(json(200, app));
    answerWith.rejected(older);
    await agent.settle();
    html = render(picker);
    expect(html).not.toContain('data-stale');
    expect(picker.current().outcome).toBeNull();
  });

  it('no new state relies on colour: each is words, in the live region or the control description', async () => {
    const { agent, picker } = await opened(app, noRoots);
    picker.authorize();
    answerWith.rejected(agent.next('POST', '/api/roots'));
    await agent.settle();
    const html = render(picker);
    expect(announced(html)).toContain('the agent rejected the request');
  });
});
