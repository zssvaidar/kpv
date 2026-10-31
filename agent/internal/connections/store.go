package connections

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
	"github.com/zssvaidar/kpv/agent/internal/secrets"
)

// Connection is one stored connection with its credentials, as the
// in-process read (Store.Get) hands it to #68 and #25. A field that is not
// set is "" or nil. The credentials are secrets.Secret, so formatting,
// logging or marshalling a Connection never shows them.
type Connection struct {
	Name                 string
	BootstrapServers     []string
	SecurityProtocol     string
	SASLMechanism        string
	SASLUsername         string
	SASLPassword         *secrets.Secret
	TLSCACertificate     string
	TLSClientCertificate string
	TLSClientKey         *secrets.Secret
}

// Summary is a stored connection as an answer carries it: everything but the
// credentials, and for each only whether it is set.
type Summary struct {
	Name                 string
	BootstrapServers     []string
	SecurityProtocol     string
	SASLMechanism        string
	SASLUsername         string
	SASLPasswordSet      bool
	TLSCACertificate     string
	TLSClientCertificate string
	TLSClientKeySet      bool
}

// Draft is a request about a connection as httpapi parsed it: Document holds
// every field but the credentials as decoded JSON (strings, []any of
// strings, booleans), Credentials the credentials, wrapped by Credential the
// moment they were read, and Problems what Credential found wrong with them.
type Draft struct {
	Document    map[string]any
	Credentials map[string]secrets.Secret
	Problems    []Problem
}

// credentialFields are the fields that are credentials: write-only, held as
// secrets.Secret, never answered.
var credentialFields = []string{FieldSASLPassword, FieldTLSClientKey}

// IsCredential reports whether field is a credential.
func IsCredential(field string) bool {
	return field == FieldSASLPassword || field == FieldTLSClientKey
}

// standIn takes a present credential's place in a document that is
// validated: the schema and the rules ask only whether it is there, and its
// own rules were checked by Credential.
const standIn = "x"

// Credential wraps the value of a credential field read from a request,
// checking it against its own schema definition and, for the key, that it
// parses as a PEM private key. The caller drops raw at once. A problem never
// quotes the value, its length included.
func Credential(field, raw string) (secrets.Secret, []Problem) {
	secret := secrets.NewSecret(raw)
	definition := definitionPassword
	if field == FieldTLSClientKey {
		definition = definitionKey
	}
	found, err := validateAgainst(definition, raw)
	if err != nil {
		return secret, []Problem{{Pointer: "/" + field, Message: "this could not be checked"}}
	}
	for index := range found {
		found[index].Pointer = "/" + field
	}
	if len(found) == 0 && field == FieldTLSClientKey && !privateKeyParses(raw) {
		found = append(found, Problem{Pointer: "/" + field, Rule: "pem", Message: privateKeyMessage})
	}
	return secret, found
}

// ErrorKind says why a store operation failed.
type ErrorKind string

// The kinds, which are connection.schema.json's connectionsErrorKind.
const (
	KindInvalid       ErrorKind = "invalid"
	KindNotFound      ErrorKind = "notFound"
	KindDuplicateName ErrorKind = "duplicateName"
	KindReferenced    ErrorKind = "referenced"
	KindIO            ErrorKind = "io"
)

// Error is a refused store operation: a kind, and for invalid the problems,
// for referenced the report. It never holds a credential or a path.
type Error struct {
	Kind     ErrorKind
	Problems []Problem
	Report   *Report
}

func (e *Error) Error() string { return "connection: " + string(e.Kind) }

// KindOf is the kind of an Error, and io for anything else.
func KindOf(err error) ErrorKind {
	var refused *Error
	if errors.As(err, &refused) {
		return refused.Kind
	}
	return KindIO
}

// Store is the set of named connections, kept in step with a file of the
// agent's own (projectfs.PrivateFile, beside the roots store), or in memory
// when there is none. It is safe for concurrent use; one change is made at
// a time, the file write and the reference check included, so two creates of
// one name leave one connection.
type Store struct {
	mutex       sync.Mutex
	file        *projectfs.PrivateFile
	pipelines   Lister
	logger      *slog.Logger
	connections []Connection
}

// Open loads the store from file, which may be nil to keep connections in
// memory. A file that cannot be read or is not a valid store is logged by
// kind, the store starts empty, and the file is moved aside before anything
// is written over it; if it cannot be moved, every change answers io until it
// can. pipelines is where the reference check finds pipelines (#48).
func Open(file *projectfs.PrivateFile, pipelines Lister, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	store := &Store{file: file, pipelines: pipelines, logger: logger}
	if file == nil {
		return store
	}
	raw, found, problem := file.Read()
	if problem == "" && found {
		loaded, ok := decodeStore(raw)
		if ok {
			store.connections = loaded
			return store
		}
		problem = "corrupt"
	}
	if problem != "" {
		logger.Warn("the stored connections could not be read, so there are none; the file is moved aside", "kind", string(problem))
		if err := file.SetAside(); err != nil {
			logger.Warn("the unreadable store of connections could not be moved aside yet, so it is not written until it can be", "kind", string(projectfs.KindOf(err)))
		}
	}
	return store
}

