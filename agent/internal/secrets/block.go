package secrets

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// The marker block.
//
// Each of the two files holds the tool's entries in one block, between a start
// marker line and an end marker line, and only that block is ever rewritten.
// Both markers are comments in both formats. The end marker carries the
// SHA-256 of the lines between the markers as this package wrote them (each
// line without its line ending, followed by "\n"), which is how a user line
// moved into the block, or an entry edited there, is told apart from the
// tool's own: the lines no longer hash to the marker's digest, and the write is
// refused rather than throwing the user's change away.
//
// For a .env the digest is over lines that hold values, and it sits in the
// same file as those values, so it tells a reader of the file nothing the file
// does not already say. It is never logged or returned.

// StartMarker is the line that opens the block in both files.
const StartMarker = "# >>> kpv managed block: do not edit between these markers >>>"

// endMarkerPrefix opens the end marker line; the digest and " <<<" follow.
const (
	endMarkerPrefix = "# <<< kpv managed block"
	endDigestPrefix = endMarkerPrefix + " sha256="
	endMarkerSuffix = " <<<"
)

// bom is the UTF-8 byte-order mark. A file may start with one; it belongs to
// the first line's bytes and is kept, but is not part of what the line says.
const bom = "\xef\xbb\xbf"

// line is one line of a file: its text without the line ending, and the
// ending itself, "\n", "\r\n", or "" for a last line with no newline. Joining
// text and end for every line gives back the file byte for byte.
type line struct {
	text string
	end  string
}

func splitLines(data string) []line {
	var out []line
	for len(data) > 0 {
		index := strings.IndexByte(data, '\n')
		if index < 0 {
			out = append(out, line{text: data})
			break
		}
		text, end := data[:index], "\n"
		if strings.HasSuffix(text, "\r") {
			text, end = text[:len(text)-1], "\r\n"
		}
		out = append(out, line{text: text, end: end})
		data = data[index+1:]
	}
	return out
}

// content is what a line says: its text without a leading byte-order mark on
// the first line.
func content(lines []line, index int) string {
	if index == 0 {
		return strings.TrimPrefix(lines[0].text, bom)
	}
	return lines[index].text
}

// lineEnding is the file's line ending: the first one it uses, LF for a file
// with none.
func lineEnding(lines []line) string {
	for _, l := range lines {
		if l.end != "" {
			return l.end
		}
	}
	return "\n"
}

// block is where the marker block sits in a file. start and end are the
// indexes of the two marker lines.
type block struct {
	found      bool
	start, end int
	digest     string
}

// body returns the indexes of the lines between the markers.
func (b block) body() (int, int) { return b.start + 1, b.end }

// outside reports whether line index is a user line.
func (b block) outside(index int) bool {
	return !b.found || index < b.start || index > b.end
}

func markerText(lines []line, index int) string {
	return strings.TrimRight(content(lines, index), " \t")
}

func isStart(text string) bool { return text == StartMarker }

func isEnd(text string) bool { return strings.HasPrefix(text, endMarkerPrefix) }

// findBlock locates the one marker block, or reports why there is not exactly
// one. It never guesses: anything but one start followed by one end is a
// KindMarkers problem and the file is left alone.
func findBlock(file string, lines []line) (block, *Problem) {
	var starts, ends []int
	for index := range lines {
		text := markerText(lines, index)
		switch {
		case isStart(text):
			starts = append(starts, index)
		case isEnd(text):
			ends = append(ends, index)
		}
	}
	markers := func(numbers []int, message string) (block, *Problem) {
		return block{}, &Problem{Kind: KindMarkers, File: file, Lines: oneBased(numbers), Message: message}
	}
	switch {
	case len(starts) == 0 && len(ends) == 0:
		return block{}, nil
	case len(starts) > 1 || len(ends) > 1:
		return markers(append(starts, ends...), "the file has more than one kpv marker block; it is left unchanged until only one remains")
	case len(ends) == 0:
		return markers(starts, "the kpv start marker has no end marker; it is left unchanged until the block is closed or removed")
	case len(starts) == 0:
		return markers(ends, "the kpv end marker has no start marker; it is left unchanged until the block is opened or removed")
	case ends[0] < starts[0]:
		return markers([]int{ends[0], starts[0]}, "the kpv end marker comes before the start marker; it is left unchanged until they are in order")
	}
	found := block{found: true, start: starts[0], end: ends[0]}
	text := markerText(lines, found.end)
	if strings.HasPrefix(text, endDigestPrefix) && strings.HasSuffix(text, endMarkerSuffix) {
		found.digest = strings.TrimSuffix(strings.TrimPrefix(text, endDigestPrefix), endMarkerSuffix)
	}
	return found, nil
}

// checkBlock reports a block whose lines no longer hash to its end marker.
func checkBlock(file string, lines []line, found block) *Problem {
	if !found.found {
		return nil
	}
	from, to := found.body()
	body := make([]string, 0, to-from)
	for index := from; index < to; index++ {
		body = append(body, content(lines, index))
	}
	if found.digest == digest(body) {
		return nil
	}
	numbers := make([]int, 0, to-from+2)
	for index := found.start; index <= found.end; index++ {
		numbers = append(numbers, index)
	}
	return &Problem{
		Kind: KindBlockEdited, File: file, Lines: oneBased(numbers),
		Message: "the lines inside the kpv marker block are not the ones kpv wrote there; move your own lines outside the block, then write again",
	}
}

func digest(body []string) string {
	sum := sha256.New()
	for _, text := range body {
		sum.Write([]byte(text))
		sum.Write([]byte{'\n'})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func endMarker(body []string) string {
	return endDigestPrefix + digest(body) + endMarkerSuffix
}

// compose returns the file with its block holding body: the existing block
// rewritten where it is, or a new one appended at the end. Every line outside
// the block is copied byte for byte. The block uses the file's line ending.
//
// A file whose last line has no newline keeps that property: a new block is
// separated from the last line by one line ending, and its end marker then has
// none, and an existing end marker keeps whatever ending it had.
func compose(lines []line, found block, body []string) string {
	ending := lineEnding(lines)
	var out strings.Builder
	writeBlock := func(prefix, lastEnd string) {
		out.WriteString(prefix)
		out.WriteString(StartMarker)
		out.WriteString(ending)
		for _, text := range body {
			out.WriteString(text)
			out.WriteString(ending)
		}
		out.WriteString(endMarker(body))
		out.WriteString(lastEnd)
	}
	if found.found {
		for _, l := range lines[:found.start] {
			out.WriteString(l.text + l.end)
		}
		prefix := ""
		if found.start == 0 && strings.HasPrefix(lines[0].text, bom) {
			prefix = bom
		}
		writeBlock(prefix, lines[found.end].end)
		for _, l := range lines[found.end+1:] {
			out.WriteString(l.text + l.end)
		}
		return out.String()
	}
	for _, l := range lines {
		out.WriteString(l.text + l.end)
	}
	if len(lines) > 0 && lines[len(lines)-1].end == "" {
		writeBlock(ending, "")
		return out.String()
	}
	writeBlock("", ending)
	return out.String()
}

func oneBased(indexes []int) []int {
	out := make([]int, len(indexes))
	for i, index := range indexes {
		out[i] = index + 1
	}
	return out
}

func lineList(numbers []int) string {
	parts := make([]string, len(numbers))
	for i, number := range numbers {
		parts[i] = strconv.Itoa(number)
	}
	return strings.Join(parts, " and ")
}
