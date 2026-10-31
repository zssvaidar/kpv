package catalog

import (
	"errors"
	"io/fs"
	"reflect"
	"strconv"
	"strings"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// dirMode is the mode of a pipeline directory Create makes.
const dirMode fs.FileMode = 0o755

// maxIDLength is the identifier pattern's limit: a letter and 62 more.
const maxIDLength = 63

// maxSuffix bounds how many suffixed ids Create tries before it gives up.
const maxSuffix = 10000

// CreateRequest is what a new pipeline is made from. The values are checked
// against the pipeline schema, by internal/pipeline, before anything is
// touched.
type CreateRequest struct {
	// Root is the stored root to create the pipeline in.
	Root             string
	Name             string
	Language         string
	SourceTopic      string
	DestinationTopic string
	NetworkMode      string
}

// model is the minimal pipeline a request describes: a source block named
// source, a destination block named destination, no processing steps and no
// value the schema has a default for.
func (r CreateRequest) model(id string) *pipeline.Pipeline {
	name := r.Name
	return &pipeline.Pipeline{
		ID:       pipeline.Identifier(id),
		Name:     &name,
		Language: pipeline.Language(r.Language),
		Source: pipeline.KafkaSourceBlock{
			ID: "source", Type: pipeline.KafkaSourceBlockTypeKafkaSource, Topic: pipeline.KafkaTopic(r.SourceTopic),
		},
		Destination: pipeline.KafkaDestinationBlock{
			ID: "destination", Type: pipeline.KafkaDestinationBlockTypeKafkaDestination, Topic: pipeline.KafkaTopic(r.DestinationTopic),
		},
		RuntimeConfiguration: pipeline.RuntimeConfiguration{
			NetworkMode: pipeline.RuntimeConfigurationNetworkMode(r.NetworkMode),
		},
	}
}

// Create makes a new pipeline in a root and answers it opened.
//
// The id is derived from the name (deriveID). The pipeline is validated and
// rendered before anything is touched. The root must be stored, available and
// not itself a pipeline (KindRootIsPipeline). Then a directory named after
// the id is claimed with a single Mkdir, which fails if anything of that name
// is there, so a directory that already exists is never written into,
// whatever it holds; an id that is taken that way, or that any listed
// pipeline in any root has (ignoring case), moves on to <id>-2, <id>-3 and so
// on. The new directory is pinned and pipeline.yaml written in it by its
// single name with Path.WriteAtomically (0644). A directory replaced between
// the Mkdir and the pin is refused KindChanged and nothing is written.
//
// If anything fails after the Mkdir, the directory is removed only when it is
// shown to be still the empty directory this create made, and the error's
// DirectoryLeftBehind says whether it is still there.
//
// Creates are serialized within the agent; across two agents the Mkdir claim
// alone keeps each out of the other's directory.
func (c *Catalog) Create(request CreateRequest) (*Document, error) {
	c.create.Lock()
	defer c.create.Unlock()
	document, err := c.createLocked(request)
	if err != nil {
		c.logger.Info("a pipeline was not created", "kind", string(KindOf(err)))
		return nil, err
	}
	c.logger.Info("a pipeline was created")
	return document, nil
}

func (c *Catalog) createLocked(request CreateRequest) (*Document, error) {
	if request.Root == "" {
		return nil, &Error{Kind: KindInvalid}
	}
	base := deriveID(request.Name)
	if _, err := render(request.model(base)); err != nil {
		return nil, err
	}
	if fault := c.rootStatus(request.Root); fault != nil {
		return nil, fault
	}
	rootPin, failed := c.pinRoot(request.Root)
	if failed != "" {
		return nil, c.rootFault(request.Root, failed)
	}
	defer func() { _ = rootPin.Close() }()
	root := rootPin.Path()
	if file, err := root.Join(FileName); err == nil {
		if _, err := file.Lstat(); err == nil || projectfs.KindOf(err) != projectfs.KindNotFound {
			if err != nil && projectfs.KindOf(err) == projectfs.KindForbidden {
				return nil, c.rootFault(request.Root, projectfs.KindForbidden)
			}
			if err == nil {
				return nil, &Error{Kind: KindRootIsPipeline}
			}
			return nil, &Error{Kind: KindIO}
		}
	}

	taken := map[string]bool{}
	for _, listed := range c.List().Roots {
		for _, entry := range listed.Pipelines {
			if entry.Readable() {
				taken[strings.ToLower(entry.ID)] = true
			}
		}
	}

	for suffix := 1; suffix <= maxSuffix; suffix++ {
		id := withSuffix(base, suffix)
		if taken[strings.ToLower(id)] {
			continue
		}
		claimed, err := root.Join(id)
		if err != nil {
			return nil, &Error{Kind: KindIO}
		}
		if err := claimed.Mkdir(dirMode); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			if projectfs.KindOf(err) == projectfs.KindForbidden {
				return nil, c.rootFault(request.Root, projectfs.KindForbidden)
			}
			return nil, &Error{Kind: KindIO}
		}
		return c.fill(request, root, id, claimed)
	}
	return nil, &Error{Kind: KindNoFreeID}
}

