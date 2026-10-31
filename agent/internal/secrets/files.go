package secrets

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// MaxFileSize is the largest .env or .gitignore this package reads, 1 MiB.
// A larger file is reported as KindTooLarge and not read, so a stray huge file
// cannot make a write hold it in memory.
const MaxFileSize = 1 << 20

// Modes of the files this package creates. An existing file keeps its own
// mode; projectfs.Path.WriteAtomically sets a new file's mode exactly,
// whatever the umask, and creates its temporary file with the mode the file
// will have.
const (
	envMode       fs.FileMode = 0o600
	gitignoreMode fs.FileMode = 0o644
)

// Files reads and writes the two files at a pipeline root, through the guard.
type Files struct {
	guard  *projectfs.Guard
	logger *slog.Logger
	hooks  hooks
}

// hooks are the points a test acts at; New sets none.
type hooks struct {
	// envEmit runs while the new .env is being written to its temporary
	// file, which exists then; an error from it fails the write there.
	envEmit func() error
}

// New returns Files over guard. Every problem a write or read finds is
// logged to logger as one warning line carrying its kind, file, lines and
// key; nil discards them.
func New(guard *projectfs.Guard, logger *slog.Logger) *Files {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Files{guard: guard, logger: logger}
}

// Env is what a .env holds.
type Env struct {
	// Present is false when the pipeline directory has no .env.
	Present bool
	// Entries are the entries in file order, inside the block and outside.
	Entries []Entry
	// Problems are the warnings about the file: malformed lines, keys set
	// twice, a mode readable by others, a block that is not well-formed.
	Problems []Problem
}

// ReadEnv reads the .env at the root of the pipeline directory dir.
func (f *Files) ReadEnv(dir string) (Env, error) {
	directory, problem, guardErr := f.resolve(dir)
	if problem != nil {
		f.log("read", []Problem{*problem})
		return Env{}, refusal("read", []Problem{*problem}, guardErr)
	}
	state, problems, guardErr := load(directory, EnvFile)
	if refused := refusal("read", problems, guardErr); refused != nil {
		f.log("read", problems)
		return Env{}, refused
	}
	if !state.exists {
		return Env{}, nil
	}
	analysis := analyzeEnv(state.lines, state.block)
	env := Env{Present: true, Entries: analysis.entries}
	env.Problems = append(env.Problems, warnings(state.problems)...)
	for _, problem := range analysis.problems {
		problem.Warning = true
		env.Problems = append(env.Problems, problem)
	}
	if exposed := exposedMode(state); exposed != nil {
		env.Problems = append(env.Problems, *exposed)
	}
	f.log("read", env.Problems)
	return env, nil
}

// WriteGitignore writes the block into the .gitignore at the root of the
// pipeline directory dir, creating the file if there is none. It is what
// WriteEnv does first.
func (f *Files) WriteGitignore(ctx context.Context, dir string) (Report, error) {
	report := Report{Gitignore: NotWritten}
	directory, problem, guardErr := f.resolve(dir)
	if problem != nil {
		return f.refuse("gitignore", report, []Problem{*problem}, guardErr)
	}
	release, err := lockDirectory(ctx, directory.Real())
	if err != nil {
		return report, err
	}
	defer release()

	gitignore, problems, guardErr := load(directory, GitignoreFile)
	problems = append(problems, gitignore.problems...)
	if refused := refusal("gitignore", problems, guardErr); refused != nil {
		return f.refuse("gitignore", report, problems, guardErr)
	}
	return f.writeGitignore(ctx, report, gitignore)
}

