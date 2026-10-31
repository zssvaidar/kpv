package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/zssvaidar/kpv/agent/internal/connections"
	"github.com/zssvaidar/kpv/agent/internal/secrets"
)

// The connection endpoints (#24): list the stored Kafka connections without
// their credentials, list their names, create, update and delete one. They
// are registered by newHandler beside the pipeline endpoints, inside the same
// wrap, for the reason directoriesPattern gives. Every shape they take and
// answer is generated from schema/connection.schema.json, but the request
// bodies are read token by token into a connections.Draft, never into the
// generated Connection or ConnectionUpdate, which hold credentials as strings
// (source_test.go). A credential is a secrets.Secret from the moment its
// token is read, and no answer carries one: a summary says only whether each
// is set.
//
// POST, PUT and DELETE change state, so #58's relaxation does not reach them,
// as for rootsAuthorizePattern.
const (
	connectionsListPattern   = "GET /api/connections"
	connectionsNamesPattern  = "GET /api/connections/names"
	connectionsCreatePattern = "POST /api/connections"
	connectionsUpdatePattern = "PUT /api/connections"
	connectionsDeletePattern = "DELETE /api/connections"
)

// maxConnectionRequestBytes caps a create, update or delete body: three PEM
// fields of at most 65536 characters each, the bootstrap servers and a few
// short values fit well inside 256 KiB.
const maxConnectionRequestBytes = 256 << 10

// connectionsFailure is the whole of a failed connection request's answer: a
// status, a kind and a fixed sentence.
type connectionsFailure struct {
	status  int
	message string
}

// connectionsFailures is what each kind answers. Nothing from an underlying
// error is passed on and no credential ever is; the additions are an invalid
// request's problems, each a field pointer and a sentence that quotes no
// value, and a referenced refusal's report of the pipelines it concerns.
var connectionsFailures = map[ConnectionsErrorKind]connectionsFailure{
	ConnectionsErrorKindInvalid: {http.StatusBadRequest,
		"send one JSON object as application/json, at most 262144 bytes, each field once and spelled exactly, describing a valid connection"},
	ConnectionsErrorKindNotFound: {http.StatusNotFound,
		"there is no connection with this name"},
	ConnectionsErrorKindDuplicateName: {http.StatusConflict,
		"another connection already has this name, ignoring letter case"},
	ConnectionsErrorKindReferenced: {http.StatusConflict,
		"pipelines name this connection, or could not all be checked; nothing was changed, and sending the request again with confirm goes ahead"},
	ConnectionsErrorKindIo: {http.StatusInternalServerError,
		"the connections could not be stored"},
}

// The fields each body may carry.
var (
	connectionRecordFields = map[string]bool{
		connections.FieldName: true, connections.FieldBootstrapServers: true, connections.FieldSecurityProtocol: true,
		connections.FieldSASLMechanism: true, connections.FieldSASLUsername: true, connections.FieldSASLPassword: true,
		connections.FieldTLSCACertificate: true, connections.FieldTLSClientCertificate: true, connections.FieldTLSClientKey: true,
	}
	connectionUpdateFields = withFields(connectionRecordFields, "current", "remove", "confirm")
	connectionDeleteFields = map[string]bool{connections.FieldName: true, "confirm": true}
)

func withFields(base map[string]bool, more ...string) map[string]bool {
	out := make(map[string]bool, len(base)+len(more))
	for field := range base {
		out[field] = true
	}
	for _, field := range more {
		out[field] = true
	}
	return out
}

// connectionsListRoute answers GET /api/connections.
func (g *guard) connectionsListRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if g.connections == nil {
			reject(w)
			return
		}
		writeJSONStatus(w, http.StatusOK, connectionListOf(g.connections.List()))
	}
}

