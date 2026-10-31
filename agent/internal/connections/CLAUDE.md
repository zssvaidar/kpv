# internal/connections

Named connections to external Kafka clusters (#24): the record and its
validation, the store, and the reference check. The package comment in
`doc.go` says what it rests on. What it may import is in `agent/CLAUDE.md`
(`source_test.go`).

The record (`validate.go`, `rules.go`, `pem.go`)

- the shapes are `schema/connection.schema.json`; this package embeds the copy
  generation writes beside it and has no generated structs, because the
  generated `Connection` holds the credentials as strings
- the combination rules are `rules.go` here and
  `packages/schema/src/connection-rules.ts`, held to the same answers by
  `schema/fixtures/connections.json` (`fixtures_test.go`). A rule sees only
  which fields are present, so a kept credential stands in as present
  (`standIn`) without being revealed
- PEM text is checked here only: certificates are one or more `CERTIFICATE`
  blocks that parse, a key exactly one unencrypted PKCS #8, PKCS #1 or SEC 1
  block, and nothing but whitespace around them
- no message quotes a value, its length included: the validator's own wording
  is replaced for the kinds that could (`messageOf`)

Credentials

- `saslPassword` and `tlsClientKey` are `secrets.Secret` from the moment
  httpapi reads them (`Credential`, which also checks their own schema
  definition and the key's PEM). `encodeStore` is the only caller of `Reveal`
  (`TestOnlyTheStoreFileWriteRevealsACredential`); `Get` is the in-process
  read for #68 and #25, and no HTTP handler calls it
- an update keeps a credential it leaves out, replaces one it gives, removes
  one it lists in `remove` (given and listed is refused), and drops the stored
  password for a protocol without SASL and the stored key for one without TLS
- `leak_test.go` sends a known password and key through every operation and
  failure and looks for them in every answer, error, rendering and log line

The store (`store.go`)

- one file, `connections.json` beside `roots.json`, handed out by
  `projectfs.OpenPrivateFile`, so the rule that no root may hold the roots
  store's directory covers it. `{"version": 1, "connections": [...]}`, 0600
  in a 0700 directory. No config directory: `Open(nil, ...)`, memory only
- a file that cannot be read, or holds anything but valid records with
  distinct names, is logged by kind, the store starts empty, and it is moved
  aside first (`PrivateFile.SetAside`); while it cannot be, a save answers
  `io` and nothing is written
- one mutex covers every change, the write and the reference check included,
  so two creates of one name leave one. Names are unique ignoring case; `Get`
  and references match exactly. A failed write changes nothing in memory

The reference check (`references.go`)

- it reads #48's `catalog.Catalog.List` (`Lister`), never a walk of its own.
  A readable entry is checked on its source and destination `connection`; an
  unreadable entry is unchecked with the list's kind; an unavailable root is
  `rootUnavailable` and a truncated one `truncated`
- a delete, or a rename, with any reference or unchecked place is refused
  `referenced` with the report and changes nothing; the same request with
  `confirm` goes ahead, and the pipelines keep the old name. Nothing here
  writes a `pipeline.yaml`
- tests use a scratch home, roots store and catalog (`helpers_test.go`)
