package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// originPolicy is the allowlist a request's Origin header is checked against.
// It holds exact origins, lowercased: there is no wildcard, no suffix match
// and no subdomain rule, because every client the agent has is a page the
// project itself serves on loopback.
type originPolicy struct {
	allowed map[string]struct{}
	// order keeps the allowlist in the order it was configured, for the
	// startup line and for tests. The map is what decides.
	order []string
}

// originHeader is the header the allowlist is checked against.
// secFetchSiteHeader is the fetch metadata that decides the one case where
// that header is absent, and upgradeHeader is what keeps that case away from a
// protocol upgrade.
const (
	originHeader       = "Origin"
	secFetchSiteHeader = "Sec-Fetch-Site"
	upgradeHeader      = "Upgrade"
)

// The rule for a request that carries no Origin header at all.
//
// It is rejected: absence is treated as a disallowed origin, not as
// permission. There is one exception, fetchSiteVouches below, and it is an
// exception for a request the browser itself vouches for -- not a rule that
// absence is permission.
//
// The premise matters as much as the rule, so it is stated as the Fetch
// standard has it rather than as it is convenient to remember. A browser
// attaches Origin to a request whose mode is CORS, which is every fetch() and
// XHR a page makes to another origin; to a request whose method is not GET or
// HEAD, same-origin ones included; and to a WebSocket handshake. It attaches
// none to a same-origin GET or HEAD.
//
// And -- this is the part that decides the rule -- it attaches none to a
// *cross-origin* no-CORS GET either. A hostile page's <img src>, <script src>,
// <link>, <iframe src> and any top-level navigation it drives are all GETs to
// this socket with no Origin header on them. So an absent Origin does not mean
// "not from a hostile page": it is the shape the oldest cross-site attacks
// have. An earlier version of this comment claimed the opposite, that a
// cross-origin request always carries an Origin, and it is wrong for exactly
// the requests an attacker can make without any cooperation from the user.
//
// Why absence is rejected, when the token is required as well:
//
//   - The attack the allowlist exists to stop is a page on some other origin,
//     in the user's browser, reaching this loopback socket. Its CORS fetches
//     carry a disallowed Origin and the allowlist rejects them. Its no-CORS
//     GETs and the navigations it drives carry no Origin, and this rule
//     rejects them. Between the two that is the class, which is why "no
//     Origin, but a valid token" is not the rule adopted here: it would admit
//     the second half of it.
//   - Treating absence as permission turns one omitted header into a way round
//     the check for anything that can reach the socket: a client holding a
//     leaked token would rather send no Origin than a wrong one, and a rule
//     that rewards that is not a check. So: fail closed.
//
// The cost, knowingly: a deliberate caller such as curl has to set a header.
const originRequired = true

// The one exception to originRequired, and the rule for it: a request that
// carries no Origin header is accepted when the browser vouches for where it
// came from, through fetch metadata.
//
// Sec-Fetch-Site is that metadata. A browser sets it on every request it makes
// and a page cannot: it is a forbidden header name, so fetch() and XHR cannot
// set or override it, and there is no way to reach it from a form, an <img> or
// a navigation either. The value describes the relationship between whoever
// initiated the request and this origin, and it is computed over the whole
// redirect chain and downgraded to the least trusted value in it, so a hostile
// page cannot launder a cross-site request into a same-origin one by bouncing
// it through a redirect.
//
// The two values that vouch, and why each is safe to accept:
//
//   - "same-origin": a document on this very origin made the request. The only
//     document this origin can serve is the agent's own page, so this is that
//     page's own GET. It is the request this exception exists for.
//   - "none": the user initiated the request and no document did -- a URL
//     typed in, a bookmark, a link handed over by the operating system. No page
//     can cause one, which is what makes it safe; it is the startup URL being
//     opened, the request that hands the token to the browser in the first
//     place.
//
// "cross-site" and "same-site" do not vouch, and they are what a hostile
// page's requests carry, whether those requests carry an Origin or not. A
// client that sends neither an Origin nor a Sec-Fetch-Site -- curl, a script,
// any non-browser caller, and a browser too old to implement fetch metadata --
// is rejected, the same fail-closed direction as every other case here.
//
// The exception is deliberately narrow, and the narrowing is what a future
// route inherits:
//
//   - It applies only when there is no Origin header at all. A present Origin
//     goes to the allowlist and nothing rescues it, so a present-but-
//     disallowed Origin is never treated like an absent one.
//   - It applies only to GET and HEAD. A browser attaches Origin to every
//     other method, same-origin included, so a state-changing route needs no
//     relaxation and gets none: when a POST is added, its same-origin request
//     carries an Origin and is checked against the allowlist like anything
//     else. A POST with no Origin is not a shape a browser produces.
//   - It does not apply to a request asking to be upgraded. A browser sends
//     Origin on a WebSocket handshake, so the upgrade path in #5 neither needs
//     this nor inherits it.
//   - It is one half of a conjunction the guard does not short-circuit: the
//     token is checked independently and still has to be the right one.
//     Nothing that does not hold the token gains anything here.
//
// The trade-off, stated rather than implied: a caller that is not a browser
// can send Sec-Fetch-Site: same-origin by hand. That gives away nothing that
// was not already given away, because the same caller can send
// Origin: http://127.0.0.1:<port> and be accepted today. The origin check has
// never been able to constrain a non-browser client that holds the token and
// does not pretend to. What this rule keeps is that absence of information is
// never permission: a caller has to assert something, and the one assertion a
// web page could make is the one it is not allowed to make.
//
// What it costs where it bites: a browser without fetch metadata (Chrome
// before 76, Firefox before 90, Safari before 16.4) sends neither header, so
// the page's GET is rejected there rather than served, as is a request whose
// Sec-Fetch-Site an intermediary stripped.
func fetchSiteVouches(value string) bool {
	switch value {
	case "same-origin", "none":
		return true
	default:
		// Anything else, including an empty value, a list of values in one
		// header, a different case and a value a later specification adds.
		return false
	}
}

