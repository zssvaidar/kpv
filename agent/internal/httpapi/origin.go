package httpapi

import (
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

// The rule for a request that carries no Origin header at all.
//
// It is rejected: absence is treated as a disallowed origin, not as
// permission. The premise matters as much as the rule, so it is stated as the
// specification has it rather than as it is convenient to remember.
//
// What a browser actually sends: it attaches Origin to every cross-origin
// request, and to a same-origin request whose method is not GET or HEAD. It
// does not attach one to a same-origin GET. So an absent Origin means one of
// three things: a client that is not a browser, a same-origin GET from a page
// the agent itself serves, or something that stripped the header. It does not
// mean a cross-origin request: those always carry one.
//
// Why reject anyway:
//
//   - The attack the allowlist exists to stop is a page on some other origin,
//     in the user's browser, making requests to this loopback socket. Such a
//     request carries an Origin by construction and a disallowed one, so the
//     allowlist rejects it whatever the rule for absence is. Rejecting absence
//     buys nothing there; it is about everything else.
//   - Treating absence as permission turns one omitted header into a way round
//     the check for anything that can reach the socket: a client holding a
//     leaked token would rather send no Origin than a wrong one, and a rule
//     that rewards that is not a check. So: fail closed.
//   - The one legitimate no-Origin browser request, a same-origin GET, does
//     not exist yet. The agent serves no page of its own: #4 adds no route
//     that could, and nothing here reads the filesystem.
//
// The cost, knowingly: a deliberate caller such as curl has to set the header,
// and once the agent does serve its own page, that page's same-origin GET
// would be rejected with nothing in front of it to supply an Origin. Which way
// that should go -- accept an absent Origin when the token is valid, or key on
// Sec-Fetch-Site -- is #58, and it is deliberately not decided here.
const originRequired = true

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

// allows reports whether a request carrying these Origin header values is from
// an allowed origin. The argument is the full header value list rather than a
// single string so that the awkward cases are decided here and not by whoever
// happened to call Header.Get:
//
//   - no Origin header: decided by originRequired, documented above
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
