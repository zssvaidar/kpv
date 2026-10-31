package connections

import (
	"github.com/zssvaidar/kpv/agent/internal/catalog"
)

// Lister is where the reference check finds pipelines: #48's list of every
// pipeline under the authorized roots (*catalog.Catalog), never a walk of its
// own.
type Lister interface {
	List() catalog.List
}

// The blocks a reference can be in.
const (
	BlockSource      = "source"
	BlockDestination = "destination"
)

// The reasons a place is unchecked beyond the list's own unreadable kinds,
// which are used as they are.
const (
	UncheckedRootUnavailable = "rootUnavailable"
	UncheckedTruncated       = "truncated"
)

// Reference is a pipeline block that names a connection.
type Reference struct {
	catalog.Location
	PipelineID string
	Block      string
}

// Unchecked is a pipeline, or a whole root (Directory "" and a root reason),
// that could not be read, so may name the connection. Reason is a
// catalog.UnreadableKind, or UncheckedRootUnavailable, or UncheckedTruncated.
type Unchecked struct {
	catalog.Location
	Reason string
}

// Report is what a delete or rename of a connection would leave dangling.
type Report struct {
	References []Reference
	Unchecked  []Unchecked
}

// Empty reports whether nothing names the connection and everything could be
// checked, so a delete or rename needs no confirmation.
func (r Report) Empty() bool { return len(r.References) == 0 && len(r.Unchecked) == 0 }

// References reports every pipeline block that names the connection called
// name, exactly as written, and every pipeline or root that could not be
// checked. It reads every pipeline.yaml through the list and writes nothing.
func (s *Store) References(name string) Report {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.references(name)
}

func (s *Store) references(name string) Report {
	report := Report{References: []Reference{}, Unchecked: []Unchecked{}}
	if s.pipelines == nil {
		return report
	}
	for _, root := range s.pipelines.List().Roots {
		if !root.Available {
			report.Unchecked = append(report.Unchecked, Unchecked{Location: catalog.Location{Root: root.Path}, Reason: UncheckedRootUnavailable})
			continue
		}
		if root.Truncated {
			report.Unchecked = append(report.Unchecked, Unchecked{Location: catalog.Location{Root: root.Path}, Reason: UncheckedTruncated})
		}
		for _, entry := range root.Pipelines {
			if !entry.Readable() || entry.Pipeline == nil {
				reason := string(catalog.UnreadableUnreadable)
				if entry.Unreadable != nil {
					reason = string(entry.Unreadable.Kind)
				}
				report.Unchecked = append(report.Unchecked, Unchecked{Location: entry.Location, Reason: reason})
				continue
			}
			id := string(entry.Pipeline.ID)
			if named := entry.Pipeline.Source.Connection; named != nil && string(*named) == name {
				report.References = append(report.References, Reference{Location: entry.Location, PipelineID: id, Block: BlockSource})
			}
			if named := entry.Pipeline.Destination.Connection; named != nil && string(*named) == name {
				report.References = append(report.References, Reference{Location: entry.Location, PipelineID: id, Block: BlockDestination})
			}
		}
	}
	return report
}
