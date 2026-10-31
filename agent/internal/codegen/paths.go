package codegen

import (
	"strings"
)

// Reason says why a path was refused or a file was not written, in a word a
// caller can branch on.
type Reason string

// Reasons a generator's path is refused. A run with any of them writes
// nothing.
const (
	// ReasonEmpty is an empty path.
	ReasonEmpty Reason = "empty"
	// ReasonAbsolute is a path that starts with a separator.
	ReasonAbsolute Reason = "absolute"
	// ReasonDotSegment is a path with a "." or ".." segment.
	ReasonDotSegment Reason = "dotSegment"
	// ReasonEmptySegment is a path with two separators in a row.
	ReasonEmptySegment Reason = "emptySegment"
	// ReasonNUL is a path holding a NUL byte.
	ReasonNUL Reason = "nul"
	// ReasonBackslash is a path holding a backslash, which is a separator on
	// one platform and a name character on another.
	ReasonBackslash Reason = "backslash"
	// ReasonTrailingSeparator is a path that ends in a separator, which names
	// a directory and not a file.
	ReasonTrailingSeparator Reason = "trailingSeparator"
	// ReasonManifest is the manifest's own path, or a path under it, in any
	// letter case.
	ReasonManifest Reason = "manifest"
	// ReasonTemporaryName is a name of the shape the guard gives a temporary
	// file during an atomic write, which a later run removes.
	ReasonTemporaryName Reason = "temporaryName"
	// ReasonDuplicate is a path emitted twice.
	ReasonDuplicate Reason = "duplicate"
	// ReasonCaseCollision is a path that differs from another only by letter
	// case, in the file name or in a directory on the way to it, which a
	// case-insensitive filesystem would make one file.
	ReasonCaseCollision Reason = "caseCollision"
	// ReasonFileDirectoryCollision is a path that is a file in one output and
	// a directory in another.
	ReasonFileDirectoryCollision Reason = "fileDirectoryCollision"
)

// Reasons a file was not written that are about the directory it sits in.
const (
	// ReasonDirectoryIsLink is a directory on the way to the file, under
	// generated/, that is a symbolic link. Nothing is written through it,
	// wherever it leads.
	ReasonDirectoryIsLink Reason = "directoryIsLink"
	// ReasonDirectoryNotDirectory is a directory on the way to the file that
	// is a regular file or anything else that is not a directory.
	ReasonDirectoryNotDirectory Reason = "directoryNotDirectory"
	// ReasonChangedDuringRun is a file that was something else when it was
	// written than when it was checked, a moment earlier.
	ReasonChangedDuringRun Reason = "changedDuringRun"
)

// checkPath is the shape a single path relative to generated/ must have.
// It returns "" for a path that may be written.
func checkPath(path string) Reason {
	switch {
	case path == "":
		return ReasonEmpty
	case strings.ContainsRune(path, 0):
		return ReasonNUL
	case strings.ContainsRune(path, '\\'):
		return ReasonBackslash
	case strings.HasPrefix(path, "/"):
		return ReasonAbsolute
	case strings.HasSuffix(path, "/"):
		return ReasonTrailingSeparator
	}
	segments := strings.Split(path, "/")
	for _, segment := range segments {
		switch segment {
		case "":
			return ReasonEmptySegment
		case ".", "..":
			return ReasonDotSegment
		}
	}
	if strings.EqualFold(segments[0], ManifestName) {
		return ReasonManifest
	}
	if _, temporary := temporaryBase(segments[len(segments)-1]); temporary {
		return ReasonTemporaryName
	}
	return ""
}

// fold is the key two paths share when a case-insensitive filesystem would
// make them the same name.
func fold(path string) string {
	return strings.ToLower(strings.ToUpper(path))
}

// parents returns the directories on the way to path, outermost first:
// "a/b/c" is "a", "a/b".
func parents(path string) []string {
	var out []string
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			out = append(out, path[:i])
		}
	}
	return out
}

// splitPath separates the directory a path is in from its name; the
// directory is "" for a name directly under generated/.
func splitPath(path string) (directory, name string) {
	i := strings.LastIndexByte(path, '/')
	if i < 0 {
		return "", path
	}
	return path[:i], path[i+1:]
}

// checkSet checks every path on its own and then against each other, and
// returns the refusal for each refused path. A path refused for colliding
// with another is reported together with the one it collides with.
func checkSet(paths []string) map[string]Reason {
	refused := map[string]Reason{}
	refuse := func(path string, reason Reason) {
		if _, already := refused[path]; !already {
			refused[path] = reason
		}
	}

	seen := map[string]int{}
	for _, path := range paths {
		seen[path]++
	}
	files := map[string]string{}                // folded file path -> spelling
	directories := map[string]map[string]bool{} // folded directory path -> spellings
	for _, path := range paths {
		if reason := checkPath(path); reason != "" {
			refuse(path, reason)
			continue
		}
		if seen[path] > 1 {
			refuse(path, ReasonDuplicate)
			continue
		}
		key := fold(path)
		if other, ok := files[key]; ok {
			refuse(path, ReasonCaseCollision)
			refuse(other, ReasonCaseCollision)
		} else {
			files[key] = path
		}
		for _, directory := range parents(path) {
			folded := fold(directory)
			if directories[folded] == nil {
				directories[folded] = map[string]bool{}
			}
			directories[folded][directory] = true
		}
	}
	// A directory spelled two ways refuses every path that uses either
	// spelling, so the report names both sides.
	for _, path := range paths {
		if checkPath(path) != "" {
			continue
		}
		for _, directory := range parents(path) {
			if len(directories[fold(directory)]) > 1 {
				refuse(path, ReasonCaseCollision)
			}
		}
	}
	for _, path := range paths {
		if checkPath(path) != "" {
			continue
		}
		key := fold(path)
		if _, isDirectory := directories[key]; isDirectory {
			refuse(path, ReasonFileDirectoryCollision)
			for _, other := range paths {
				if strings.HasPrefix(fold(other), key+"/") {
					refuse(other, ReasonFileDirectoryCollision)
				}
			}
		}
	}
	return refused
}

// temporaryBase reports whether name is the guard's temporary file name for
// an atomic write, ".<base>.tmp-<16 hex digits>", and which file it was for.
func temporaryBase(name string) (string, bool) {
	if !strings.HasPrefix(name, ".") {
		return "", false
	}
	i := strings.LastIndex(name, ".tmp-")
	if i < 2 {
		return "", false
	}
	suffix := name[i+len(".tmp-"):]
	if len(suffix) != 16 || !isLowerHex(suffix) {
		return "", false
	}
	return name[1:i], true
}
