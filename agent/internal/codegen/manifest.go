package codegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
)

// The manifest is generated/kpv-manifest.json:
//
//	{
//	  "version": 1,
//	  "files": [
//	    {"path": "main.go", "sha256": "<64 lowercase hex digits>"},
//	    {"path": "old.go", "sha256": "...", "orphaned": true}
//	  ]
//	}
//
// files is sorted by path, byte order, and the encoding is fixed, so the same
// set of files is always the same bytes. path is relative to generated/ with
// forward slashes, and sha256 is over the file's exact bytes. orphaned marks a
// file the last run found no generator emitting; it stays listed, with the
// hash it was generated with, until it is deleted. It is the agent's own
// format and not a shape exchanged with the page, so it has no schema in
// schema/.

// manifestEntry is one file the manifest lists.
type manifestEntry struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Orphaned bool   `json:"orphaned,omitempty"`
}

// manifestDocument is the manifest file as it is encoded.
type manifestDocument struct {
	Version int             `json:"version"`
	Files   []manifestEntry `json:"files"`
}

// manifest is the parsed manifest, keyed by path.
type manifest map[string]manifestEntry

// hashOf is the hex SHA-256 of data, the form the manifest records.
func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// encode is the manifest's bytes: always the same for the same entries.
func (m manifest) encode() []byte {
	document := manifestDocument{Version: ManifestVersion, Files: make([]manifestEntry, 0, len(m))}
	for _, entry := range m {
		document.Files = append(document.Files, entry)
	}
	sort.Slice(document.Files, func(i, j int) bool { return document.Files[i].Path < document.Files[j].Path })
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		// Strings, an int and a bool cannot fail to encode.
		panic(err)
	}
	return out.Bytes()
}

// errManifestVersion is a manifest that parses and is another version.
var errManifestVersion = errors.New("the manifest has a version this agent does not read")

// decodeManifest parses a manifest, refusing anything this package would not
// have written: unknown fields, trailing data, a path that is not a valid
// output path, a path listed twice or twice under different letter case, or
// a hash that is not 64 lowercase hex digits.
func decodeManifest(data []byte) (manifest, error) {
	var probe struct {
		Version *json.Number `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	if probe.Version == nil {
		return nil, errors.New("the manifest has no version")
	}
	if probe.Version.String() != "1" {
		return nil, errManifestVersion
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document manifestDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("the manifest has data after its document")
	}
	if document.Files == nil {
		return nil, errors.New("the manifest has no files list")
	}
	out := manifest{}
	folded := map[string]bool{}
	for _, entry := range document.Files {
		if checkPath(entry.Path) != "" {
			return nil, errors.New("the manifest lists a path that is not a valid generated path")
		}
		if folded[fold(entry.Path)] {
			return nil, errors.New("the manifest lists a path twice")
		}
		folded[fold(entry.Path)] = true
		if !isHash(entry.SHA256) {
			return nil, errors.New("the manifest lists a hash that is not a SHA-256")
		}
		out[entry.Path] = entry
	}
	return out, nil
}

func isHash(value string) bool {
	return len(value) == sha256.Size*2 && isLowerHex(value)
}

// isLowerHex reports whether value is only the digits 0-9 and a-f.
func isLowerHex(value string) bool {
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