// vouchedByFetchMetadata reports whether r is the one kind of request with no
// Origin header that is accepted: see fetchSiteVouches for the rule and the
// reasoning. The checks are in the order they are cheap in, and all of them
// have to hold.
func vouchedByFetchMetadata(r *http.Request) bool {
	values := r.Header.Values(secFetchSiteHeader)
	if len(values) != 1 {
		// None, or more than one, which is not something a browser sends and
		// not something to guess the meaning of.
		return false
	}
	if !fetchSiteVouches(values[0]) {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	// An upgrade is a GET, so it would otherwise land in here; a browser sends
	// Origin on a handshake, so there is nothing to relax.
	return len(r.Header.Values(upgradeHeader)) == 0
}

// newOriginPolicy builds the allowlist for a server listening on port.
//
// The server's own origin is always allowed, in both spellings a browser can
// produce for a loopback address, because that is where the agent's own page
// is served from. Anything else has to be configured, and is validated here
// rather than when a request arrives, so a typo fails at startup.
func newOriginPolicy(port int, configured []string) (*originPolicy, error) {
	policy := &originPolicy{allowed: make(map[string]struct{}, len(configured)+2)}
	for _, host := range []string{loopbackHost, "localhost"} {
		policy.add("http://" + host + ":" + strconv.Itoa(port))
	}
	for index, origin := range configured {
		normalized, err := normalizeOrigin(origin)
		if err != nil {
			return nil, &ServerError{
				Op: "new", Kind: KindOrigin,
				Location: "/allowedOrigins/" + strconv.Itoa(index),
				Value:    origin,
				Message:  "the configured allowed origin is not an origin",
				Err:      err,
			}
		}
		policy.add(normalized)
	}
	return policy, nil
}

func (p *originPolicy) add(origin string) {
	if _, already := p.allowed[origin]; already {
		return
	}
	p.allowed[origin] = struct{}{}
	p.order = append(p.order, origin)
}

// origins returns the allowlist, in configuration order.
func (p *originPolicy) origins() []string {
	return append([]string(nil), p.order...)
}

// allowsRequest reports whether r may be answered as far as its origin goes.
// It is the one place a request meets the origin rule, and it is written so
// that a present Origin and an absent one cannot be confused: a request that
// carries the header at all is decided by the allowlist below and by nothing
// else, and only a request that carries none can be vouched for by the
// browser's own fetch metadata.
func (p *originPolicy) allowsRequest(r *http.Request) bool {
	values := r.Header.Values(originHeader)
	if len(values) == 0 && vouchedByFetchMetadata(r) {
		return true
	}
	return p.allows(values)
}

// allows reports whether a request carrying these Origin header values is from
// an allowed origin. The argument is the full header value list rather than a
// single string so that the awkward cases are decided here and not by whoever
// happened to call Header.Get:
//
//   - no Origin header: decided by originRequired, documented above, with the
//     one exception allowsRequest has already applied before calling this
//   - more than one Origin header: rejected, because which one the browser
//     meant is not something to guess, and a request with two is not one a
//     browser sends
//   - the literal "null" origin, which a sandboxed iframe or a file:// page
//     sends: rejected, because it is not in the allowlist and never can be
func (p *originPolicy) allows(values []string) bool {
	switch len(values) {
	case 0:
		return !originRequired
	case 1:
		normalized, err := normalizeOrigin(values[0])
		if err != nil {
			return false
		}
		_, ok := p.allowed[normalized]
		return ok
	default:
		return false
	}
}

// originError is the reason a string is not an origin. The wording reaches the
// operator through a startup failure; it never reaches a client, because what a
// client sent is never quoted back.
type originError string

func (e originError) Error() string { return string(e) }

// normalizeOrigin parses an origin and returns it in the one spelling the
// allowlist stores: lowercase scheme and host, port kept as written, nothing
// else. Everything a URL can carry that an origin cannot is a failure rather
// than something to strip, so a configured value that looks like a rule
// ("http://localhost/*") cannot quietly become a different rule.
func normalizeOrigin(raw string) (string, error) {
	if raw == "" {
		return "", originError("an origin cannot be empty")
	}
	if raw == "null" {
		return "", originError(`"null" is not an origin the allowlist can hold`)
	}
	if strings.ContainsAny(raw, "*? \t\r\n") {
		return "", originError("an origin cannot contain a wildcard or whitespace")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", originError("an origin has to parse as a URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", originError("an origin's scheme has to be http or https")
	}
	if parsed.Host == "" {
		return "", originError("an origin has to name a host")
	}
	if parsed.User != nil {
		return "", originError("an origin cannot carry credentials")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", originError("an origin is a scheme, a host and a port, nothing more")
	}
	return scheme + "://" + strings.ToLower(parsed.Host), nil
}