// List is every connection without its credentials, sorted by name ignoring
// letter case.
func (s *Store) List() []Summary {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	out := make([]Summary, 0, len(s.connections))
	for _, connection := range s.connections {
		out = append(out, summaryOf(connection))
	}
	return out
}

// Names is the name of every connection, in List's order. Never nil.
func (s *Store) Names() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	out := make([]string, 0, len(s.connections))
	for _, connection := range s.connections {
		out = append(out, connection.Name)
	}
	return out
}

// Get is the in-process read of one connection with its credentials, for
// deploy (#68) and the connection test (#25). The name matches exactly, as a
// pipeline's reference does. No HTTP handler calls it.
func (s *Store) Get(name string) (Connection, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, connection := range s.connections {
		if connection.Name == name {
			return copyOf(connection), true
		}
	}
	return Connection{}, false
}

// Create adds the connection a create request describes.
func (s *Store) Create(draft Draft) (Summary, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	problems, err := validateAgainst(definitionConnection, withStandIns(draft.Document, draft.Credentials, nil))
	if err != nil {
		return Summary{}, s.refuse(KindIO, nil, nil)
	}
	problems = append(problems, draft.Problems...)
	problems = append(problems, ruleProblems(present(withStandIns(draft.Document, draft.Credentials, nil)))...)
	problems = append(problems, certificateProblems(draft.Document)...)
	if len(problems) > 0 {
		return Summary{}, s.refuse(KindInvalid, problems, nil)
	}
	connection := connectionOf(draft.Document, draft.Credentials, nil)
	if s.indexOfIgnoringCase(connection.Name, -1) >= 0 {
		return Summary{}, s.refuse(KindDuplicateName, nil, nil)
	}
	next := append(append([]Connection{}, s.connections...), connection)
	if err := s.commit(next); err != nil {
		return Summary{}, err
	}
	return summaryOf(connection), nil
}

// Update changes the connection an update request names as current. A
// credential left out is kept, unless the new protocol does not carry it; a
// credential given replaces it; one listed in remove is removed. A rename
// that some pipeline names, or that cannot be fully checked, is refused with
// the report unless the request confirms it; no pipeline.yaml is changed.
func (s *Store) Update(draft Draft) (Summary, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	document := draft.Document
	problems, err := validateAgainst(definitionUpdate, withStandIns(document, draft.Credentials, nil))
	if err != nil {
		return Summary{}, s.refuse(KindIO, nil, nil)
	}
	problems = append(problems, draft.Problems...)
	if len(problems) > 0 {
		return Summary{}, s.refuse(KindInvalid, problems, nil)
	}
	current, _ := document["current"].(string)
	index := s.indexOf(current)
	if index < 0 {
		return Summary{}, s.refuse(KindNotFound, nil, nil)
	}
	stored := s.connections[index]
	removed := map[string]bool{}
	if list, ok := document["remove"].([]any); ok {
		for _, item := range list {
			field, _ := item.(string)
			removed[field] = true
			if _, given := draft.Credentials[field]; given {
				problems = append(problems, Problem{Pointer: "/" + field, Rule: "givenAndRemoved",
					Message: "this credential is both given and removed; send one or the other"})
			}
		}
	}
	protocol, _ := document[FieldSecurityProtocol].(string)
	kept := map[string]secrets.Secret{}
	if stored.SASLPassword != nil && !removed[FieldSASLPassword] && AllowsSASL(protocol) {
		kept[FieldSASLPassword] = *stored.SASLPassword
	}
	if stored.TLSClientKey != nil && !removed[FieldTLSClientKey] && AllowsTLS(protocol) {
		kept[FieldTLSClientKey] = *stored.TLSClientKey
	}
	view := withStandIns(document, draft.Credentials, kept)
	delete(view, "current")
	delete(view, "remove")
	delete(view, "confirm")
	problems = append(problems, ruleProblems(present(view))...)
	problems = append(problems, certificateProblems(document)...)
	if len(problems) > 0 {
		return Summary{}, s.refuse(KindInvalid, problems, nil)
	}
	connection := connectionOf(view, draft.Credentials, kept)
	if s.indexOfIgnoringCase(connection.Name, index) >= 0 {
		return Summary{}, s.refuse(KindDuplicateName, nil, nil)
	}
	if connection.Name != stored.Name {
		if report := s.references(stored.Name); !report.Empty() && document["confirm"] != true {
			return Summary{}, s.refuse(KindReferenced, nil, &report)
		}
	}
	next := append([]Connection{}, s.connections...)
	next[index] = connection
	if err := s.commit(next); err != nil {
		return Summary{}, err
	}
	return summaryOf(connection), nil
}

