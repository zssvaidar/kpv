package secrets

import "strings"

// The .gitignore block.
//
// The block ignores two names at the pipeline root:
//
//   - ".env", the file itself
//   - "..env.tmp-*", the temporary files an interrupted .env write leaves
//     behind (projectfs.Path.WriteAtomically names them ".<name>.tmp-<random>")
//
// A pattern goes into the block only when no user line of the file matches
// that name at all. A user line that already ignores it (".env", "/.env",
// "*.env", ".env*", ...) makes a block line redundant, and a user line that
// un-ignores it ("!.env") is the user's decision: the block does not override
// it, wherever the block sits, and a KindNotIgnored warning names the line.
//
// Whether a name is ignored is decided the way git decides it for a file at the
// root of the directory a .gitignore is in: the last pattern that matches
// wins, a "!" pattern un-ignores, a pattern ending in "/" matches only
// directories, a leading "/" anchors to that directory, "**/" matches any
// number of directories, and "*", "?", "[...]" and "\" are wildmatch's. The
// comparison is case-sensitive, which is git's default on Linux; with
// core.ignorecase set, git may ignore names this package does not.
//
// Only the pipeline-root .gitignore is read. A .gitignore in a parent
// directory, .git/info/exclude and the user's global excludes file are not,
// so a .env they ignore is still given a block line here, which is harmless,
// and a .env they un-ignore is not noticed.

// GitignoreFile is the .gitignore's name inside the pipeline directory.
const GitignoreFile = ".gitignore"

// ignoreEntry is one line the block may hold, with a name it must ignore.
type ignoreEntry struct {
	pattern string
	sample  string
}

var ignoreEntries = []ignoreEntry{
	{pattern: EnvFile, sample: EnvFile},
	{pattern: "." + EnvFile + ".tmp-*", sample: "." + EnvFile + ".tmp-0123456789abcdef"},
}

// ignoreRule is one pattern line of a .gitignore.
type ignoreRule struct {
	negate  bool
	dirOnly bool
	pattern string
}

// parseIgnoreLine reads one line's text the way git does. ok is false for a
// blank line or a comment.
func parseIgnoreLine(text string) (ignoreRule, bool) {
	text = trimTrailingSpaces(text)
	if text == "" || text[0] == '#' {
		return ignoreRule{}, false
	}
	var rule ignoreRule
	if text[0] == '!' {
		rule.negate = true
		text = text[1:]
	}
	if strings.HasSuffix(text, "/") {
		rule.dirOnly = true
		text = strings.TrimSuffix(text, "/")
	}
	if text == "" {
		return ignoreRule{}, false
	}
	rule.pattern = text
	return rule, true
}

// trimTrailingSpaces removes trailing spaces that are not escaped with a
// backslash, as git's trim_trailing_spaces does. Tabs are kept.
func trimTrailingSpaces(text string) string {
	lastSpace := -1
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case ' ':
			if lastSpace < 0 {
				lastSpace = i
			}
		case '\\':
			i++
			lastSpace = -1
		default:
			lastSpace = -1
		}
	}
	if lastSpace >= 0 {
		return text[:lastSpace]
	}
	return text
}

// matchesRootFile reports whether the rule matches name, a regular file
// directly in the .gitignore's directory.
func (r ignoreRule) matchesRootFile(name string) bool {
	if r.dirOnly {
		return false
	}
	pattern := strings.TrimPrefix(r.pattern, "/")
	for strings.HasPrefix(pattern, "**/") {
		pattern = pattern[3:]
	}
	return wildmatch(pattern, name)
}

// wildmatch is git's wildmatch with WM_PATHNAME, for a subject with no "/":
// "*" and "**" match any run of characters other than "/", "?" one such
// character, "[...]" a class, "\" escapes the next character, and anything
// else matches itself.
func wildmatch(pattern, name string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			for len(pattern) > 0 && pattern[0] == '*' {
				pattern = pattern[1:]
			}
			for i := 0; i <= len(name); i++ {
				if i > 0 && name[i-1] == '/' {
					return false
				}
				if wildmatch(pattern, name[i:]) {
					return true
				}
			}
			return false
		case '?':
			if name == "" || name[0] == '/' {
				return false
			}
			pattern, name = pattern[1:], name[1:]
		case '[':
			if name == "" {
				return false
			}
			matched, rest, ok := matchClass(pattern, name[0])
			if !ok || !matched {
				return false
			}
			pattern, name = rest, name[1:]
		case '\\':
			if len(pattern) < 2 || name == "" || pattern[1] != name[0] {
				return false
			}
			pattern, name = pattern[2:], name[1:]
		default:
			if name == "" || pattern[0] != name[0] {
				return false
			}
			pattern, name = pattern[1:], name[1:]
		}
	}
	return name == ""
}