// connectionsNamesRoute answers GET /api/connections/names, which #12's
// select is filled from.
func (g *guard) connectionsNamesRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if g.connections == nil {
			reject(w)
			return
		}
		names := g.connections.Names()
		answer := ConnectionNameList{Names: make([]ConnectionName, 0, len(names))}
		for _, name := range names {
			answer.Names = append(answer.Names, ConnectionName(name))
		}
		writeJSONStatus(w, http.StatusOK, answer)
	}
}

// connectionsCreateRoute answers POST /api/connections.
func (g *guard) connectionsCreateRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.connections == nil {
			reject(w)
			return
		}
		draft, ok := readConnectionRequest(r, connectionRecordFields)
		if !ok {
			writeConnectionsError(w, &connections.Error{Kind: connections.KindInvalid})
			return
		}
		summary, err := g.connections.Create(draft)
		if err != nil {
			writeConnectionsError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusOK, summaryOf(summary))
	}
}

// connectionsUpdateRoute answers PUT /api/connections.
func (g *guard) connectionsUpdateRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.connections == nil {
			reject(w)
			return
		}
		draft, ok := readConnectionRequest(r, connectionUpdateFields)
		if !ok {
			writeConnectionsError(w, &connections.Error{Kind: connections.KindInvalid})
			return
		}
		summary, err := g.connections.Update(draft)
		if err != nil {
			writeConnectionsError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusOK, summaryOf(summary))
	}
}

// connectionsDeleteRoute answers DELETE /api/connections with what is left.
func (g *guard) connectionsDeleteRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.connections == nil {
			reject(w)
			return
		}
		draft, ok := readConnectionRequest(r, connectionDeleteFields)
		if !ok {
			writeConnectionsError(w, &connections.Error{Kind: connections.KindInvalid})
			return
		}
		remaining, err := g.connections.Delete(draft)
		if err != nil {
			writeConnectionsError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusOK, connectionListOf(remaining))
	}
}

// writeConnectionsError answers a failed connection request with its kind's
// status and sentence, plus an invalid request's problems or a referenced
// refusal's report. Anything that is not a connections.Error is io.
func writeConnectionsError(w http.ResponseWriter, err error) {
	kind := ConnectionsErrorKind(connections.KindOf(err))
	failure, known := connectionsFailures[kind]
	if !known {
		kind = ConnectionsErrorKindIo
		failure = connectionsFailures[kind]
	}
	answer := ConnectionsError{Error: kind, Message: failure.message}
	var refused *connections.Error
	if errors.As(err, &refused) {
		if kind == ConnectionsErrorKindInvalid {
			answer.Problems = make([]ConnectionProblem, 0, len(refused.Problems))
			for _, problem := range refused.Problems {
				answer.Problems = append(answer.Problems, ConnectionProblem{Pointer: problem.Pointer, Message: problem.Message})
			}
		}
		if kind == ConnectionsErrorKindReferenced && refused.Report != nil {
			answer.References = make([]ConnectionReference, 0, len(refused.Report.References))
			for _, reference := range refused.Report.References {
				answer.References = append(answer.References, ConnectionReference{
					Root: reference.Root, Directory: optional(reference.Directory),
					PipelineID: reference.PipelineID, Block: ConnectionBlock(reference.Block),
				})
			}
			answer.Unchecked = make([]UncheckedPlace, 0, len(refused.Report.Unchecked))
			for _, place := range refused.Report.Unchecked {
				answer.Unchecked = append(answer.Unchecked, UncheckedPlace{
					Root: place.Root, Directory: optional(place.Directory), Reason: UncheckedReason(place.Reason),
				})
			}
		}
	}
	writeJSONStatus(w, failure.status, answer)
}

func optional(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}

func connectionListOf(summaries []connections.Summary) ConnectionList {
	out := ConnectionList{Connections: make([]ConnectionSummary, 0, len(summaries))}
	for _, summary := range summaries {
		out.Connections = append(out.Connections, summaryOf(summary))
	}
	return out
}

