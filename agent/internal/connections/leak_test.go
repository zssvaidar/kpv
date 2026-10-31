// No credential escapes the store (#24): a known password and private key go
// through every operation and every failure, and neither appears in an
// error, a problem, a summary, a formatted or marshalled value, or a log
// line. The store file is the one place they are written.

package connections

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestNoCredentialAppearsInAnyAnswerErrorOrLogLine(t *testing.T) {
	t.Parallel()
	w, _ := referencedWorld(t)
	p := testPEMs(t)
	keyBody := strings.Split(p.key, "\n")[1]
	var seen []any
	record := func(values ...any) { seen = append(seen, values...) }

	with := func(overrides map[string]any) map[string]any {
		out := saslRecord(t, "leak")
		for key, value := range overrides {
			if value == nil {
				delete(out, key)
				continue
			}
			out[key] = value
		}
		return out
	}
	attempts := []func() (any, error){
		func() (any, error) { return w.store.Create(draft(with(nil))) },
		func() (any, error) { return w.store.Create(draft(with(nil))) }, // duplicate
		func() (any, error) {
			return w.store.Create(draft(with(map[string]any{"securityProtocol": "PLAINTEXT"})))
		},
		func() (any, error) { return w.store.Create(draft(with(map[string]any{"name": "x y"}))) },
		func() (any, error) {
			return w.store.Create(draft(with(map[string]any{"name": "k", "tlsClientKey": knownPassword})))
		},
		func() (any, error) {
			return w.store.Create(draft(with(map[string]any{"name": "k2", "saslPassword": strings.Repeat(knownPassword, 100)})))
		},
		func() (any, error) {
			return w.store.Create(draft(with(map[string]any{"name": "k3", "tlsCaCertificate": p.key})))
		},
		func() (any, error) {
			return w.store.Update(draft(update("leak", with(nil), map[string]any{"remove": []any{"saslPassword"}})))
		},
		func() (any, error) {
			return w.store.Update(draft(update("leak", with(map[string]any{"securityProtocol": "SSL"}), nil)))
		},
		func() (any, error) { return w.store.Update(draft(update("missing", with(nil), nil))) },
		func() (any, error) {
			return w.store.Update(draft(update("leak", with(map[string]any{"name": "eu-cluster"}), nil)))
		},
		func() (any, error) {
			return w.store.Update(draft(update("eu-cluster", saslRecord(t, "renamed"), nil)))
		},
		func() (any, error) { return w.store.Update(draft(update("leak", with(nil), nil))) },
		func() (any, error) { return w.store.Delete(draft(map[string]any{"name": "eu-cluster"})) },
		func() (any, error) { return w.store.Delete(draft(map[string]any{"name": "missing"})) },
		func() (any, error) { return w.store.List(), nil },
		func() (any, error) { return w.store.Names(), nil },
		func() (any, error) { connection, _ := w.store.Get("leak"); return connection, nil },
	}
	for _, attempt := range attempts {
		answer, err := attempt()
		record(answer)
		if err != nil {
			record(err, err.Error())
			if refused, ok := err.(*Error); ok {
				for _, problem := range refused.Problems {
					record(problem.Message, problem)
				}
			}
		}
	}
	// Every store failure: a directory has taken the store file's place, so
	// every write fails.
	if err := os.Remove(w.file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(w.file, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := w.store.Create(draft(with(map[string]any{"name": "io"})))
	record(err)
	if KindOf(err) != KindIO {
		t.Fatalf("a failed write = %v", err)
	}
	_, err = w.store.Update(draft(update("leak", with(nil), nil)))
	record(err)
	_, err = w.store.Delete(draft(map[string]any{"name": "leak", "confirm": true}))
	record(err)
	if KindOf(err) != KindIO {
		t.Fatalf("a failed delete = %v", err)
	}
	if err := os.Remove(w.file); err != nil {
		t.Fatal(err)
	}
	// A corrupt store holding the credentials is logged by kind only.
	writeFile(t, w.file, "{"+knownPassword+p.key)
	w.open()

	for index, value := range seen {
		for _, rendering := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			for _, secret := range []string{knownPassword, keyBody} {
				if strings.Contains(rendering, secret) {
					t.Errorf("value %d shows a credential: %.200s", index, rendering)
				}
			}
		}
		if marshalled, err := json.Marshal(value); err == nil {
			for _, secret := range []string{knownPassword, keyBody} {
				if strings.Contains(string(marshalled), secret) {
					t.Errorf("value %d marshals a credential", index)
				}
			}
		}
	}
	logs := w.logs.String()
	if logs == "" {
		t.Fatal("nothing was logged, so this test sees nothing")
	}
	for _, secret := range []string{knownPassword, keyBody} {
		if strings.Contains(logs, secret) {
			t.Error("a log line holds a credential")
		}
	}
}