// fill pins the directory create just claimed and writes the pipeline into
// it, or undoes the claim when it can show it is still its own.
func (c *Catalog) fill(request CreateRequest, root projectfs.Path, id string, claimed projectfs.Path) (*Document, error) {
	if c.hook.afterMkdir != nil {
		c.hook.afterMkdir(id)
	}
	pin, err := claimed.Pin()
	if err != nil {
		leftBehind := true
		kind := KindIO
		if projectfs.KindOf(err) == projectfs.KindChanged {
			kind = KindChanged
		}
		return nil, &Error{Kind: kind, DirectoryLeftBehind: &leftBehind}
	}
	defer func() { _ = pin.Close() }()
	data, err := render(request.model(id))
	if err == nil {
		if c.hook.beforeWrite != nil {
			c.hook.beforeWrite(id)
		}
		err = writeIn(pin.Path(), data)
	}
	if err != nil {
		leftBehind := !removeIfOwn(root, id, pin)
		kind := KindOf(err)
		if kind == "" {
			kind = KindIO
		}
		return nil, &Error{Kind: kind, DirectoryLeftBehind: &leftBehind}
	}
	model, steps, err := pipeline.ParseYAML(data)
	if err != nil {
		// Unreachable: MarshalYAML validated these bytes.
		return nil, &Error{Kind: KindIO}
	}
	encoded, err := pipeline.YAMLToJSON(data)
	if err != nil {
		return nil, &Error{Kind: KindIO}
	}
	return &Document{
		Location: Location{Root: request.Root, Directory: id}, Dir: pin.Path().Real(),
		Pipeline: model, Steps: steps, JSON: encoded, SHA256: digest(data),
	}, nil
}

// removeIfOwn removes the directory named id in root only when it is still
// the directory pin holds and that directory is empty, and reports whether it
// is gone. A directory it cannot show to be its own is left as it is.
func removeIfOwn(root projectfs.Path, id string, pin *projectfs.PinnedDir) bool {
	entries, err := pin.Path().ReadDir()
	if err != nil || len(entries) != 0 {
		return false
	}
	held, err := pin.Path().Stat()
	if err != nil {
		return false
	}
	named, err := root.Join(id)
	if err != nil {
		return false
	}
	there, err := named.Lstat()
	if err != nil || !there.IsDir() || !sameFile(held, there) {
		return false
	}
	return named.Remove() == nil
}

// sameFile is os.SameFile for the two descriptions, which this package cannot
// import os to call: the device and inode the platform's stat structure
// holds. It answers false whenever either cannot be read, so a directory that
// cannot be shown to be the same is treated as another one.
func sameFile(a, b fs.FileInfo) bool {
	first, ok := fileKey(a)
	if !ok {
		return false
	}
	second, ok := fileKey(b)
	return ok && first == second
}

func fileKey(info fs.FileInfo) ([2]uint64, bool) {
	value := reflect.ValueOf(info.Sys())
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return [2]uint64{}, false
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return [2]uint64{}, false
	}
	device, okDevice := unsigned(value.FieldByName("Dev"))
	inode, okInode := unsigned(value.FieldByName("Ino"))
	return [2]uint64{device, inode}, okDevice && okInode
}

func unsigned(field reflect.Value) (uint64, bool) {
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return field.Uint(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return uint64(field.Int()), true
	}
	return 0, false
}

// deriveID is the id a pipeline named name gets: ASCII letters and digits are
// kept and lowercased, every other run of characters becomes one "-", and the
// dashes this leaves at either end are dropped. A result that does not start
// with a letter gets the prefix "pipeline-", an empty one is "pipeline", and
// the result is cut to the identifier pattern's 63 characters, without a dash
// left at the end of the cut.
func deriveID(name string) string {
	var out strings.Builder
	pending := false
	for index := 0; index < len(name); index++ {
		character := name[index]
		switch {
		case character >= 'A' && character <= 'Z':
			character += 'a' - 'A'
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
		default:
			pending = true
			continue
		}
		if pending && out.Len() > 0 {
			out.WriteByte('-')
		}
		pending = false
		out.WriteByte(character)
	}
	id := out.String()
	switch {
	case id == "":
		id = "pipeline"
	case id[0] < 'a' || id[0] > 'z':
		id = "pipeline-" + id
	}
	return cut(id, maxIDLength)
}

// withSuffix is base for the first try and base-<n> after it, cut so the
// whole stays within 63 characters.
func withSuffix(base string, n int) string {
	if n == 1 {
		return base
	}
	suffix := "-" + strconv.Itoa(n)
	return cut(base, maxIDLength-len(suffix)) + suffix
}

// cut shortens id to at most limit bytes (it is ASCII) with no trailing dash.
func cut(id string, limit int) string {
	if len(id) > limit {
		id = id[:limit]
	}
	return strings.TrimRight(id, "-")
}
