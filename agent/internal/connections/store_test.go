package connections

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
	"github.com/zssvaidar/kpv/agent/internal/pipeline"
)

func TestACreatedConnectionIsListedWithoutItsCredentialsAndReadInProcessWithThem(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	p := testPEMs(t)
	summary, err := w.store.Create(draft(saslRecord(t, "eu-cluster")))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !summary.SASLPasswordSet || !summary.TLSClientKeySet || summary.SASLUsername != knownUsername {
		t.Fatalf("summary %+v", summary)
	}
	if got := w.store.Names(); len(got) != 1 || got[0] != "eu-cluster" {
		t.Fatalf("Names = %v", got)
	}
	connection, ok := w.store.Get("eu-cluster")
	if !ok || connection.SASLPassword == nil || connection.SASLPassword.Reveal() != knownPassword ||
		connection.TLSClientKey == nil || connection.TLSClientKey.Reveal() != p.key {
		t.Fatalf("Get = %v %v", connection, ok)
	}
	if _, ok := w.store.Get("EU-cluster"); ok {
		t.Fatal("the in-process read matches the name exactly, as a pipeline reference does")
	}
	if printed := fmt.Sprintf("%+v %#v", connection, connection); strings.Contains(printed, knownPassword) {
		t.Fatal("formatting a Connection prints its password")
	}
}

func TestAnEmptyStoreHasAnEmptyNameList(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if names := w.store.Names(); names == nil || len(names) != 0 {
		t.Fatalf("Names = %#v", names)
	}
	if list := w.store.List(); list == nil || len(list) != 0 {
		t.Fatalf("List = %#v", list)
	}
}

func TestConnectionsPersistBesideTheRootsStoreAsAnOwnerOnlyFile(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if _, err := w.store.Create(draft(saslRecord(t, "eu-cluster"))); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(w.file)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("store file %v %v", info, err)
	}
	directory, err := os.Stat(filepath.Dir(w.file))
	if err != nil || directory.Mode().Perm() != 0o700 {
		t.Fatalf("store directory %v %v", directory.Mode(), err)
	}
	w.open()
	connection, ok := w.store.Get("eu-cluster")
	if !ok || connection.SASLPassword.Reveal() != knownPassword {
		t.Fatalf("after a restart: %v %v", connection, ok)
	}
	// The store sits outside every root: the directories holding it cannot be
	// authorized.
	for _, holder := range []string{filepath.Dir(w.file), filepath.Join(w.home, ".config")} {
		if _, err := w.roots.Authorize(holder); err == nil {
			t.Fatalf("%s holds the connection store and was authorized", holder)
		}
	}
}

func TestWithoutAConfigurationDirectoryConnectionsLastAsLongAsTheProcess(t *testing.T) {
	t.Parallel()
	store := Open(nil, nil, nil)
	if _, err := store.Create(draft(saslRecord(t, "memory"))); err != nil {
		t.Fatal(err)
	}
	if names := store.Names(); len(names) != 1 {
		t.Fatalf("Names = %v", names)
	}
}

func TestTwoConnectionsCannotShareANameIgnoringCase(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if _, err := w.store.Create(draft(saslRecord(t, "Kafka"))); err != nil {
		t.Fatal(err)
	}
	if _, err := w.store.Create(draft(saslRecord(t, "kafka"))); kindOf(t, err) != KindDuplicateName {
		t.Fatalf("a create of the same name in another case = %v", err)
	}
	if _, err := w.store.Create(draft(saslRecord(t, "other"))); err != nil {
		t.Fatal(err)
	}
	record := saslRecord(t, "KAFKA")
	if _, err := w.store.Update(draft(update("other", record, nil))); kindOf(t, err) != KindDuplicateName {
		t.Fatalf("a rename onto an existing name = %v", err)
	}
	// Changing only the letter case of one's own name is not a clash.
	if _, err := w.store.Update(draft(update("Kafka", saslRecord(t, "KAFKA"), nil))); err != nil {
		t.Fatalf("a case-only rename of itself: %v", err)
	}
}

func TestTwoConcurrentCreatesOfOneNameLeaveOneConnection(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	const attempts = 16
	var wait sync.WaitGroup
	results := make([]error, attempts)
	for index := range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			name := "shared"
			if index%2 == 1 {
				name = "SHARED"
			}
			_, results[index] = w.store.Create(draft(saslRecord(t, name)))
		}()
	}
	wait.Wait()
	created := 0
	for _, err := range results {
		switch kindOf(t, err) {
		case "":
			created++
		case KindDuplicateName:
		default:
			t.Fatalf("unexpected %v", err)
		}
	}
	if created != 1 || len(w.store.Names()) != 1 {
		t.Fatalf("created %d, names %v", created, w.store.Names())
	}
	w.open()
	if len(w.store.Names()) != 1 {
		t.Fatalf("the file holds %v", w.store.Names())
	}
}

