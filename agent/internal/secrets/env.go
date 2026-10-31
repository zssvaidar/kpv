package secrets

import (
	"sort"
	"strings"
)

// The .env format.
//
// A line is blank, a comment (its first non-blank character is "#"), or an
// entry: optional blanks, an optional "export ", a key, optional blanks, "=",
// optional blanks, and a value. A key is an environment variable name,
// [A-Za-z_][A-Za-z0-9_]*. A value is read the way Docker Compose's env_file
// reads it:
//
//   - double-quoted, "...": "\\", "\"" and "\$" are a backslash, a quote and a
//     dollar sign, "\n", "\r" and "\t" are a line feed, a carriage return and a
//     tab, and any other backslash is kept as it is;
//   - single-quoted, '...': literal, except that "\'" is a quote;
//   - unquoted: everything up to " #" (an inline comment), trailing blanks
//     removed.
//
// After a closing quote only blanks and a "#" comment may follow. A value is
// returned as written: "$NAME" references in an unquoted or double-quoted
// value are not expanded, though Compose would expand them. Every other line,
// including the continuation of a quoted value spread over several lines,
// which this package does not read, is malformed and reported by its number.
//
// The quoting rule for what this package writes is one form for every value:
// KEY="...", with "\" written as "\\", a double quote as "\"" and "$" as "\$",
// and every other byte, non-ASCII text included, as it is. Compose reads that
// back to the exact value, with no interpolation, and so does Read. A value
// holding a line feed, a carriage return or a NUL byte cannot be one line and
// is refused.

// EnvFile is the .env's name inside the pipeline directory.
const EnvFile = ".env"

// Entry is one entry of a .env file.
type Entry struct {
	Key   string
	Value Secret
	// Line is the entry's 1-based line number.
	Line int
}

// envLine is what one line of a .env says.
type envLine struct {
	entry bool
	// malformed is a line that is not blank, a comment or an entry. key is
	// still set when the line starts with a key and "=", so a malformed value
	// still counts as setting that key.
	malformed bool
	key       string
	value     string
}

// validKey reports whether key is an environment variable name.
func validKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c == '_' || isAlpha(c) || (i > 0 && isDigit(c)) {
			continue
		}
		return false
	}
	return true
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' }

func trimBlanksLeft(s string) string { return strings.TrimLeft(s, " \t") }

// parseEnvLine reads one line's text.
func parseEnvLine(text string) envLine {
	rest := trimBlanksLeft(text)
	if rest == "" || rest[0] == '#' {
		return envLine{}
	}
	if after, ok := strings.CutPrefix(rest, "export"); ok && after != "" && isBlank(after[0]) {
		rest = trimBlanksLeft(after)
	}
	end := 0
	for end < len(rest) && (rest[end] == '_' || isAlpha(rest[end]) || isDigit(rest[end])) {
		end++
	}
	key := rest[:end]
	rest = trimBlanksLeft(rest[end:])
	if !validKey(key) || rest == "" || rest[0] != '=' {
		return envLine{malformed: true}
	}
	value, ok := parseValue(trimBlanksLeft(rest[1:]))
	if !ok {
		return envLine{malformed: true, key: key}
	}
	return envLine{entry: true, key: key, value: value}
}

func parseValue(raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	quote := raw[0]
	if quote != '"' && quote != '\'' {
		if index := strings.Index(raw, " #"); index >= 0 {
			raw = raw[:index]
		}
		return strings.TrimRight(raw, " \t"), true
	}
	var out strings.Builder
	for i := 1; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == quote:
			trailer := trimBlanksLeft(raw[i+1:])
			if trailer != "" && trailer[0] != '#' {
				return "", false
			}
			return out.String(), true
		case c == '\\' && i+1 < len(raw):
			next := raw[i+1]
			if quote == '\'' {
				if next == '\'' {
					out.WriteByte('\'')
					i++
					continue
				}
				out.WriteByte(c)
				continue
			}
			i++
			switch next {
			case '\\', '"', '$':
				out.WriteByte(next)
			case 'n':
				out.WriteByte('\n')
			case 'r':
				out.WriteByte('\r')
			case 't':
				out.WriteByte('\t')
			default:
				out.WriteByte(c)
				out.WriteByte(next)
			}
		default:
			out.WriteByte(c)
		}
	}
	return "", false
}

