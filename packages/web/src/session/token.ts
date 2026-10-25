// Getting the session token out of the page's URL, and out of everywhere else.
//
// The token reaches the page in the `token` query parameter of the URL the
// agent prints at startup; that is the only way it can get here. What the
// page owes it next is to take it out of that URL straight away: out of the
// address bar, so a screenshot or a copied link does not carry it, and out of
// the session history, so Back does not bring it back.
//
// This is load-bearing rather than tidiness. Since #58 the agent serves a
// request with no `Origin` when `Sec-Fetch-Site` is `none`, which is what a
// user opening a link gets: a token that leaked into a pasted link is a link
// that works. Stripping it is what keeps the address bar from being that link.
//
// `history.replaceState` rewrites the current entry in place, so the entry the
// browser loaded with the token in it is gone, not merely followed by a clean
// one; `pushState` would leave it one Back away.
//
// The token is then held in memory only, by whatever the caller hands it to.
// It is never written to localStorage, sessionStorage, a cookie or any other
// storage (ESLint refuses those names in this package), which is why a reload
// after this has run has no token and shows the authentication state.

/** The query parameter the agent puts the token in. */
export const tokenParameter = 'token';

/** The part of `window` this needs, so a test can hand it a fake one. */
export interface TokenLocation {
  location: { href: string };
  history: {
    state: unknown;
    replaceState(data: unknown, unused: string, url: string): void;
  };
}

/**
 * Takes the token out of the page's URL and returns it, or `null` when the URL
 * holds none. Every `token` parameter is removed, even an empty one, and
 * every other part of the URL is kept as it was: other parameters, the path
 * and the fragment, and the history entry's state.
 */
export function takeToken(page: TokenLocation): string | null {
  const url = new URL(page.location.href);
  if (!url.searchParams.has(tokenParameter)) {
    return null;
  }
  const token = url.searchParams.get(tokenParameter);
  url.searchParams.delete(tokenParameter);
  page.history.replaceState(page.history.state, '', url.href);
  return token === null || token === '' ? null : token;
}