func TestAnInvalidConnectionIsRefusedOnTheFieldsItBreaks(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	record := saslRecord(t, "a")
	record["securityProtocol"] = "PLAINTEXT"
	record["bootstrapServers"] = []string{"broker:0", "broker:0"}
	_, err := w.store.Create(draft(record))
	var refused *Error
	if kindOf(t, err) != KindInvalid || !asError(err, &refused) {
		t.Fatalf("Create = %v", err)
	}
	pointers := map[string]bool{}
	for _, problem := range refused.Problems {
		pointers[problem.Pointer] = true
	}
	for _, want := range []string{"/bootstrapServers/0", "/saslMechanism", "/saslPassword", "/tlsCaCertificate", "/tlsClientKey"} {
		if !pointers[want] {
			t.Errorf("no problem at %s in %+v", want, refused.Problems)
		}
	}
	if len(w.store.Names()) != 0 {
		t.Fatal("an invalid connection was stored")
	}
}

func asError(err error, target **Error) bool {
	refused, ok := err.(*Error)
	if ok {
		*target = refused
	}
	return ok
}

func TestPEMTextThatDoesNotParseIsRefusedOnItsFieldWithoutQuotingIt(t *testing.T) {
	t.Parallel()
	p := testPEMs(t)
	const marker = "MIIBnotreallyacertificateQUOTEME"
	garbage := "-----BEGIN CERTIFICATE-----\n" + marker + "\n-----END CERTIFICATE-----\n"
	cases := []struct {
		name, field, value string
	}{
		{"a certificate that is not one", "tlsCaCertificate", garbage},
		{"a key where a certificate is expected", "tlsClientCertificate", p.key},
		{"a certificate where a key is expected", "tlsClientKey", p.certificate},
		{"text around a certificate", "tlsCaCertificate", "junk " + marker + "\n" + p.certificate},
		{"plain text as a key", "tlsClientKey", marker},
		{"two keys", "tlsClientKey", p.key + p.key},
		{"an encrypted key", "tlsClientKey", "-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIIB" + marker + "\n-----END ENCRYPTED PRIVATE KEY-----\n"},
	}
	for _, c := range cases {
		w := newWorld(t)
		record := saslRecord(t, "a")
		record[c.field] = c.value
		_, err := w.store.Create(draft(record))
		var refused *Error
		if !asError(err, &refused) || refused.Kind != KindInvalid {
			t.Fatalf("%s: Create = %v", c.name, err)
		}
		found := false
		for _, problem := range refused.Problems {
			if problem.Pointer == "/"+c.field {
				found = true
			}
			if strings.Contains(problem.Message, marker) || strings.Contains(problem.Message, "BEGIN") {
				t.Errorf("%s: the message quotes the text: %s", c.name, problem.Message)
			}
		}
		if !found {
			t.Errorf("%s: no problem at /%s in %+v", c.name, c.field, refused.Problems)
		}
	}
	// A PKCS #1 RSA key and a certificate bundle are accepted.
	w := newWorld(t)
	record := saslRecord(t, "rsa")
	record["tlsClientKey"] = p.rsaKey
	record["tlsCaCertificate"] = p.certificate + "\n" + p.otherCertificate
	if _, err := w.store.Create(draft(record)); err != nil {
		t.Fatalf("an RSA key and a bundle: %v", err)
	}
}

func TestEditingWithoutRetypingACredentialKeepsIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if _, err := w.store.Create(draft(saslRecord(t, "a"))); err != nil {
		t.Fatal(err)
	}
	edited := update("a", saslRecord(t, "a"), map[string]any{"saslPassword": nil, "tlsClientKey": nil, "saslUsername": "someone-else"})
	summary, err := w.store.Update(draft(edited))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !summary.SASLPasswordSet || !summary.TLSClientKeySet || summary.SASLUsername != "someone-else" {
		t.Fatalf("summary %+v", summary)
	}
	connection, _ := w.store.Get("a")
	if connection.SASLPassword.Reveal() != knownPassword || connection.TLSClientKey.Reveal() != testPEMs(t).key {
		t.Fatal("a kept credential changed")
	}
}