// Delete removes the connection a delete request names, and returns what is
// left. A connection some pipeline names, or whose references cannot all be
// checked, is refused with the report unless the request confirms it.
func (s *Store) Delete(draft Draft) ([]Summary, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	problems, err := validateAgainst(definitionDelete, draft.Document)
	if err != nil {
		return nil, s.refuse(KindIO, nil, nil)
	}
	if len(problems) > 0 || len(draft.Credentials) > 0 {
		return nil, s.refuse(KindInvalid, problems, nil)
	}
	name, _ := draft.Document[FieldName].(string)
	index := s.indexOf(name)
	if index < 0 {
		return nil, s.refuse(KindNotFound, nil, nil)
	}
	if report := s.references(name); !report.Empty() && draft.Document["confirm"] != true {
		return nil, s.refuse(KindReferenced, nil, &report)
	}
	next := append(append([]Connection{}, s.connections[:index]...), s.connections[index+1:]...)
	if err := s.commit(next); err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(next))
	for _, connection := range next {
		out = append(out, summaryOf(connection))
	}
	return out, nil
}

func (s *Store) refuse(kind ErrorKind, problems []Problem, report *Report) error {
	s.logger.Debug("a connection request was refused", "kind", string(kind), "problems", len(problems))
	return &Error{Kind: kind, Problems: problems, Report: report}
}

// commit writes next to the file and only then makes it the store's set, so
// a failed write changes nothing.
func (s *Store) commit(next []Connection) error {
	sort.SliceStable(next, func(i, j int) bool {
		return strings.ToLower(next[i].Name) < strings.ToLower(next[j].Name)
	})
	if s.file != nil {
		body, err := encodeStore(next)
		if err != nil {
			return s.refuse(KindIO, nil, nil)
		}
		if err := s.file.Write(body); err != nil {
			s.logger.Warn("the store of connections could not be written", "kind", string(projectfs.KindOf(err)))
			return s.refuse(KindIO, nil, nil)
		}
	}
	s.connections = next
	return nil
}

func (s *Store) indexOf(name string) int {
	for index, connection := range s.connections {
		if connection.Name == name {
			return index
		}
	}
	return -1
}

// indexOfIgnoringCase finds another connection whose name is name ignoring
// letter case, skipping the one at except.
func (s *Store) indexOfIgnoringCase(name string, except int) int {
	for index, connection := range s.connections {
		if index != except && strings.EqualFold(connection.Name, name) {
			return index
		}
	}
	return -1
}

// withStandIns is document with a stand-in for every credential given or
// kept, which is what the schema and the rules see of them.
func withStandIns(document map[string]any, given, kept map[string]secrets.Secret) map[string]any {
	out := make(map[string]any, len(document)+2)
	for key, value := range document {
		out[key] = value
	}
	for _, field := range credentialFields {
		_, isGiven := given[field]
		_, isKept := kept[field]
		if isGiven || isKept {
			out[field] = standIn
		}
	}
	return out
}

// certificateProblems is every certificate field in document that does not
// parse as PEM certificates.
func certificateProblems(document map[string]any) []Problem {
	var found []Problem
	for _, field := range []string{FieldTLSCACertificate, FieldTLSClientCertificate} {
		text, ok := document[field].(string)
		if ok && text != "" && !certificatesParse(text) {
			found = append(found, Problem{Pointer: "/" + field, Rule: "pem", Message: certificateMessage})
		}
	}
	return found
}

// connectionOf builds a connection from a validated document, taking each
// credential from given, then kept.
func connectionOf(document map[string]any, given, kept map[string]secrets.Secret) Connection {
	text := func(field string) string {
		value, _ := document[field].(string)
		return value
	}
	connection := Connection{
		Name:                 text(FieldName),
		SecurityProtocol:     text(FieldSecurityProtocol),
		SASLMechanism:        text(FieldSASLMechanism),
		SASLUsername:         text(FieldSASLUsername),
		TLSCACertificate:     text(FieldTLSCACertificate),
		TLSClientCertificate: text(FieldTLSClientCertificate),
	}
	if servers, ok := document[FieldBootstrapServers].([]any); ok {
		for _, server := range servers {
			if value, ok := server.(string); ok {
				connection.BootstrapServers = append(connection.BootstrapServers, value)
			}
		}
	}
	pick := func(field string) *secrets.Secret {
		if secret, ok := given[field]; ok {
			return &secret
		}
		if secret, ok := kept[field]; ok {
			return &secret
		}
		return nil
	}
	connection.SASLPassword = pick(FieldSASLPassword)
	connection.TLSClientKey = pick(FieldTLSClientKey)
	return connection
}

