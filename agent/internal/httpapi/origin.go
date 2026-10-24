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
// It is rejected. Absence is treated as a disallowed origin, not as
// permission, and this is the deliberate choice the two defensible ones:
//
//   - The only client this API is built for is the browser session the startup
//     URL opens, and a browser attaches Origin to every fetch, XHR and
//     WebSocket handshake it sends here. So a request with no Origin is, by
//     construction, not that client.
//   - The alternative rule, "no Origin means a non-browser client, which the
//     same-origin policy cannot be tricked into making a request, so allow
//     it", is defensible and is what many local servers do. It is rejected
//     here because it turns one omitted header into a way around the check:
//     anything that can reach the loopback socket and knows the token would
//     rather send no Origin than a wrong one, and a rule that rewards that is
//     not a check. A deliberate caller such as curl can set the header.
//
// The cost is paid knowingly: a plain `curl http://127.0.0.1:port/...` without
// an Origin header is rejected even with the right token, and anything
// scripted against this API has to send one.
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