func TestAnUpdateReplacesOrRemovesACredentialOnlyWhenItSaysSo(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if _, err := w.store.Create(draft(saslRecord(t, "a"))); err != nil {
		t.Fatal(err)
	}
	replaced := update("a", saslRecord(t, "a"), map[string]any{"saslPassword": "new-password", "tlsClientKey": nil})
	if _, err := w.store.Update(draft(replaced)); err != nil {
		t.Fatal(err)
	}
	if connection, _ := w.store.Get("a"); connection.SASLPassword.Reveal() != "new-password" {
		t.Fatal("the password was not replaced")
	}
	// An empty string is refused, never read as remove.
	empty := update("a", saslRecord(t, "a"), map[string]any{"saslPassword": "", "tlsClientKey": nil})
	if _, err := w.store.Update(draft(empty)); kindOf(t, err) != KindInvalid {
		t.Fatalf("an empty password = %v", err)
	}
	if connection, _ := w.store.Get("a"); connection.SASLPassword.Reveal() != "new-password" {
		t.Fatal("an empty password changed the stored one")
	}
	// Given and removed at once is refused.
	both := update("a", saslRecord(t, "a"), map[string]any{"tlsClientKey": nil, "remove": []any{"saslPassword"}})
	if _, err := w.store.Update(draft(both)); kindOf(t, err) != KindInvalid {
		t.Fatalf("given and removed = %v", err)
	}
	// Removing the client key with its certificate is a valid connection.
	removed := update("a", saslRecord(t, "a"), map[string]any{
		"saslPassword": nil, "tlsClientKey": nil, "tlsClientCertificate": nil, "remove": []any{"tlsClientKey"},
	})
	summary, err := w.store.Update(draft(removed))
	if err != nil {
		t.Fatalf("remove the key: %v", err)
	}
	if summary.TLSClientKeySet || !summary.SASLPasswordSet {
		t.Fatalf("summary %+v", summary)
	}
	// Removing the password a SASL protocol needs is refused on that field.
	noPassword := update("a", saslRecord(t, "a"), map[string]any{
		"saslPassword": nil, "tlsClientKey": nil, "tlsClientCertificate": nil, "remove": []any{"saslPassword"},
	})
	_, err = w.store.Update(draft(noPassword))
	var refused *Error
	if !asError(err, &refused) || refused.Kind != KindInvalid || refused.Problems[0].Pointer != "/saslPassword" {
		t.Fatalf("remove a needed password = %v", err)
	}
}

func TestChangingTheProtocolDropsTheCredentialsItNoLongerCarries(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if _, err := w.store.Create(draft(saslRecord(t, "a"))); err != nil {
		t.Fatal(err)
	}
	// SASL_SSL to SASL_PLAINTEXT: TLS goes, SASL stays.
	plain := update("a", saslRecord(t, "a"), map[string]any{
		"securityProtocol": "SASL_PLAINTEXT", "saslPassword": nil,
		"tlsClientKey": nil, "tlsClientCertificate": nil, "tlsCaCertificate": nil,
	})
	summary, err := w.store.Update(draft(plain))
	if err != nil {
		t.Fatalf("to SASL_PLAINTEXT: %v", err)
	}
	if summary.TLSClientKeySet || !summary.SASLPasswordSet {
		t.Fatalf("summary %+v", summary)
	}
	// SASL_PLAINTEXT to PLAINTEXT: SASL goes too.
	none := map[string]any{"current": "a", "name": "a", "bootstrapServers": []string{"b:1"}, "securityProtocol": "PLAINTEXT"}
	summary, err = w.store.Update(draft(none))
	if err != nil {
		t.Fatalf("to PLAINTEXT: %v", err)
	}
	if summary.SASLPasswordSet || summary.TLSClientKeySet {
		t.Fatalf("summary %+v", summary)
	}
	// Back to SASL_SSL without retyping them is refused where they are missing.
	back := update("a", saslRecord(t, "a"), map[string]any{"saslPassword": nil, "tlsClientKey": nil, "tlsClientCertificate": nil})
	_, err = w.store.Update(draft(back))
	var refused *Error
	if !asError(err, &refused) || refused.Kind != KindInvalid || refused.Problems[0].Pointer != "/saslPassword" {
		t.Fatalf("switching back without retyping = %v", err)
	}
	if connection, _ := w.store.Get("a"); connection.SASLPassword != nil {
		t.Fatal("the dropped password came back")
	}
}

func TestRenamingKeepsTheCredentials(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if _, err := w.store.Create(draft(saslRecord(t, "old"))); err != nil {
		t.Fatal(err)
	}
	renamed := update("old", saslRecord(t, "new"), map[string]any{"saslPassword": nil, "tlsClientKey": nil})
	if _, err := w.store.Update(draft(renamed)); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.store.Get("old"); ok {
		t.Fatal("the old name is still there")
	}
	connection, ok := w.store.Get("new")
	if !ok || connection.SASLPassword.Reveal() != knownPassword || connection.TLSClientKey == nil {
		t.Fatalf("renamed %v %v", connection, ok)
	}
}

