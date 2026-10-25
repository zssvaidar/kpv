import { describe, expect, it } from 'vitest';

import { takeToken, type TokenLocation } from './token.ts';

// A page whose URL and history behave like a browser's for this purpose: the
// current entry is replaced in place, and `href` is what the address bar shows.
function page(href: string, state: unknown = null) {
  const replaced: { state: unknown; url: string }[] = [];
  const fake: TokenLocation & { replaced: typeof replaced } = {
    location: { href },
    history: {
      state,
      replaceState(data, _unused, url) {
        replaced.push({ state: data, url });
        fake.location.href = url;
        fake.history.state = data;
      },
    },
    replaced,
  };
  return fake;
}

describe('takeToken', () => {
  it('returns the token and takes it out of the address bar', () => {
    const p = page('http://127.0.0.1:5173/?token=s3cr3t');
    expect(takeToken(p)).toBe('s3cr3t');
    expect(p.location.href).toBe('http://127.0.0.1:5173/');
    expect(p.location.href).not.toContain('s3cr3t');
  });

  it('replaces the history entry rather than adding one', () => {
    const p = page('http://127.0.0.1:5173/?token=s3cr3t', { kept: true });
    takeToken(p);
    expect(p.replaced).toEqual([
      { state: { kept: true }, url: 'http://127.0.0.1:5173/' },
    ]);
  });

  it('keeps the rest of the URL as it was', () => {
    const p = page('http://127.0.0.1:5173/a/b?x=1&token=s3cr3t&y=2#frag');
    expect(takeToken(p)).toBe('s3cr3t');
    expect(p.location.href).toBe('http://127.0.0.1:5173/a/b?x=1&y=2#frag');
  });

  it('removes every token parameter, not only the first', () => {
    const p = page('http://127.0.0.1:5173/?token=one&token=two');
    expect(takeToken(p)).toBe('one');
    expect(p.location.href).toBe('http://127.0.0.1:5173/');
  });

  it('treats an empty token as none, and still strips it', () => {
    const p = page('http://127.0.0.1:5173/?token=');
    expect(takeToken(p)).toBeNull();
    expect(p.location.href).toBe('http://127.0.0.1:5173/');
  });

  it('returns null and leaves the URL alone when there is no token', () => {
    // The reload after the token was stripped: nothing to take.
    const p = page('http://127.0.0.1:5173/?x=1');
    expect(takeToken(p)).toBeNull();
    expect(p.replaced).toEqual([]);
    expect(p.location.href).toBe('http://127.0.0.1:5173/?x=1');
  });

  it('decodes a token the way the agent encoded it', () => {
    const p = page('http://127.0.0.1:5173/?token=a-b_c%3D');
    expect(takeToken(p)).toBe('a-b_c=');
  });
});
