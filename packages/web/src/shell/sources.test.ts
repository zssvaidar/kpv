// The shell's custom-code files (#14), read and written through the real
// typed client against a stand-in agent (picker/fake-agent.ts).

import { describe, expect, it } from 'vitest';

import { isUnsaved } from '../blocks/source-buffer.ts';
import { fakeAgent, json, rejection } from '../picker/fake-agent.ts';
import { createSourcesController } from './sources.ts';

const location = { root: '/home/user/code/app', directory: 'billing' };
const hash = (c: string) => c.repeat(64);

const file = (path: string, text: string, generated = false, sha = 'a') => ({
  root: location.root,
  directory: location.directory,
  path,
  text,
  sha256: hash(sha),
  generated,
});

function started(paths: string[] = ['src/a.py']) {
  const agent = fakeAgent();
  const sources = createSourcesController(agent.client, location);
  sources.setOnline(true);
  sources.load(paths);
  return { agent, sources };
}

async function readAs(
  text: string,
  path = 'src/a.py',
): Promise<ReturnType<typeof started>> {
  const ready = started([path]);
  ready.agent
    .next('GET', '/api/pipelines/source')
    .answer(json(200, file(path, text)));
  await ready.agent.settle();
  return ready;
}

describe('reading', () => {
  it('reads nothing until online, then each distinct path once, by root, directory and path', () => {
    const agent = fakeAgent();
    const sources = createSourcesController(agent.client, location);
    sources.load(['src/a.py', 'src/b.py', 'src/a.py']);
    expect(agent.sent).toHaveLength(0);
    sources.setOnline(true);
    expect(agent.sent.map((r) => r.query.get('path'))).toEqual([
      'src/a.py',
      'src/b.py',
    ]);
    const first = agent.sent[0];
    expect(first?.query.get('root')).toBe(location.root);
    expect(first?.query.get('directory')).toBe(location.directory);
    // Drawn again, or loaded again: nothing more is read.
    sources.load(['src/a.py', 'src/b.py']);
    expect(agent.sent).toHaveLength(2);
  });

  it('leaves the directory out for a pipeline that is its root', () => {
    const agent = fakeAgent();
    const sources = createSourcesController(agent.client, {
      root: '/home/user/code/app',
    });
    sources.setOnline(true);
    sources.load(['src/a.py']);
    expect(agent.sent[0]?.query.has('directory')).toBe(false);
  });

  it('holds what the read answered as the editor holds it', async () => {
    const { sources } = await readAs('a\r\nb');
    expect(sources.current().buffers['src/a.py']).toMatchObject({
      status: 'loaded',
      text: 'a\nb',
      disk: { text: 'a\r\nb', sha256: hash('a') },
      generated: false,
    });
  });

  it('reads notFound as a file that does not exist yet', async () => {
    const { agent, sources } = started();
    agent
      .next('GET', '/api/pipelines/source')
      .answer(json(404, { error: 'notFound', message: 'no file' }));
    await agent.settle();
    expect(sources.current().buffers['src/a.py']).toMatchObject({
      status: 'loaded',
      disk: null,
      text: '',
    });
  });

  it('holds any other refusal as refused, with no text', async () => {
    const { agent, sources } = started();
    agent
      .next('GET', '/api/pipelines/source')
      .answer(json(422, { error: 'notText', message: 'not text' }));
    await agent.settle();
    expect(sources.current().buffers['src/a.py']).toEqual({
      status: 'refused',
      error: { error: 'notText', message: 'not text' },
    });
  });

  it('holds a rejected or unanswered read as a connection problem', async () => {
    const { agent, sources } = started(['src/a.py', 'src/b.py']);
    agent.next('GET', '/api/pipelines/source').answer(rejection());
    agent.next('GET', '/api/pipelines/source').drop();
    await agent.settle();
    expect(sources.current().buffers['src/a.py']).toEqual({
      status: 'problem',
      problem: { kind: 'rejected' },
    });
    expect(sources.current().buffers['src/b.py']).toEqual({
      status: 'problem',
      problem: { kind: 'unreachable' },
    });
  });

  it('reads again on reload, dropping what was typed, and only the last answer counts', async () => {
    const { agent, sources } = await readAs('one\n');
    sources.edit('src/a.py', 'typed\n');
    sources.reload('src/a.py');
    const older = agent.next('GET', '/api/pipelines/source');
    sources.reload('src/a.py');
    const newer = agent
      .pending()
      .find((r) => r !== older && r.path === '/api/pipelines/source');
    newer?.answer(json(200, file('src/a.py', 'two\n', false, 'b')));
    older.answer(json(200, file('src/a.py', 'stale\n')));
    await agent.settle();
    expect(sources.current().buffers['src/a.py']).toMatchObject({
      text: 'two\n',
      disk: { sha256: hash('b') },
    });
  });
});

