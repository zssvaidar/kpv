package process

// ForTest builds an arbitrary Program, for tests only: a fake child that is
// the test binary run again, or an executable a test installs on a PATH of
// its own. It is the one way to build a Program outside the table in
// programs.go, and agent/internal/projectfs/boundary_test.go fails when any
// non-test file but this one names it, so shipped code cannot reach it.
func ForTest(executable string, args []string, env []string, limits Limits) Program {
	return newProgram(executable, args, env, limits)
}