func summaryOf(connection Connection) Summary {
	return Summary{
		Name:                 connection.Name,
		BootstrapServers:     append([]string{}, connection.BootstrapServers...),
		SecurityProtocol:     connection.SecurityProtocol,
		SASLMechanism:        connection.SASLMechanism,
		SASLUsername:         connection.SASLUsername,
		SASLPasswordSet:      connection.SASLPassword != nil,
		TLSCACertificate:     connection.TLSCACertificate,
		TLSClientCertificate: connection.TLSClientCertificate,
		TLSClientKeySet:      connection.TLSClientKey != nil,
	}
}

func copyOf(connection Connection) Connection {
	connection.BootstrapServers = append([]string{}, connection.BootstrapServers...)
	return connection
}

// The store file: {"version": 1, "connections": [...]}, each record the
// connection schema's Connection with its credentials in clear, which is why
// the file is 0600 in a 0700 directory outside every project.
const storeVersion = 1

type storeRecord struct {
	Name                 string   `json:"name"`
	BootstrapServers     []string `json:"bootstrapServers"`
	SecurityProtocol     string   `json:"securityProtocol"`
	SASLMechanism        string   `json:"saslMechanism,omitempty"`
	SASLUsername         string   `json:"saslUsername,omitempty"`
	SASLPassword         *string  `json:"saslPassword,omitempty"`
	TLSCACertificate     string   `json:"tlsCaCertificate,omitempty"`
	TLSClientCertificate string   `json:"tlsClientCertificate,omitempty"`
	TLSClientKey         *string  `json:"tlsClientKey,omitempty"`
}

type storeDocument struct {
	Version     int           `json:"version"`
	Connections []storeRecord `json:"connections"`
}

// encodeStore renders the store file. It is the one place in this package
// that calls Reveal: the value goes into the file's bytes and nowhere else.
func encodeStore(connections []Connection) ([]byte, error) {
	document := storeDocument{Version: storeVersion, Connections: make([]storeRecord, 0, len(connections))}
	reveal := func(secret *secrets.Secret) *string {
		if secret == nil {
			return nil
		}
		value := secret.Reveal()
		return &value
	}
	for _, connection := range connections {
		document.Connections = append(document.Connections, storeRecord{
			Name:                 connection.Name,
			BootstrapServers:     connection.BootstrapServers,
			SecurityProtocol:     connection.SecurityProtocol,
			SASLMechanism:        connection.SASLMechanism,
			SASLUsername:         connection.SASLUsername,
			SASLPassword:         reveal(connection.SASLPassword),
			TLSCACertificate:     connection.TLSCACertificate,
			TLSClientCertificate: connection.TLSClientCertificate,
			TLSClientKey:         reveal(connection.TLSClientKey),
		})
	}
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

// decodeStore reads a store file: exactly one document of the shape above,
// every record a valid connection whose PEM text parses, no two names equal
// ignoring case. Anything else is not a store.
func decodeStore(raw []byte) ([]Connection, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document storeDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	if document.Version != storeVersion || document.Connections == nil {
		return nil, false
	}
	out := make([]Connection, 0, len(document.Connections))
	seen := map[string]bool{}
	for _, record := range document.Connections {
		given := map[string]secrets.Secret{}
		var problems []Problem
		for field, value := range map[string]*string{FieldSASLPassword: record.SASLPassword, FieldTLSClientKey: record.TLSClientKey} {
			if value == nil {
				continue
			}
			secret, found := Credential(field, *value)
			given[field] = secret
			problems = append(problems, found...)
		}
		view := map[string]any{
			FieldName: record.Name, FieldSecurityProtocol: record.SecurityProtocol,
		}
		servers := make([]any, 0, len(record.BootstrapServers))
		for _, server := range record.BootstrapServers {
			servers = append(servers, server)
		}
		view[FieldBootstrapServers] = servers
		for field, value := range map[string]string{
			FieldSASLMechanism: record.SASLMechanism, FieldSASLUsername: record.SASLUsername,
			FieldTLSCACertificate: record.TLSCACertificate, FieldTLSClientCertificate: record.TLSClientCertificate,
		} {
			if value != "" {
				view[field] = value
			}
		}
		recordProblems, err := ValidateRecord(withStandIns(view, given, nil))
		if err != nil || len(recordProblems) > 0 || len(problems) > 0 || len(certificateProblems(view)) > 0 {
			return nil, false
		}
		key := strings.ToLower(record.Name)
		if seen[key] {
			return nil, false
		}
		seen[key] = true
		out = append(out, connectionOf(view, given, nil))
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, true
}
