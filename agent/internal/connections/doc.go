// Package connections holds the named connections to external Kafka clusters
// (#24): the record, its combination rules and validation, the store that
// keeps them in the agent's own configuration directory, and the reference
// check that finds which pipelines name one.
//
// A connection's credentials, the SASL password and the TLS client key, are
// secrets.Secret from the moment a request is parsed. The store file write is
// the only place here that calls Reveal; nothing here answers, logs or
// formats a credential. A pipeline names a connection by name only
// (pipeline.schema.json, the connection property of its Kafka blocks), and
// nothing here writes a pipeline.yaml.
package connections
