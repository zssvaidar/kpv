package connections

import (
	"strconv"
	"strings"
)

// The combination rules of a connection, which schema/connection.schema.json
// states in its descriptions and not as keywords, because the Zod generator
// drops if/then. packages/schema/src/connection-rules.ts implements the same
// rules, and schema/fixtures/connections.json holds the two to the same
// answers (fixtures_test.go here, connection-fixtures.test.ts there).
//
// A rule looks only at which fields are present, never at a credential's
// value, which is what lets the store check the connection an update makes
// while its kept credentials stay secrets.Secret.

// The security protocols.
const (
	ProtocolPlaintext     = "PLAINTEXT"
	ProtocolSSL           = "SSL"
	ProtocolSASLPlaintext = "SASL_PLAINTEXT"
	ProtocolSASLSSL       = "SASL_SSL"
)

// The record's field names, as the schema spells them.
const (
	FieldName                 = "name"
	FieldBootstrapServers     = "bootstrapServers"
	FieldSecurityProtocol     = "securityProtocol"
	FieldSASLMechanism        = "saslMechanism"
	FieldSASLUsername         = "saslUsername"
	FieldSASLPassword         = "saslPassword"
	FieldTLSCACertificate     = "tlsCaCertificate"
	FieldTLSClientCertificate = "tlsClientCertificate"
	FieldTLSClientKey         = "tlsClientKey"
)

// The rule ids the shared fixtures name.
const (
	RuleSASLNotAllowed            = "saslNotAllowed"
	RuleSASLRequired              = "saslRequired"
	RuleTLSNotAllowed             = "tlsNotAllowed"
	RuleClientKeyRequired         = "clientKeyRequired"
	RuleClientCertificateRequired = "clientCertificateRequired"
	RuleDuplicateBootstrapServer  = "duplicateBootstrapServer"
)

var (
	saslFields = []string{FieldSASLMechanism, FieldSASLUsername, FieldSASLPassword}
	tlsFields  = []string{FieldTLSCACertificate, FieldTLSClientCertificate, FieldTLSClientKey}
	fieldWords = map[string]string{
		FieldSASLMechanism:        "a SASL mechanism",
		FieldSASLUsername:         "a SASL username",
		FieldSASLPassword:         "a SASL password",
		FieldTLSCACertificate:     "a CA certificate",
		FieldTLSClientCertificate: "a client certificate",
		FieldTLSClientKey:         "a client key",
	}
)

// AllowsSASL reports whether a protocol carries SASL, so allows and needs the
// SASL fields.
func AllowsSASL(protocol string) bool {
	return protocol == ProtocolSASLPlaintext || protocol == ProtocolSASLSSL
}

// AllowsTLS reports whether a protocol uses TLS, so allows the TLS fields.
func AllowsTLS(protocol string) bool {
	return protocol == ProtocolSSL || protocol == ProtocolSASLSSL
}

func knownProtocol(protocol string) bool {
	switch protocol {
	case ProtocolPlaintext, ProtocolSSL, ProtocolSASLPlaintext, ProtocolSASLSSL:
		return true
	}
	return false
}

// shape is what a rule sees of a connection: the protocol, the bootstrap
// servers, and which of the other fields are present.
type shape struct {
	protocol string
	servers  []any
	has      map[string]bool
}

// present is the shape of a decoded JSON document.
func present(object map[string]any) shape {
	s := shape{has: map[string]bool{}}
	s.protocol, _ = object[FieldSecurityProtocol].(string)
	s.servers, _ = object[FieldBootstrapServers].([]any)
	for field := range fieldWords {
		_, s.has[field] = object[field]
	}
	return s
}

// ruleProblems is every combination rule the shape breaks, in the order the
// TypeScript side reports them.
func ruleProblems(s shape) []Problem {
	var found []Problem
	if knownProtocol(s.protocol) {
		for _, field := range saslFields {
			switch {
			case AllowsSASL(s.protocol) && !s.has[field]:
				found = append(found, Problem{Pointer: "/" + field, Rule: RuleSASLRequired,
					Message: s.protocol + " needs " + fieldWords[field]})
			case !AllowsSASL(s.protocol) && s.has[field]:
				found = append(found, Problem{Pointer: "/" + field, Rule: RuleSASLNotAllowed,
					Message: fieldWords[field] + " cannot be used with " + s.protocol + ", which carries no SASL; choose SASL_PLAINTEXT or SASL_SSL"})
			}
		}
		if !AllowsTLS(s.protocol) {
			for _, field := range tlsFields {
				if s.has[field] {
					found = append(found, Problem{Pointer: "/" + field, Rule: RuleTLSNotAllowed,
						Message: fieldWords[field] + " cannot be used with " + s.protocol + ", which does not use TLS; choose SSL or SASL_SSL"})
				}
			}
		}
	}
	if s.has[FieldTLSClientCertificate] && !s.has[FieldTLSClientKey] {
		found = append(found, Problem{Pointer: "/" + FieldTLSClientKey, Rule: RuleClientKeyRequired,
			Message: "a client certificate needs its private key"})
	}
	if s.has[FieldTLSClientKey] && !s.has[FieldTLSClientCertificate] {
		found = append(found, Problem{Pointer: "/" + FieldTLSClientCertificate, Rule: RuleClientCertificateRequired,
			Message: "a client key needs its certificate"})
	}
	seen := map[string]bool{}
	for index, server := range s.servers {
		text, ok := server.(string)
		if !ok {
			continue
		}
		key := strings.ToLower(text)
		if seen[key] {
			found = append(found, Problem{Pointer: "/" + FieldBootstrapServers + "/" + strconv.Itoa(index),
				Rule: RuleDuplicateBootstrapServer, Message: "this bootstrap server is already listed"})
		}
		seen[key] = true
	}
	return found
}