// WriteEnv sets values in the .env at the root of the pipeline directory dir,
// after writing the .gitignore block that ignores it.
//
// It is a merged write. The block of .env ends up holding the entries it held
// before with every key in values set to its value, sorted by key; nothing
// outside the block is changed, and an empty values still leaves a block. The
// order is fixed: every key, value and both files are checked first, and a
// problem with any of them writes nothing; then .gitignore is written, and
// only when that succeeded is .env written. Each file is replaced atomically.
//
// It is refused, with nothing written, for a key that is not a variable
// name, a value with a line break or NUL byte, a key a user line outside the
// block already sets, a file that is a link, not a regular file, too large or
// unreadable, a block whose lines were edited, markers that do not make one
// block, and a pipeline directory the guard refuses. The error is an *Error
// naming files, lines and keys, never a value.
//
// Two writes for one directory in one agent do not interleave: the second
// waits for the first, or for ctx.
func (f *Files) WriteEnv(ctx context.Context, dir string, values map[string]Secret) (Report, error) {
	report := Report{Gitignore: NotWritten, Env: NotWritten}

	var problems []Problem
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		switch {
		case !validKey(key):
			problems = append(problems, Problem{
				Kind: KindInvalidKey, Key: key,
				Message: "this key is not an environment variable name ([A-Za-z_][A-Za-z0-9_]*)",
			})
		case !validValue(values[key].Reveal()):
			problems = append(problems, Problem{
				Kind: KindInvalidValue, Key: key,
				Message: "this key's value holds a line break or a NUL byte, which a .env line cannot carry",
			})
		}
	}
	if len(problems) > 0 {
		return f.refuse("write", report, problems, nil)
	}

	directory, problem, guardErr := f.resolve(dir)
	if problem != nil {
		return f.refuse("write", report, []Problem{*problem}, guardErr)
	}
	release, err := lockDirectory(ctx, directory.Real())
	if err != nil {
		return report, err
	}
	defer release()

	gitignore, gitProblems, gitGuard := load(directory, GitignoreFile)
	env, envProblems, envGuard := load(directory, EnvFile)
	problems = append(problems, gitProblems...)
	problems = append(problems, gitignore.problems...)
	problems = append(problems, envProblems...)
	problems = append(problems, env.problems...)
	if guardErr = gitGuard; guardErr == nil {
		guardErr = envGuard
	}
	var analysis envAnalysis
	if env.readable() {
		analysis = analyzeEnv(env.lines, env.block)
		problems = append(problems, analysis.problems...)
		for _, key := range keys {
			if numbers, set := analysis.user[key]; set {
				problems = append(problems, Problem{
					Kind: KindSetOutsideBlock, File: EnvFile, Lines: numbers, Key: key,
					Message: "a user line outside the kpv marker block already sets this key; kpv does not override it, so nothing was written",
				})
			}
		}
		if exposed := exposedMode(env); exposed != nil {
			problems = append(problems, *exposed)
		}
	}
	if refused := refusal("write", problems, guardErr); refused != nil {
		return f.refuse("write", report, problems, guardErr)
	}

	report, err = f.writeGitignore(ctx, report, gitignore)
	if err != nil {
		return report, err
	}
	report.Problems = append(report.Problems, problems...)

	report.Problems = append(report.Problems, removeLeftovers(directory)...)

	body := envBody(analysis.block, values)
	next := compose(env.lines, env.block, body)
	switch {
	case env.exists && next == string(env.data):
		report.Env = Unchanged
	default:
		if err := ctx.Err(); err != nil {
			f.log("write", report.Problems)
			return report, err
		}
		emit := func(out io.Writer) error {
			if f.hooks.envEmit != nil {
				if err := f.hooks.envEmit(); err != nil {
					return err
				}
			}
			_, err := io.WriteString(out, next)
			return err
		}
		if err := env.path.WriteAtomically(envMode, emit); err != nil {
			report.Problems = append(report.Problems, guardProblem(EnvFile, err))
			return f.refuse("write", report, report.Problems, err)
		}
		report.Env = Updated
		if !env.exists {
			report.Env = Created
		}
	}
	f.log("write", report.Problems)
	return report, nil
}