// encodeEntry is the line this package writes for key and value.
func encodeEntry(key, value string) string {
	var out strings.Builder
	out.WriteString(key)
	out.WriteString(`="`)
	for i := 0; i < len(value); i++ {
		switch c := value[i]; c {
		case '\\', '"', '$':
			out.WriteByte('\\')
			out.WriteByte(c)
		default:
			out.WriteByte(c)
		}
	}
	out.WriteString(`"`)
	return out.String()
}

// validValue reports whether value can be written as one line.
func validValue(value string) bool {
	return !strings.ContainsAny(value, "\n\r\x00")
}

// envAnalysis is what the lines of a .env say, split by where they are.
type envAnalysis struct {
	entries []Entry
	// user maps each key set outside the block to the lines setting it.
	user map[string][]int
	// block is the entries inside the block, by key.
	block map[string]string
	// blockLines maps each key inside the block to its line.
	blockLines map[string]int
	problems   []Problem
}

func analyzeEnv(lines []line, found block) envAnalysis {
	analysis := envAnalysis{user: map[string][]int{}, block: map[string]string{}, blockLines: map[string]int{}}
	var userKeys []string
	for index := range lines {
		parsed := parseEnvLine(content(lines, index))
		outside := found.outside(index)
		if found.found && (index == found.start || index == found.end) {
			continue
		}
		if parsed.malformed {
			if !outside {
				// A valid digest over a line kpv would never write: the
				// digest was forged or recomputed by hand. Treat it as
				// edited rather than drop the line.
				analysis.problems = append(analysis.problems, Problem{
					Kind: KindBlockEdited, File: EnvFile, Lines: []int{index + 1},
					Message: "this line inside the kpv marker block is not one kpv writes; move it outside the block, then write again",
				})
				continue
			}
			analysis.problems = append(analysis.problems, Problem{
				Kind: KindMalformedLine, Warning: true, File: EnvFile, Lines: []int{index + 1},
				Message: "this line is not a blank line, a comment or a KEY=value entry, so it is not read; it is kept as it is",
			})
		}
		if parsed.key == "" {
			continue
		}
		if parsed.entry {
			analysis.entries = append(analysis.entries, Entry{Key: parsed.key, Value: NewSecret(parsed.value), Line: index + 1})
		}
		if outside {
			if _, seen := analysis.user[parsed.key]; !seen {
				userKeys = append(userKeys, parsed.key)
			}
			analysis.user[parsed.key] = append(analysis.user[parsed.key], index+1)
		} else {
			analysis.block[parsed.key] = parsed.value
			analysis.blockLines[parsed.key] = index + 1
		}
	}
	for _, key := range userKeys {
		if numbers := analysis.user[key]; len(numbers) > 1 {
			analysis.problems = append(analysis.problems, Problem{
				Kind: KindDuplicateKey, Warning: true, File: EnvFile, Lines: numbers, Key: key,
				Message: "this key is set on lines " + lineList(numbers) + "; Compose uses the last one",
			})
		}
		if blockLine, inBlock := analysis.blockLines[key]; inBlock {
			numbers := append(append([]int{}, analysis.user[key]...), blockLine)
			analysis.problems = append(analysis.problems, Problem{
				Kind: KindDuplicateKey, Warning: true, File: EnvFile, Lines: numbers, Key: key,
				Message: "this key is set both by a user line and inside the kpv marker block",
			})
		}
	}
	return analysis
}

// envBody is the block's lines: the block's existing entries with values set
// over them, sorted by key.
func envBody(existing map[string]string, values map[string]Secret) []string {
	merged := make(map[string]string, len(existing)+len(values))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range values {
		merged[key] = value.Reveal()
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	body := make([]string, len(keys))
	for i, key := range keys {
		body[i] = encodeEntry(key, merged[key])
	}
	return body
}
