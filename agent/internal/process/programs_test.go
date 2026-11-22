package process

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"time"
)

// shellNames reads the shell and -c literals TestNothingInTheAgentCanRunACommand
// bans, from the boundary test itself, so the two lists cannot drift apart.
func shellNames(t *testing.T) map[string]bool {
	t.Helper()
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "../projectfs/boundary_test.go", nil, 0)
	if err != nil {
		t.Fatalf("parse the boundary test: %v", err)
	}
	names := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "bannedLiterals" || len(spec.Values) != 1 {
			return true
		}
		literal, ok := spec.Values[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, element := range literal.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := pair.Key.(*ast.BasicLit)
			if !ok {
				continue
			}
			text, err := strconv.Unquote(key.Value)
			if err == nil {
				names[text] = true
			}
		}
		return false
	})
	if !names["sh"] || !names["bash"] || !names["-c"] || len(names) < 10 {
		t.Fatalf("read only %v from the boundary test's bannedLiterals; this test is not seeing the list", names)
	}
	return names
}

// programProblems is every way an entry of the table is malformed.
func programProblems(program Program, shells map[string]bool) []string {
	var problems []string
	switch name := program.executable; {
	case name == "":
		problems = append(problems, "has no executable")
	case strings.ContainsAny(name, `/\`):
		problems = append(problems, "names its executable by a path, not a bare name looked up on PATH")
	case shells[name] || shells[strings.ToLower(name)]:
		problems = append(problems, "runs a shell")
	}
	for _, argument := range program.args {
		if shells[argument] {
			problems = append(problems, "passes a shell's name or its -c flag as an argument")
		}
	}
	if program.limits.Timeout <= 0 {
		problems = append(problems, "has no timeout")
	}
	if program.limits.MaxStdout <= 0 {
		problems = append(problems, "has no stdout cap")
	}
	return problems
}

func TestEveryProgramInTheTableIsWellFormed(t *testing.T) {
	t.Parallel()
	shells := shellNames(t)
	for name, program := range programs {
		for _, problem := range programProblems(program, shells) {
			t.Errorf("program %s %s", name, problem)
		}
	}
	// #37 adds no entry; #38, #39 and #51 do.
	if len(programs) != 0 {
		t.Logf("the table has %d entries", len(programs))
	}
}

// The check above has to refuse what it says it refuses, or an empty table
// would pass it while it checks nothing.
func TestTheTableCheckRefusesEachMalformedEntry(t *testing.T) {
	t.Parallel()
	shells := shellNames(t)
	good := Limits{Timeout: time.Second, MaxStdout: 1}
	if problems := programProblems(newProgram("python3", []string{"-I", "parse.py"}, nil, good), shells); len(problems) != 0 {
		t.Fatalf("a well-formed entry is refused: %v", problems)
	}
	for _, bad := range []Program{
		newProgram("", nil, nil, good),
		newProgram("/usr/bin/python3", nil, nil, good),
		newProgram("bin/python3", nil, nil, good),
		newProgram(`C:\python3.exe`, nil, nil, good),
		newProgram("sh", nil, nil, good),
		newProgram("bash", nil, nil, good),
		newProgram("pwsh", nil, nil, good),
		newProgram("cmd.exe", nil, nil, good),
		newProgram("python3", []string{"-c", "print(1)"}, nil, good),
		newProgram("python3", nil, nil, Limits{Timeout: 0, MaxStdout: 1}),
		newProgram("python3", nil, nil, Limits{Timeout: -time.Second, MaxStdout: 1}),
		newProgram("python3", nil, nil, Limits{Timeout: time.Second, MaxStdout: 0}),
		newProgram("python3", nil, nil, Limits{Timeout: time.Second, MaxStdout: -1}),
	} {
		if len(programProblems(bad, shells)) == 0 {
			t.Errorf("%+v passes the table check", bad)
		}
	}
}

func TestLookupAnswersOnlyTableEntries(t *testing.T) {
	t.Parallel()
	if _, found := Lookup("pythonParser"); found {
		t.Fatal("Lookup found a program the table does not have")
	}
}