// writeGitignore writes the block into a loaded, checked .gitignore.
func (f *Files) writeGitignore(ctx context.Context, report Report, gitignore fileState) (Report, error) {
	body, notIgnored, envIgnored := gitignoreBody(gitignore.lines, gitignore.block)
	report.EnvIgnored = envIgnored
	report.Problems = append(report.Problems, notIgnored...)
	next := compose(gitignore.lines, gitignore.block, body)
	if gitignore.exists && next == string(gitignore.data) {
		report.Gitignore = Unchanged
		return report, nil
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	err := gitignore.path.WriteAtomically(gitignoreMode, func(out io.Writer) error {
		_, err := io.WriteString(out, next)
		return err
	})
	if err != nil {
		report.Problems = append(report.Problems, guardProblem(GitignoreFile, err))
		return f.refuse(opOf(report), report, report.Problems, err)
	}
	report.Gitignore = Updated
	if !gitignore.exists {
		report.Gitignore = Created
	}
	if report.Env == "" {
		f.log("gitignore", report.Problems)
	}
	return report, nil
}

func opOf(report Report) string {
	if report.Env == "" {
		return "gitignore"
	}
	return "write"
}

// refuse logs problems and returns the report with them and the error.
func (f *Files) refuse(op string, report Report, problems []Problem, guardErr error) (Report, error) {
	report.Problems = problems
	f.log(op, problems)
	return report, refusal(op, problems, guardErr)
}

// resolve checks the pipeline directory with the guard.
func (f *Files) resolve(dir string) (projectfs.Path, *Problem, error) {
	directory, err := f.guard.Resolve(dir)
	if err != nil {
		problem := guardProblem("", err)
		return projectfs.Path{}, &problem, err
	}
	info, err := directory.Stat()
	if err != nil {
		problem := guardProblem("", err)
		return projectfs.Path{}, &problem, err
	}
	if !info.IsDir() {
		return projectfs.Path{}, &Problem{
			Kind: KindNotRegularFile, Guard: projectfs.KindNotDirectory,
			Message: "the pipeline directory is not a directory",
		}, nil
	}
	return directory, nil, nil
}

// log writes one warning line per problem. A line carries the kind, the file
// name, lines and key, never a path or a value.
func (f *Files) log(op string, problems []Problem) {
	for _, problem := range problems {
		attrs := []slog.Attr{
			slog.String("op", op),
			slog.String("kind", string(problem.Kind)),
			slog.Bool("warning", problem.Warning),
		}
		if problem.File != "" {
			attrs = append(attrs, slog.String("file", problem.File))
		}
		if len(problem.Lines) > 0 {
			attrs = append(attrs, slog.Any("lines", problem.Lines))
		}
		if problem.Key != "" {
			attrs = append(attrs, slog.String("key", problem.Key))
		}
		if problem.Guard != "" {
			attrs = append(attrs, slog.String("guard", string(problem.Guard)))
		}
		f.logger.LogAttrs(context.Background(), slog.LevelWarn, "secrets: "+problem.Message, attrs...)
	}
}

// fileState is one of the two files as it was found.
type fileState struct {
	name   string
	path   projectfs.Path
	exists bool
	mode   fs.FileMode
	data   []byte
	lines  []line
	block  block
	// problems are what the file's markers and block say; the problems
	// load returns are about reaching the file at all.
	problems []Problem
}

func (s fileState) readable() bool { return s.path != (projectfs.Path{}) }

// load reaches the named file in directory without following a link at its
// name, and reads it if it is a regular file no larger than MaxFileSize.
func load(directory projectfs.Path, name string) (fileState, []Problem, error) {
	state := fileState{name: name}
	path, err := directory.Join(name)
	if err != nil {
		return state, []Problem{guardProblem(name, err)}, err
	}
	info, err := path.Lstat()
	switch {
	case projectfs.IsNotFound(err):
		state.path = path
		return state, nil, nil
	case err != nil:
		return state, []Problem{guardProblem(name, err)}, err
	case info.Mode()&fs.ModeSymlink != 0:
		return state, []Problem{{Kind: KindLink, File: name,
			Message: "this is a symbolic link; kpv does not read, write through or replace it"}}, nil
	case info.IsDir():
		return state, []Problem{{Kind: KindNotRegularFile, File: name,
			Message: "this is a directory, not a file; nothing is written over it"}}, nil
	case !info.Mode().IsRegular():
		return state, []Problem{{Kind: KindNotRegularFile, File: name,
			Message: "this is not a regular file; nothing is written over it"}}, nil
	case info.Size() > MaxFileSize:
		return state, []Problem{tooLarge(name)}, nil
	}
	file, err := path.Open()
	if err != nil {
		return state, []Problem{guardProblem(name, err)}, err
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxFileSize+1))
	_ = file.Close()
	if err != nil {
		return state, []Problem{guardProblem(name, err)}, err
	}
	if len(data) > MaxFileSize {
		return state, []Problem{tooLarge(name)}, nil
	}
	state.path, state.exists, state.mode, state.data = path, true, info.Mode().Perm(), data
	state.lines = splitLines(string(data))
	found, problem := findBlock(name, state.lines)
	if problem != nil {
		state.problems = append(state.problems, *problem)
		// The markers cannot be trusted, so nothing is a block.
		return state, nil, nil
	}
	state.block = found
	if problem := checkBlock(name, state.lines, found); problem != nil {
		state.problems = append(state.problems, *problem)
	}
	return state, nil, nil
}

func tooLarge(name string) Problem {
	return Problem{Kind: KindTooLarge, File: name,
		Message: "this file is larger than 1 MiB, so it is not read and nothing is written over it"}
}

// exposedMode is the warning for an existing .env others can read.
func exposedMode(state fileState) *Problem {
	if !state.exists || state.mode&0o044 == 0 {
		return nil
	}
	return &Problem{
		Kind: KindExposedMode, Warning: true, File: state.name,
		Message: "this file is readable by group or others (mode " + state.mode.String() + "); kpv keeps its mode, consider chmod 600",
	}
}

// warnings marks every problem as a warning; Read refuses nothing a write
// would refuse for a block.
func warnings(problems []Problem) []Problem {
	out := make([]Problem, len(problems))
	for i, problem := range problems {
		problem.Warning = true
		out[i] = problem
	}
	return out
}

// removeLeftovers removes the temporary files an interrupted write of either
// file left in directory: regular files named ".<name>.tmp-*". A link or
// anything else by that name is left alone.
func removeLeftovers(directory projectfs.Path) []Problem {
	entries, err := directory.ReadDir()
	if err != nil {
		problem := guardProblem("", err)
		problem.Kind, problem.Warning = KindLeftover, true
		problem.Message = "the pipeline directory could not be listed to remove temporary files of interrupted writes"
		return []Problem{problem}
	}
	var problems []Problem
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "."+EnvFile+".tmp-") && !strings.HasPrefix(name, "."+GitignoreFile+".tmp-") {
			continue
		}
		path, err := directory.Join(name)
		if err != nil {
			continue
		}
		info, err := path.Lstat()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := path.Remove(); err != nil {
			problems = append(problems, Problem{
				Kind: KindLeftover, Warning: true, File: name, Guard: projectfs.KindOf(err),
				Message: "this temporary file of an interrupted write could not be removed",
			})
		}
	}
	return problems
}
