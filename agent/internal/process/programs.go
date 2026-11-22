package process

// Name names one entry of the program table.
type Name string

// programs is every program the agent may start. Each entry fixes a bare
// executable name looked up on PATH, its whole argument list, the environment
// variable names passed through from the agent, a timeout and a stdout cap.
// Nothing in a request can add to or change an entry.
//
// It is empty: #37 has no real parser. #38, #39 and #51 each add their own
// entry here, as a reviewed edit to this one file, with a Name constant above
// it. TestEveryProgramInTheTableIsWellFormed (programs_test.go) refuses an
// entry whose executable is empty, holds a / or a \, or names a shell, and one
// whose timeout or stdout cap is not positive.
var programs = map[Name]Program{}

// Lookup answers the table's entry for name.
func Lookup(name Name) (Program, bool) {
	program, found := programs[name]
	return program, found
}

// newProgram is the one place a Program is built, for the table above and for
// ForTest. It keeps copies, so a caller's later change to a slice it passed
// changes nothing.
func newProgram(executable string, args []string, env []string, limits Limits) Program {
	return Program{
		executable: executable,
		args:       append([]string(nil), args...),
		env:        append([]string(nil), env...),
		limits:     limits,
	}
}