// summaryOf turns a stored connection's summary into the generated answer.
// It has no credential to carry: connections.Summary holds only whether each
// is set.
func summaryOf(summary connections.Summary) ConnectionSummary {
	out := ConnectionSummary{
		Name:             ConnectionName(summary.Name),
		SecurityProtocol: SecurityProtocol(summary.SecurityProtocol),
		BootstrapServers: make(BootstrapServers, 0, len(summary.BootstrapServers)),
		SaslPasswordSet:  summary.SASLPasswordSet,
		TlsClientKeySet:  summary.TLSClientKeySet,
	}
	for _, server := range summary.BootstrapServers {
		out.BootstrapServers = append(out.BootstrapServers, BootstrapServer(server))
	}
	if summary.SASLMechanism != "" {
		mechanism := SaslMechanism(summary.SASLMechanism)
		out.SaslMechanism = &mechanism
	}
	if summary.SASLUsername != "" {
		username := SaslUsername(summary.SASLUsername)
		out.SaslUsername = &username
	}
	if summary.TLSCACertificate != "" {
		certificate := CertificatePem(summary.TLSCACertificate)
		out.TlsCaCertificate = &certificate
	}
	if summary.TLSClientCertificate != "" {
		certificate := CertificatePem(summary.TLSClientCertificate)
		out.TlsClientCertificate = &certificate
	}
	return out
}

// readConnectionRequest reads a create, update or delete body into a draft,
// or false for anything that is not one JSON object of the allowed fields:
// a Content-Type other than application/json (with at most a UTF-8 charset),
// a body over the cap or not UTF-8, a field not allowed, spelled in another
// case or given twice, an object anywhere inside, or anything after the
// object. A body over the cap is refused without being read further or
// echoed. Whether the values make a valid connection is the store's to say.
//
// The object is read token by token, for the reason readRootRequest gives,
// and so that a credential's string token is wrapped in a secrets.Secret
// (connections.Credential) the moment it is read and never sits in a
// decoded document.
func readConnectionRequest(r *http.Request, allowed map[string]bool) (connections.Draft, bool) {
	draft := connections.Draft{Document: map[string]any{}, Credentials: map[string]secrets.Secret{}}
	if !isJSONContentType(r.Header.Values("Content-Type")) {
		return draft, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxConnectionRequestBytes+1))
	if err != nil || len(body) > maxConnectionRequestBytes || !utf8.Valid(body) {
		return draft, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if opening, err := decoder.Token(); err != nil || opening != json.Delim('{') {
		return draft, false
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		key, isString := token.(string)
		if err != nil || !isString || !allowed[key] || seen[key] {
			return draft, false
		}
		seen[key] = true
		if connections.IsCredential(key) {
			value, err := decoder.Token()
			text, isText := value.(string)
			if err != nil || !isText {
				return draft, false
			}
			secret, problems := connections.Credential(key, text)
			draft.Credentials[key] = secret
			draft.Problems = append(draft.Problems, problems...)
			continue
		}
		value, ok := readFlatValue(decoder)
		if !ok {
			return draft, false
		}
		draft.Document[key] = value
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return draft, false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return draft, false
	}
	return draft, true
}

// readFlatValue reads one value that is a scalar or an array of scalars,
// which is every non-credential field's shape and a little more, so the
// schema can say what is wrong with a mistyped one. An object, or an array
// inside an array, is refused here: none is ever valid, and reading one
// would decode keys this reader has not checked.
func readFlatValue(decoder *json.Decoder) (any, bool) {
	token, err := decoder.Token()
	if err != nil {
		return nil, false
	}
	switch token {
	case json.Delim('{'):
		return nil, false
	case json.Delim('['):
		items := []any{}
		for decoder.More() {
			item, err := decoder.Token()
			if err != nil {
				return nil, false
			}
			if _, isDelimiter := item.(json.Delim); isDelimiter {
				return nil, false
			}
			items = append(items, item)
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim(']') {
			return nil, false
		}
		return items, true
	}
	if _, isDelimiter := token.(json.Delim); isDelimiter {
		return nil, false
	}
	return token, true
}