func TestUpdatingOrDeletingAConnectionThatDoesNotExistIsNotFound(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if _, err := w.store.Update(draft(update("missing", saslRecord(t, "missing"), nil))); kindOf(t, err) != KindNotFound {
		t.Fatalf("Update = %v", err)
	}
	if _, err := w.store.Delete(draft(map[string]any{"name": "missing"})); kindOf(t, err) != KindNotFound {
		t.Fatalf("Delete = %v", err)
	}
}

func TestACorruptStoreIsLoggedByKindStartsEmptyAndIsMovedAside(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"not JSON":        "{" + knownPassword,
		"another version": `{"version": 2, "connections": []}`,
		"an invalid record": `{"version": 1, "connections": [{"name": "a", "bootstrapServers": ["b:1"], ` +
			`"securityProtocol": "PLAINTEXT", "saslPassword": "` + knownPassword + `"}]}`,
		"two names in two cases": `{"version": 1, "connections": [` +
			`{"name": "a", "bootstrapServers": ["b:1"], "securityProtocol": "PLAINTEXT"},` +
			`{"name": "A", "bootstrapServers": ["b:1"], "securityProtocol": "PLAINTEXT"}]}`,
	} {
		w := newWorld(t)
		writeFile(t, w.file, content)
		w.open()
		if len(w.store.Names()) != 0 {
			t.Fatalf("%s: started with %v", name, w.store.Names())
		}
		logs := w.logs.String()
		if !strings.Contains(logs, "kind=corrupt") || strings.Contains(logs, knownPassword) || strings.Contains(logs, w.home) {
			t.Fatalf("%s: logs %s", name, logs)
		}
		backups, _ := filepath.Glob(w.file + ".corrupt-*")
		if len(backups) != 1 {
			t.Fatalf("%s: backups %v", name, backups)
		}
		if kept, _ := os.ReadFile(backups[0]); string(kept) != content {
			t.Fatalf("%s: the backup is not the old file", name)
		}
		if _, err := w.store.Create(draft(saslRecord(t, "fresh"))); err != nil {
			t.Fatalf("%s: a create after: %v", name, err)
		}
	}
}

func TestAStoreThatCannotBeMovedAsideIsNotWrittenOverAndASaveAnswersIO(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads and renames anything")
	}
	w := newWorld(t)
	writeFile(t, w.file, "{broken")
	directory := filepath.Dir(w.file)
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })
	w.open()
	if !strings.Contains(w.logs.String(), "could not be moved aside") {
		t.Fatalf("logs %s", w.logs.String())
	}
	if _, err := w.store.Create(draft(saslRecord(t, "a"))); kindOf(t, err) != KindIO {
		t.Fatalf("Create = %v", err)
	}
	if len(w.store.Names()) != 0 {
		t.Fatal("a failed save changed the store")
	}
	_ = os.Chmod(directory, 0o700)
	if raw, _ := os.ReadFile(w.file); string(raw) != "{broken" {
		t.Fatalf("the unreadable store was written over: %q", raw)
	}
	if _, err := w.store.Create(draft(saslRecord(t, "a"))); err != nil {
		t.Fatalf("once it can be moved: %v", err)
	}
}

func TestTheStoreFileHoldsTheCredentialsAndNoPipelineFileDoes(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root := w.root("code")
	pipelineFile := filepath.Join(root, "orders", catalog.FileName)
	writeFile(t, pipelineFile, pipelineYAML("orders", "eu-cluster", "eu-cluster"))
	if _, err := w.store.Create(draft(saslRecord(t, "eu-cluster"))); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(w.file)
	if err != nil || !strings.Contains(string(raw), knownPassword) {
		t.Fatalf("the store does not hold the password: %v", err)
	}
	var document storeDocument
	if err := json.Unmarshal(raw, &document); err != nil || document.Version != 1 {
		t.Fatalf("store %s", raw)
	}
	// Saving the pipeline that names the connection writes the name and
	// nothing else of it.
	parsed, _, err := pipeline.ParseYAML([]byte(pipelineYAML("orders", "eu-cluster", "eu-cluster")))
	if err != nil {
		t.Fatal(err)
	}
	written, err := pipeline.MarshalYAML(parsed)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{knownPassword, knownUsername, "broker-1", "BEGIN", "SCRAM"} {
		if strings.Contains(string(written), leaked) {
			t.Errorf("the pipeline file holds %q", leaked)
		}
	}
	if !strings.Contains(string(written), "connection: eu-cluster") {
		t.Errorf("the pipeline file lost the name:\n%s", written)
	}
}