describe('typing', () => {
  it('holds what was typed, and the unsaved list changes only when the set does', async () => {
    const { agent, sources } = await readAs('a\n');
    const clean = sources.current().unsaved;
    expect(clean).toEqual([]);
    sources.edit('src/a.py', 'ab\n');
    const dirty = sources.current().unsaved;
    expect(dirty).toEqual(['src/a.py']);
    sources.edit('src/a.py', 'abc\n');
    expect(sources.current().unsaved).toBe(dirty);
    sources.edit('src/a.py', 'a\n');
    expect(sources.current().unsaved).toEqual([]);
    // Nothing was written by typing.
    expect(agent.sent.filter((r) => r.method === 'PUT')).toHaveLength(0);
  });

  it('ignores typing into a generated file', async () => {
    const { agent, sources } = started();
    agent
      .next('GET', '/api/pipelines/source')
      .answer(json(200, file('generated/main.go', 'package main\n', true)));
    await agent.settle();
    sources.edit('src/a.py', 'changed');
    expect(sources.current().unsaved).toEqual([]);
    sources.save('src/a.py');
    expect(agent.sent.filter((r) => r.method === 'PUT')).toHaveLength(0);
  });
});

describe('saving', () => {
  it('writes with the hash the read answered, keeping CRLF and a missing final newline', async () => {
    const { agent, sources } = await readAs('a\r\nb');
    sources.edit('src/a.py', 'a\nB');
    sources.save('src/a.py');
    const put = agent.next('PUT', '/api/pipelines/source');
    expect(put.body).toEqual({
      root: location.root,
      directory: location.directory,
      path: 'src/a.py',
      text: 'a\r\nB',
      expectedSha256: hash('a'),
    });
    expect(sources.current().buffers['src/a.py']).toMatchObject({
      saving: true,
    });
    put.answer(
      json(200, {
        root: location.root,
        directory: location.directory,
        path: 'src/a.py',
        sha256: hash('c'),
      }),
    );
    await agent.settle();
    const saved = sources.current().buffers['src/a.py'];
    expect(saved).toMatchObject({
      saving: false,
      text: 'a\nB',
      disk: { text: 'a\r\nB', sha256: hash('c') },
    });
    expect(isUnsaved(saved)).toBe(false);
    expect(sources.current().unsaved).toEqual([]);
  });

  it('sends the next save with the hash the last one answered', async () => {
    const { agent, sources } = await readAs('a\n');
    sources.edit('src/a.py', 'b\n');
    sources.save('src/a.py');
    agent
      .next('PUT', '/api/pipelines/source')
      .answer(
        json(200, { root: location.root, path: 'src/a.py', sha256: hash('d') }),
      );
    await agent.settle();
    sources.edit('src/a.py', 'c\n');
    sources.save('src/a.py');
    expect(agent.next('PUT', '/api/pipelines/source').body).toMatchObject({
      text: 'c\n',
      expectedSha256: hash('d'),
    });
  });

  it('keeps what was typed while a save was out, unsaved against what was written', async () => {
    const { agent, sources } = await readAs('a\n');
    sources.edit('src/a.py', 'b\n');
    sources.save('src/a.py');
    sources.edit('src/a.py', 'bc\n');
    agent
      .next('PUT', '/api/pipelines/source')
      .answer(
        json(200, { root: location.root, path: 'src/a.py', sha256: hash('e') }),
      );
    await agent.settle();
    expect(sources.current().buffers['src/a.py']).toMatchObject({
      text: 'bc\n',
      read: 'b\n',
    });
    expect(sources.current().unsaved).toEqual(['src/a.py']);
  });

  it('creates a file that did not exist with expectedAbsent', async () => {
    const { agent, sources } = started();
    agent
      .next('GET', '/api/pipelines/source')
      .answer(json(404, { error: 'notFound', message: 'no file' }));
    await agent.settle();
    sources.save('src/a.py');
    expect(agent.sent.filter((r) => r.method === 'PUT')).toHaveLength(0);
    sources.edit('src/a.py', 'def process(m):\n    return m\n');
    sources.save('src/a.py');
    expect(agent.next('PUT', '/api/pipelines/source').body).toEqual({
      root: location.root,
      directory: location.directory,
      path: 'src/a.py',
      text: 'def process(m):\n    return m\n',
      expectedAbsent: true,
    });
  });

  it('sends what the agent would write for a lone surrogate', async () => {
    const { agent, sources } = await readAs('x\n');
    sources.edit('src/a.py', 'y\uD800\n');
    sources.save('src/a.py');
    expect(agent.next('PUT', '/api/pipelines/source').body).toMatchObject({
      text: 'y�\n',
    });
  });

  it('keeps what was typed on changed, says the file changed on disk, and never overwrites', async () => {
    const { agent, sources } = await readAs('a\n');
    sources.edit('src/a.py', 'mine\n');
    sources.save('src/a.py');
    agent.next('PUT', '/api/pipelines/source').answer(
      json(409, {
        error: 'changed',
        message: 'changed',
        sha256: hash('f'),
      }),
    );
    await agent.settle();
    const after = sources.current().buffers['src/a.py'];
    expect(after).toMatchObject({
      text: 'mine\n',
      saving: false,
      failure: { kind: 'changed' },
      // The hash stays the one read: a second save is changed again.
      disk: { sha256: hash('a') },
    });
    sources.save('src/a.py');
    expect(agent.next('PUT', '/api/pipelines/source').body).toMatchObject({
      expectedSha256: hash('a'),
    });
  });

  it('reads exists, for a file made since it was found missing, as changed', async () => {
    const { agent, sources } = started();
    agent
      .next('GET', '/api/pipelines/source')
      .answer(json(404, { error: 'notFound', message: 'no file' }));
    await agent.settle();
    sources.edit('src/a.py', 'x');
    sources.save('src/a.py');
    agent
      .next('PUT', '/api/pipelines/source')
      .answer(json(409, { error: 'exists', message: 'exists' }));
    await agent.settle();
    expect(sources.current().buffers['src/a.py']).toMatchObject({
      text: 'x',
      failure: { kind: 'changed' },
    });
  });

  it('keeps what was typed on any other refusal, with the refusal', async () => {
    const { agent, sources } = await readAs('a\n');
    sources.edit('src/a.py', 'mine\n');
    sources.save('src/a.py');
    agent
      .next('PUT', '/api/pipelines/source')
      .answer(json(422, { error: 'tooLarge', message: 'too large' }));
    await agent.settle();
    expect(sources.current().buffers['src/a.py']).toMatchObject({
      text: 'mine\n',
      failure: {
        kind: 'refused',
        error: { error: 'tooLarge', message: 'too large' },
      },
    });
    expect(sources.current().unsaved).toEqual(['src/a.py']);
  });

  it('sends nothing while offline, and keeps what was typed through a drop', async () => {
    const { agent, sources } = await readAs('a\n');
    sources.edit('src/a.py', 'mine\n');
    sources.save('src/a.py');
    const put = agent.next('PUT', '/api/pipelines/source');
    sources.setOnline(false);
    put.drop();
    await agent.settle();
    expect(sources.current().buffers['src/a.py']).toMatchObject({
      text: 'mine\n',
      saving: false,
      failure: null,
    });
    sources.save('src/a.py');
    expect(agent.sent.filter((r) => r.method === 'PUT')).toHaveLength(1);
    sources.setOnline(true);
    // Back online, nothing is read again over what was typed.
    expect(agent.sent.filter((r) => r.method === 'GET')).toHaveLength(1);
    sources.save('src/a.py');
    expect(agent.sent.filter((r) => r.method === 'PUT')).toHaveLength(2);
  });

  it('forgets a buffer only when told, and ignores a late answer for it', async () => {
    const { agent, sources } = await readAs('a\n');
    sources.edit('src/a.py', 'b\n');
    sources.save('src/a.py');
    sources.forget(['src/a.py']);
    agent
      .next('PUT', '/api/pipelines/source')
      .answer(
        json(200, { root: location.root, path: 'src/a.py', sha256: hash('g') }),
      );
    await agent.settle();
    expect(sources.current()).toEqual({ buffers: {}, unsaved: [] });
  });
});