// classes are the POSIX character classes wildmatch knows.
var classes = map[string]func(byte) bool{
	"alnum":  func(c byte) bool { return isAlpha(c) || isDigit(c) },
	"alpha":  isAlpha,
	"blank":  func(c byte) bool { return c == ' ' || c == '\t' },
	"cntrl":  func(c byte) bool { return c < 0x20 || c == 0x7f },
	"digit":  isDigit,
	"graph":  func(c byte) bool { return c > 0x20 && c < 0x7f },
	"lower":  func(c byte) bool { return c >= 'a' && c <= 'z' },
	"print":  func(c byte) bool { return c >= 0x20 && c < 0x7f },
	"punct":  func(c byte) bool { return c > 0x20 && c < 0x7f && !isAlpha(c) && !isDigit(c) },
	"space":  func(c byte) bool { return c == ' ' || (c >= '\t' && c <= '\r') },
	"upper":  func(c byte) bool { return c >= 'A' && c <= 'Z' },
	"xdigit": func(c byte) bool { return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') },
}

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// matchClass matches c against the bracket expression pattern starts with,
// returning the pattern after it. ok is false for an unterminated bracket,
// which matches nothing, as in git.
func matchClass(pattern string, c byte) (matched bool, rest string, ok bool) {
	i := 1
	negate := false
	if i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^') {
		negate = true
		i++
	}
	first := true
	for i < len(pattern) {
		ch := pattern[i]
		if ch == ']' && !first {
			return matched != negate, pattern[i+1:], true
		}
		first = false
		switch {
		case ch == '[' && i+1 < len(pattern) && pattern[i+1] == ':':
			end := strings.Index(pattern[i+2:], ":]")
			if end < 0 {
				// Not a class after all; "[" is a literal.
				if c == '[' {
					matched = true
				}
				i++
				continue
			}
			name := pattern[i+2 : i+2+end]
			test, known := classes[name]
			if !known {
				return false, "", false
			}
			if test(c) {
				matched = true
			}
			i += 2 + end + 2
			continue
		case ch == '\\':
			i++
			if i >= len(pattern) {
				return false, "", false
			}
			ch = pattern[i]
		}
		low := ch
		i++
		if i+1 < len(pattern) && pattern[i] == '-' && pattern[i+1] != ']' {
			high := pattern[i+1]
			i += 2
			if high == '\\' {
				if i >= len(pattern) {
					return false, "", false
				}
				high = pattern[i]
				i++
			}
			if c >= low && c <= high {
				matched = true
			}
			continue
		}
		if c == low {
			matched = true
		}
	}
	return false, "", false
}

// ignoreDecision is what the user lines of a .gitignore say about one name.
type ignoreDecision struct {
	// matched is whether any user line matches the name.
	matched bool
	// ignored is whether the last matching user line ignores it.
	ignored bool
	// line is the 1-based number of that last matching line.
	line int
}

// decide applies the last-matching-pattern rule over the user lines.
func decide(lines []line, found block, name string) ignoreDecision {
	var decision ignoreDecision
	for index := range lines {
		if !found.outside(index) {
			continue
		}
		rule, ok := parseIgnoreLine(content(lines, index))
		if !ok || !rule.matchesRootFile(name) {
			continue
		}
		decision = ignoreDecision{matched: true, ignored: !rule.negate, line: index + 1}
	}
	return decision
}

// gitignoreBody is the block's lines for a .gitignore, the warnings about
// user lines that un-ignore what it would ignore, and whether .env ends up
// ignored.
func gitignoreBody(lines []line, found block) (body []string, warnings []Problem, envIgnored bool) {
	envIgnored = true
	for _, entry := range ignoreEntries {
		decision := decide(lines, found, entry.sample)
		switch {
		case !decision.matched:
			body = append(body, entry.pattern)
		case !decision.ignored:
			if entry.sample == EnvFile {
				envIgnored = false
			}
			warnings = append(warnings, Problem{
				Kind: KindNotIgnored, Warning: true, File: GitignoreFile, Lines: []int{decision.line},
				Message: "this line un-ignores " + entry.pattern + ", so " + entry.pattern + " is not ignored; kpv leaves the line as it is",
			})
		}
	}
	return body, warnings, envIgnored
}
