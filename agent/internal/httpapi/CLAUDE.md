# internal/httpapi

The agent's HTTP server: `net/http` and `http.ServeMux`, no framework.
`agent/cmd/agent` binds it, prints the startup lines and serves. What this
package may import is in `agent/CLAUDE.md`.

Adding a route

- `newHandler` in `routes.go` is the only place a mux exists, and it returns
  it wrapped in the one `guard`. Request-and-answer routes that read nothing
  are rows in `apiRoutes()` (`/api/health`, `/api/version`); the channel,
  `/api/directories` and `/api/roots` are registered in `newHandler` itself,
  because the row tests hold every row to answering `200` with a JSON object
- `TestOnlyNewHandlerBuildsAMuxAndItWrapsIt`,
  `TestTheServedHandlerIsExactlyTheGuardedOne` (`source_test.go`) and
  `TestANewlyAddedRouteCannotSkipTheChecks` (`guard_test.go`) fail a route
  registered any other way; `channel_test.go` fails when something is
  registered that no test covers. A new endpoint gets its own rejection matrix
  test, as `directories_test.go` and `roots_test.go` have
- an error answers a kind and a fixed sentence from the table in its handler
  file (`directories.go`, `roots.go`, which also hold the status per kind),
  never text from the underlying error: a `projectfs` refusal can describe what
  is outside the roots
- a request body is `application/json` and bounded; `/api/roots` reads it
  token by token (at most 4096 bytes, one object, `path` once and nothing
  else), because `encoding/json` keeps the last of two keys and matches keys
  case-insensitively (`roots.go`)

The checks every request gets (`guard.go`, `origin.go`, `guard_test.go`,
`origin_test.go`)

- the token is presented as `Authorization: Bearer` or the `token` query
  parameter, which is how the startup URL carries it
- `Origin` must be exactly the agent's own origin or one in
  `Options.AllowedOrigins`. A request with no `Origin` is rejected, except
  (#58) a `GET`/`HEAD` with no `Upgrade` whose single `Sec-Fetch-Site` is
  `same-origin` or `none`; the token is still required. So `POST`, `DELETE`
  and the WebSocket handshake always need an allowed `Origin`.
  `originRequired` and `fetchSiteVouches` are the rule, and `rule_test.go`
  fails when their code and the comment beside them disagree, so change both
- every failed check, and an unauthorized request to a path that does not
  exist, gets the same `404` with the same body and headers, and both checks
  run before either is acted on (`TestEveryRejectionIsTheSameAnswer`). A
  refused WebSocket upgrade is turned into the same `404` by `handshakeWriter`
  in `channel.go`. `packages/web/src/api/client.ts` relies on this
- `DisableGeneralOptionsHandler` is set because `net/http` answered `OPTIONS *`
  ahead of the guard; `bypass_test.go` sends raw requests to keep any request
  shape from getting a `2xx` without the token

The server and the token

- it binds `127.0.0.1` on port 0, and `New` binds before returning, so `Addr`,
  `Port` and `URL` are known before anything is served. Timeouts and a header
  cap below `net/http`'s default are set in `server.go`
- the token is 256 bits from `crypto/rand`, compared with
  `subtle.ConstantTimeCompare` over digests (`source_test.go`). It is written
  only on the startup line, never to the log
- a `Token` cannot be printed: `Format`, `LogValue` and `MarshalText` cover
  `fmt`, `slog` and `encoding/json`, and the plaintext lives in a closure, not a
  field, because `fmt` prints an unexported field without calling its methods.
  Do not add a field that holds it (`token.go`, `token_test.go`)
- `Shutdown` and `Close` close the channel first, because `net/http` neither
  closes nor waits for a hijacked connection
