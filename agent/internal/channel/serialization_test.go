// Writes for one connection are serialized. There are two halves to proving
// that, and neither is enough on its own.
//
// The behavioural half: many goroutines publish at once, under -race, and the
// client gets every message whole. That is necessary but it is not sufficient,
// and it is worth being precise about why. The library takes a lock of its own
// around each frame it writes, so even a connection that was written to from
// several goroutines would not interleave two single-frame messages, and this
// test would pass while the property it is named after had been removed. It
// would start failing the day a message was written in pieces -- a streamed
// writer, a compressed message split across frames -- which is exactly the
// kind of change nobody expects to break delivery.
//
// The structural half, then: this package's syntax tree says that the only
// thing which writes to a socket is the write loop. That is the property, it
// is checked where it is visible, and moving a write anywhere else fails here
// rather than years later.

package channel

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strings"
	"sync"
	"testing"
)

// The one function allowed to write a frame, and the methods that write one.
const theOnlyWriter = "writeLoop"

// writing lists the connection's methods that put bytes on the wire, and what
// each one is. Close is in here as a thing that must not be called at all: it
// writes a close frame, which would be a second writer, and CloseNow -- which
// closes the socket and writes nothing -- is what this package uses instead.
var writing = map[string]string{
	"Write":  "writes a message frame",
	"Writer": "opens a streaming message writer",
	"Ping":   "writes a ping frame",
}

func TestTheWriteLoopIsTheOnlyThingThatWritesToASocket(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read this package's directory: %v", err)
	}
	fileSet := token.NewFileSet()
	found := 0
	parsed := 0

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fileSet, name, nil, parser.ParseComments)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		parsed++

		functions := map[*ast.FuncDecl]bool{}
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok {
				functions[function] = true
			}
		}
		enclosing := func(position token.Pos) string {
			for function := range functions {
				if position >= function.Pos() && position <= function.End() {
					return function.Name.Name
				}
			}
			return "no function"
		}

		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			var rendered strings.Builder
			if err := printer.Fprint(&rendered, fileSet, call.Fun); err != nil {
				t.Fatalf("print a call in %s: %v", name, err)
			}
			written := rendered.String()
			// Only calls on the connection's websocket, which is the one
			// socket this package has. Anything else named Write belongs to
			// something else.
			method, isWebSocketCall := strings.CutPrefix(written, "c.ws.")
			if !isWebSocketCall {
				return true
			}
			if method == "Close" {
				t.Errorf("%s calls c.ws.Close in %s, which writes a close frame; this package closes with CloseNow, so that the write loop is the only thing that ever writes",
					name, enclosing(call.Pos()))
				return true
			}
			what, writes := writing[method]
			if !writes {
				return true
			}
			found++
			if in := enclosing(call.Pos()); in != theOnlyWriter {
				t.Errorf("%s %s in %s; every frame is written by %s and by nothing else, or two concurrent sends can interleave",
					written, what, in, theOnlyWriter)
			}
			return true
		})
	}

	if parsed < 3 {
		t.Fatalf("only %d source files parsed, so this test is looking at the wrong thing", parsed)
	}
	if found == 0 {
		t.Fatalf("no call that writes a frame was found at all, so this test is no longer checking anything; the names it looks for are %v on c.ws", writing)
	}
}

// Many goroutines publishing at once, and every message arrives whole. The
// payloads are large and each is a single repeated character, so a frame that
// had been interleaved with another would show up as a payload with two
// characters in it, as JSON that will not parse, or as a message that never
// arrives at all.
//
// The queue is made large enough to hold the whole burst, so that this test is
// about serialization and not about the slow-client disconnect, which has its
// own tests.
func TestConcurrentPublishersCannotInterleaveFrames(t *testing.T) {
	t.Parallel()

	const (
		publishers = 8
		each       = 50
		size       = 4096
	)
	hub := startChannel(t, Options{OutboundBuffer: publishers * each * 2})
	client := hub.dial(t)
	subscribe(t, client, "alpha")

	var ready sync.WaitGroup
	var start sync.WaitGroup
	start.Add(1)
	for publisher := range publishers {
		ready.Add(1)
		go func() {
			defer ready.Done()
			// Every publisher waits for the same signal, so they are really
			// publishing at the same time rather than one after another.
			start.Wait()
			letter := string(rune('a' + publisher))
			for index := range each {
				hub.Publish(ChannelMessage{
					Kind:       ChannelMessageKindLog,
					PipelineID: "alpha",
					Payload: ChannelPayload{
						"line":  strings.Repeat(letter, size),
						"index": index,
					},
				})
			}
		}()
	}
	start.Done()

	seen := make(map[string]int, publishers*each)
	for range publishers * each {
		message, raw := receive(t, client)
		if message.Kind != ChannelMessageKindLog || message.PipelineID != "alpha" {
			t.Fatalf("a frame arrived as %v %q, which is not what was published: %q", message.Kind, message.PipelineID, raw)
		}
		line := lineOf(t, message)
		if len(line) != size {
			t.Fatalf("a payload arrived %d characters long, want %d: two frames were interleaved", len(line), size)
		}
		if trimmed := strings.Trim(line, line[:1]); trimmed != "" {
			t.Fatalf("a payload arrived with more than one character in it, so two frames were interleaved: %q", trimmed)
		}
		index, ok := message.Payload["index"].(float64)
		if !ok {
			t.Fatalf("a payload arrived without its index: %q", raw)
		}
		seen[fmt.Sprintf("%s-%d", line[:1], int(index))]++
	}
	ready.Wait()

	if len(seen) != publishers*each {
		t.Fatalf("%d distinct messages arrived, want %d; a frame was lost or duplicated", len(seen), publishers*each)
	}
	for key, count := range seen {
		if count != 1 {
			t.Fatalf("the message %s arrived %d times", key, count)
		}
	}
}
