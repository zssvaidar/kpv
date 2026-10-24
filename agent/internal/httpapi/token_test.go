// The session token: where it comes from, that it is different every time, and
// that it refuses to print itself. These are internal tests because the
// constant-time comparison is not, and should not be, part of the package's
// exported surface.

package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func newTokenOrFail(t *testing.T) Token {
	t.Helper()
	token, err := NewToken()
	if err != nil {
		t.Fatalf("mint a token: %v", err)
	}
	return token
}

// A fresh value per run is the point of the token, so this mints many of them
// the way separate runs do and insists every one is new. One non-empty token
// would prove nothing: a hardcoded constant would pass that.
func TestEveryTokenIsFresh(t *testing.T) {
	const runs = 256
	seen := make(map[string]int, runs)
	for run := range runs {
		token := newTokenOrFail(t)
		if token.Secret() == "" {
			t.Fatalf("run %d minted an empty token", run)
		}
		if earlier, repeated := seen[token.Secret()]; repeated {
			t.Fatalf("run %d minted the same token as run %d, so it is not fresh per run", run, earlier)
		}
		seen[token.Secret()] = run
	}
	if len(seen) != runs {
		t.Fatalf("%d runs produced %d distinct tokens", runs, len(seen))
	}
}

// Freshness is only worth having if each token carries real entropy, so this
// checks the encoded value decodes back to the full 256 bits rather than to a
// few bytes padded out to look long.
func TestATokenCarries256BitsOfEntropy(t *testing.T) {
	token := newTokenOrFail(t)
	raw, err := base64.RawURLEncoding.DecodeString(token.Secret())
	if err != nil {
		t.Fatalf("the token %d characters long is not unpadded base64url: %v", len(token.Secret()), err)
	}
	if len(raw) != tokenBytes {
		t.Fatalf("the token decodes to %d bytes of entropy, want %d", len(raw), tokenBytes)
	}
	if bytes.Equal(raw, make([]byte, tokenBytes)) {
		t.Fatal("the token decodes to all zero bytes, which is not a random source")
	}
}

// Two tokens minted one after the other must not share a prefix of any length
// worth mentioning, which a counter or a timestamp source would.
func TestTwoTokensDoNotShareAPrefix(t *testing.T) {
	first, second := newTokenOrFail(t).Secret(), newTokenOrFail(t).Secret()
	shared := 0
	for shared < len(first) && shared < len(second) && first[shared] == second[shared] {
		shared++
	}
	if shared > 4 {
		t.Fatalf("two consecutive tokens share a %d-character prefix, so the source looks sequential", shared)
	}
}

func TestTheRightTokenMatchesAndNothingElseDoes(t *testing.T) {
	token := newTokenOrFail(t)
	if !token.matches(token.Secret()) {
		t.Fatal("the token does not match its own secret")
	}

	secret := token.Secret()
	flipped := []byte(secret)
	flipped[len(flipped)-1] ^= 'a' ^ 'b'
	// Every case is a different shape of wrong: empty, far too short, the
	// right length with one character changed, a prefix, the secret with
	// something appended, and something absurdly long. None may match, and
	// none takes a different path through the comparison than any other.
	wrong := map[string]string{
		"empty":                                 "",
		"one character":                         "x",
		"right length, one byte different":      string(flipped),
		"a prefix of the secret":                secret[:len(secret)-1],
		"the secret with a suffix":              secret + "x",
		"a megabyte of padding":                 strings.Repeat("x", 1<<20),
		"the secret plus a megabyte of padding": secret + strings.Repeat("x", 1<<20),
	}
	for name, presented := range wrong {
		if token.matches(presented) {
			t.Errorf("%s matched the token", name)
		}
	}
}

func TestTheZeroTokenMatchesNothing(t *testing.T) {
	var unset Token
	for _, presented := range []string{"", unset.Secret(), "anything", newTokenOrFail(t).Secret()} {
		if unset.matches(presented) {
			t.Errorf("the zero token matched %q", presented)
		}
	}
}

// The token is the one secret the agent has. The type is supposed to make
// leaking it take a deliberate call to Secret, so every ordinary way a value
// gets rendered has to come out redacted.
func TestATokenRefusesToRenderItself(t *testing.T) {
	token := newTokenOrFail(t)
	secret := token.Secret()

	rendered := map[string]string{
		"%v":     fmt.Sprintf("%v", token),
		"%s":     fmt.Sprintf("%s", token),
		"%q":     fmt.Sprintf("%q", token),
		"%+v":    fmt.Sprintf("%+v", token),
		"String": token.String(),
	}
	for verb, out := range rendered {
		if strings.Contains(out, secret) {
			t.Errorf("%s rendered the secret", verb)
		}
		if !strings.Contains(out, redacted) {
			t.Errorf("%s rendered %q, want it redacted", verb, out)
		}
	}

	marshalled, err := json.Marshal(struct{ Token Token }{token})
	if err != nil {
		t.Fatalf("marshal a token to JSON: %v", err)
	}
	if strings.Contains(string(marshalled), secret) {
		t.Errorf("marshalling a token to JSON wrote the secret: %s", marshalled)
	}

	var logged bytes.Buffer
	slog.New(slog.NewTextHandler(&logged, nil)).Info("startup", "token", token)
	if strings.Contains(logged.String(), secret) {
		t.Errorf("logging a token wrote the secret: %s", logged.String())
	}
}
