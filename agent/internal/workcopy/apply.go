package workcopy

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
)

// Apply writes what is staged for the pipeline at location into the
// project. It is called only when the user activates apply in the review,
// with the revision that review was drawn from; nothing applies on staging,
// on a save or on a timer.
//
// A working copy that is not there or has another revision is KindChanged
// and nothing is written. Then, holding catalog.Files (so no WriteSource
// interleaves) and before anything is written: the root is checked again,
// the pipeline directory and every directory on each path are pinned one
// name at a time, and every staged file's project copy is compared with the
// hash it was staged from, absent included. Any that differs is listed in
// a KindDrift error, all of them, and nothing is written. The directories
// are verified again, then a snapshot of every file the apply changes is
// written to the store (KindSnapshot when it cannot be, with nothing
// written), and only then each file is written with Path.WriteAtomically or
// removed, in path order. A failure stops there: the report says which
// paths were written, which failed with what kind, and which were not
// written. The written paths leave the working copy; the others stay staged,
// and the snapshot stays.
func (s *Store) Apply(location catalog.Location, revision string) (*Report, error) {
	if s.tree == nil {
		return nil, &Error{Kind: KindUnavailable}
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	stored := s.loadCopy(location)
	if stored == nil || stored.Revision != revision {
		return nil, &Error{Kind: KindChanged}
	}
	var report *Report
	err := s.catalog.WithFiles(location, func(files *catalog.Files) error {
		currents := make([]catalog.Current, len(stored.Files))
		var drift []Drift
		for index, file := range stored.Files {
			current, err := files.Read(file.Path)
			if err != nil {
				return err
			}
			currents[index] = current
			if state := stateOf(file.Base, current); state != Ready {
				drift = append(drift, Drift{Path: file.Path, Now: state})
			}
		}
		if len(drift) > 0 {
			return &Error{Kind: KindDrift, Drift: drift}
		}
		if s.hook.afterCheck != nil {
			s.hook.afterCheck()
		}
		if err := files.Verify(); err != nil {
			return err
		}
		snapshot := storedSnapshot{Version: storeVersion, Root: location.Root, Directory: location.Directory, Taken: s.now().UTC()}
		for index, file := range stored.Files {
			entry := snapshotFile{Path: file.Path, BeforeAbsent: !currents[index].Present, Before: currents[index].Data}
			if !file.Delete {
				entry.After = digest(file.Data)
			}
			snapshot.Files = append(snapshot.Files, entry)
		}
		if err := s.writeSnapshot(location, &snapshot); err != nil {
			return err
		}
		paths := make([]string, len(stored.Files))
		for index, file := range stored.Files {
			paths[index] = file.Path
		}
		report = s.write(location, snapshot.ID, paths, func(index int) error {
			file := stored.Files[index]
			if file.Delete {
				return files.Remove(file.Path)
			}
			return files.Write(file.Path, file.Data)
		})
		return nil
	})
	if err != nil {
		fault := fromCatalog(err)
		s.logger.Info("staged changes were not applied", "kind", string(fault.Kind), "drift", len(fault.Drift))
		return nil, fault
	}
	kept := []storedFile{}
	for index, outcome := range report.Files {
		if outcome.Result != Written {
			kept = append(kept, stored.Files[index])
		}
	}
	stored.Files = kept
	if err := s.saveCopy(location, stored); err != nil {
		s.logger.Warn("the working copy could not be updated after an apply", "kind", string(KindOf(err)))
	}
	s.logger.Info("staged changes were applied", "files", len(report.Files), "complete", report.Complete)
	return report, nil
}

// writeSnapshot gives a snapshot its id and writes it to the store, on disk
// before it answers.
func (s *Store) writeSnapshot(location catalog.Location, snapshot *storedSnapshot) error {
	suffix, err := randomHex(4)
	if err != nil {
		return &Error{Kind: KindSnapshot}
	}
	snapshot.ID = snapshot.Taken.Format("20060102T150405Z") + "-" + suffix
	body, err := json.Marshal(snapshot)
	if err != nil {
		return &Error{Kind: KindSnapshot}
	}
	file, err := s.tree.File(snapshotName(location, snapshot.ID))
	if err != nil {
		return &Error{Kind: KindSnapshot}
	}
	if err := file.Write(body); err != nil {
		return &Error{Kind: KindSnapshot}
	}
	return nil
}

// write runs each write in path order and reports them, stopping at the
// first failure: the paths after it are reported not written.
func (s *Store) write(location catalog.Location, snapshot string, paths []string, one func(index int) error) *Report {
	report := &Report{Location: location, Snapshot: snapshot, Complete: true, Files: []Outcome{}}
	for index, path := range paths {
		if !report.Complete {
			report.Files = append(report.Files, Outcome{Path: path, Result: NotWritten})
			continue
		}
		var err error
		if s.hook.beforeFile != nil {
			err = s.hook.beforeFile(index, path)
		}
		if err == nil {
			err = one(index)
		}
		if err != nil {
			report.Complete = false
			report.Files = append(report.Files, Outcome{Path: path, Result: Failed, Kind: fromCatalog(err).Kind})
			continue
		}
		report.Files = append(report.Files, Outcome{Path: path, Result: Written})
	}
	return report
}

// Restore puts every file of the snapshot id of location back: its bytes,
// or removed where the snapshot says there was no file. It goes through
// apply's checks: the root is checked again, the directories are pinned and
// verified, and each file must now be what the apply wrote, or still what
// the snapshot holds (the apply did not write it; it is left Unchanged).
// Any other file is listed in a KindDrift error and nothing is written. A
// snapshot that is not there is KindNotFound. The snapshot is kept.
func (s *Store) Restore(location catalog.Location, id string) (*Report, error) {
	if s.tree == nil {
		return nil, &Error{Kind: KindUnavailable}
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	snapshot := s.loadSnapshot(location, id)
	if snapshot == nil {
		return nil, &Error{Kind: KindNotFound}
	}
	var report *Report
	err := s.catalog.WithFiles(location, func(files *catalog.Files) error {
		restore := make([]bool, len(snapshot.Files))
		var drift []Drift
		for index, file := range snapshot.Files {
			current, err := files.Read(file.Path)
			if err != nil {
				return err
			}
			readable := current.Present && current.Kind == ""
			before := (file.BeforeAbsent && !current.Present) || (!file.BeforeAbsent && readable && bytes.Equal(current.Data, file.Before))
			after := (file.After == "" && !current.Present) || (file.After != "" && readable && digest(current.Data) == file.After)
			switch {
			case before:
			case after:
				restore[index] = true
			default:
				state := stateOf(file.After, current)
				if state == Ready {
					state = Changed
				}
				drift = append(drift, Drift{Path: file.Path, Now: state})
			}
		}
		if len(drift) > 0 {
			return &Error{Kind: KindDrift, Drift: drift}
		}
		if s.hook.afterCheck != nil {
			s.hook.afterCheck()
		}
		if err := files.Verify(); err != nil {
			return err
		}
		var paths []string
		var indexes []int
		unchanged := []Outcome{}
		for index, file := range snapshot.Files {
			if restore[index] {
				paths = append(paths, file.Path)
				indexes = append(indexes, index)
			} else {
				unchanged = append(unchanged, Outcome{Path: file.Path, Result: Unchanged})
			}
		}
		report = s.write(location, id, paths, func(at int) error {
			file := snapshot.Files[indexes[at]]
			if file.BeforeAbsent {
				return files.Remove(file.Path)
			}
			return files.Write(file.Path, file.Before)
		})
		report.Files = append(report.Files, unchanged...)
		sortOutcomes(report.Files)
		return nil
	})
	if err != nil {
		fault := fromCatalog(err)
		s.logger.Info("a snapshot was not restored", "kind", string(fault.Kind), "drift", len(fault.Drift))
		return nil, fault
	}
	s.logger.Info("a snapshot was restored", "files", len(report.Files), "complete", report.Complete)
	return report, nil
}

func sortOutcomes(outcomes []Outcome) {
	sort.Slice(outcomes, func(i, j int) bool { return outcomes[i].Path < outcomes[j].Path })
}
