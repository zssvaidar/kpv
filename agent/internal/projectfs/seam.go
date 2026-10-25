package projectfs

// PipelineSeam adapts a Guard to the PathGuard interface internal/pipeline
// declares, which is the seam #3 left for this package: a Store is handed one
// and no caller of Load or Save changes shape.
//
// It satisfies that interface structurally, without importing the pipeline
// package. That is deliberate in both directions: this package is the lower
// one and has no business knowing about the pipeline model, and the HTTP
// boundary is not allowed to import internal/pipeline at all, so a guard it
// can hold must not drag that package in behind it.
//
// What this seam cannot carry, and the reason it is not the route to prefer:
// the interface hands back a path for the caller to open with os itself, so
// the authorization is checked once, on a path, and the open that follows is
// an ordinary open. A symbolic link swapped in between the two would be
// followed. The Guard's own operations do not have that gap, because they
// never hand out a path to open -- they perform the operation through the
// root's descriptor, and the kernel refuses anything that leaves it. Callers
// that can use those operations should, and the store that moves onto them is
// #48.
type PipelineSeam struct {
	guard *Guard
}

// PipelineSeam returns the adapter for #3's PathGuard seam.
func (g *Guard) PipelineSeam() PipelineSeam { return PipelineSeam{guard: g} }

// ResolvePipelinePath checks path against the authorized roots and returns the
// real path to use, or a *PathError. It is the method internal/pipeline's
// PathGuard interface asks for.
func (s PipelineSeam) ResolvePipelinePath(path string) (string, error) {
	if s.guard == nil {
		return "", &PathError{
			Op: "resolve", Path: path, Kind: KindForbidden,
			Message: "no guard is behind this seam, so no path is authorized",
		}
	}
	resolved, err := s.guard.resolve("resolve", path)
	if err != nil {
		return "", err
	}
	return resolved.Real(), nil
}
