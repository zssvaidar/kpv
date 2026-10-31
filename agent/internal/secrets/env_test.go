package secrets

import (
	"context"
	"strings"
	"testing"
)

func TestAWriteCreatesBothFilesWithOnlyTheBlock(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	report, err := p.write(t, map[string]string{"B": "2", "A": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Gitignore != Created || report.Env != Created || !report.EnvIgnored || len(report.Problems) != 0 {
		t.Fatalf("report %+v", report)
	}
	body := []string{`A="1"`, `B="2"`}
	if want := StartMarker + "\n" + strings.Join(body, "\n") + "\n" + endMarker(body) + "\n"; p.env(t) != want {
		t.Fatalf(".env is\n%s\nwant\n%s", p.env(t), want)
	}
	gitBody := []string{".env", "..env.tmp-*"}
	if want := StartMarker + "\n.env\n..env.tmp-*\n" + endMarker(gitBody) + "\n"; p.gitignore(t) != want {
		t.Fatalf(".gitignore is\n%s\nwant\n%s", p.gitignore(t), want)
	}
}

func TestAnEmptySetCreatesAnEmptyBlockAndKeepsIt(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	if _, err := p.write(t, nil); err != nil {
		t.Fatal(err)
	}
	empty := StartMarker + "\n" + endMarker(nil) + "\n"
	if p.env(t) != empty {
		t.Fatalf(".env is %q", p.env(t))
	}
	write(t, p.path(EnvFile), "USER=1\n"+empty+"OTHER=2\n")
	report, err := p.write(t, map[string]string{})
	if err != nil || report.Env != Unchanged {
		t.Fatalf("%+v %v", report, err)
	}
	if p.env(t) != "USER=1\n"+empty+"OTHER=2\n" {
		t.Fatalf(".env is %q", p.env(t))
	}
}

func TestAMergedWriteSetsTheGivenKeysAndKeepsEverythingElse(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	user := "# my settings\n\nUSER_ONE=keep me   \nexport USER_TWO='x'\n"
	write(t, p.path(EnvFile), user)
	if _, err := p.write(t, map[string]string{"A": "1", "B": "2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.write(t, map[string]string{"B": "changed", "C": "3"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.env(t), user) {
		t.Fatalf("the user's lines changed:\n%s", p.env(t))
	}
	env, err := p.files.ReadEnv(p.pipe)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, entry := range env.Entries {
		got[entry.Key] = entry.Value.Reveal()
	}
	want := map[string]string{"USER_ONE": "keep me", "USER_TWO": "x", "A": "1", "B": "changed", "C": "3"}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q", key, got[key], value)
		}
	}
	if len(got) != len(want) {
		t.Errorf("entries %v", got)
	}
}

func TestReadReturnsKeysValuesAndLineNumbers(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	write(t, p.path(EnvFile), "\xef\xbb\xbfFIRST=1\n# c\n  export SECOND = \"two \\\"q\\\" \\$X\\n\" # trailing\nTHIRD='lit $X \\' ok' \nFOURTH=plain text # comment\nFIFTH=\n")
	env, err := p.files.ReadEnv(p.pipe)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		key, value string
		line       int
	}{{"FIRST", "1", 1}, {"SECOND", "two \"q\" $X\n", 3}, {"THIRD", "lit $X ' ok", 4}, {"FOURTH", "plain text", 5}, {"FIFTH", "", 6}}
	if len(env.Entries) != len(want) || !env.Present {
		t.Fatalf("entries %+v", env.Entries)
	}
	for i, w := range want {
		e := env.Entries[i]
		if e.Key != w.key || e.Value.Reveal() != w.value || e.Line != w.line {
			t.Errorf("entry %d is %s %q line %d, want %s %q line %d", i, e.Key, e.Value.Reveal(), e.Line, w.key, w.value, w.line)
		}
	}
}

func TestReadOfNoEnvIsEmpty(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	env, err := p.files.ReadEnv(p.pipe)
	if err != nil || env.Present || len(env.Entries) != 0 {
		t.Fatalf("%+v %v", env, err)
	}
}

func TestAMalformedLineIsReportedByFileAndLineWithoutItsContent(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	lines := []string{"OK=1", "no equals " + canary, "1BAD=" + canary, `Q="unterminated ` + canary, `J="x"junk` + canary, "KEY.DOT=" + canary}
	write(t, p.path(EnvFile), strings.Join(lines, "\n")+"\n")
	env, err := p.files.ReadEnv(p.pipe)
	if err != nil {
		t.Fatal(err)
	}
	var malformed []int
	for _, problem := range env.Problems {
		if problem.Kind == KindMalformedLine {
			malformed = append(malformed, problem.Lines...)
			if problem.File != EnvFile || strings.Contains(problem.String(), "Pa55w0rd") {
				t.Errorf("problem %v", problem)
			}
		}
	}
	if len(malformed) != 5 || malformed[0] != 2 || malformed[4] != 6 {
		t.Fatalf("malformed lines %v, problems %v", malformed, env.Problems)
	}
	if len(env.Entries) != 1 {
		t.Fatalf("entries %+v", env.Entries)
	}
	// A write keeps every malformed line as it was, and reports them too.
	report, err := p.write(t, map[string]string{"NEW": "v"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.env(t), strings.Join(lines, "\n")+"\n") || hasKind(report.Problems, KindMalformedLine) == nil {
		t.Fatalf("%+v\n%s", report, p.env(t))
	}
	if strings.Contains(p.logs.String(), "Pa55w0rd") || strings.Contains(p.logs.String(), "no equals") {
		t.Fatal("a log line holds a line's content")
	}
}

func TestValuesRoundTripInTheComposeQuotingRule(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	values := map[string]string{
		"DOLLAR": "$HOME ${X} $$ \\$", "HASH": "a #b#", "EQUALS": "a=b==", "SINGLE": "it's 'q'",
		"DOUBLE": `say "hi"`, "SPACES": "  lead  trail  ", "BACKSLASH": `C:\path\ ends\`, "UNICODE": "日本語 ü €",
		"TAB": "a\tb", "ESCAPES": `\n \t \\ \"`, "EMPTY": "", "MIXED": canary,
	}
	if _, err := p.write(t, values); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.env(t), `BACKSLASH="C:\\path\\ ends\\"`) || !strings.Contains(p.env(t), `DOLLAR="\$HOME \${X} \$\$ \\\$"`) {
		t.Fatalf("not in the documented quoting:\n%s", p.env(t))
	}
	env, err := p.files.ReadEnv(p.pipe)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Entries) != len(values) || len(env.Problems) != 0 {
		t.Fatalf("%+v", env)
	}
	for _, entry := range env.Entries {
		if entry.Value.Reveal() != values[entry.Key] {
			t.Errorf("%s read back as %q, want %q", entry.Key, entry.Value.Reveal(), values[entry.Key])
		}
	}
}

func TestAnInvalidKeyOrValueIsRefusedByNameBeforeAnythingIsWritten(t *testing.T) {
	t.Parallel()
	for name, values := range map[string]map[string]string{
		"digit first": {"1KEY": canary},
		"dot":         {"A.B": canary},
		"empty":       {"": canary},
		"dash":        {"A-B": canary},
		"newline":     {"GOOD": "a\n" + canary},
		"return":      {"GOOD": canary + "\r"},
		"nul":         {"GOOD": canary + "\x00"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			before := snapshot(t, p.pipe)
			_, err := p.write(t, values)
			want := KindInvalidKey
			if strings.HasPrefix(name, "n") || name == "return" {
				want = KindInvalidValue
			}
			refusal := refusedWith(t, err, want)
			for key := range values {
				if refusal[0].Key != key {
					t.Errorf("problem key %q, want %q", refusal[0].Key, key)
				}
			}
			sameSnapshot(t, before, snapshot(t, p.pipe))
		})
	}
}

func TestAKeyAUserLineAlreadySetsIsRefusedAndTheLineKept(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	user := "KAFKA_PASSWORD=mine\n"
	write(t, p.path(EnvFile), user)
	before := snapshot(t, p.pipe)
	_, err := p.write(t, map[string]string{"KAFKA_PASSWORD": canary, "OTHER": "x"})
	refusal := refusedWith(t, err, KindSetOutsideBlock)
	if refusal[0].Key != "KAFKA_PASSWORD" || refusal[0].Lines[0] != 1 {
		t.Fatalf("%+v", refusal[0])
	}
	sameSnapshot(t, before, snapshot(t, p.pipe))
}

func TestAKeyOnTwoUserLinesIsReportedWithBothLines(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	write(t, p.path(EnvFile), "A=1\nB=2\nA=3\n")
	report, err := p.write(t, map[string]string{"C": "x"})
	if err != nil {
		t.Fatal(err)
	}
	problem := hasKind(report.Problems, KindDuplicateKey)
	if problem == nil || problem.Key != "A" || len(problem.Lines) != 2 || problem.Lines[0] != 1 || problem.Lines[1] != 3 {
		t.Fatalf("%+v", report.Problems)
	}
	env, _ := p.files.ReadEnv(p.pipe)
	if hasKind(env.Problems, KindDuplicateKey) == nil {
		t.Fatalf("read does not report it: %+v", env.Problems)
	}
}

func TestWritingTwiceIsByteIdentical(t *testing.T) {
	t.Parallel()
	for name, existing := range map[string][2]string{
		"none":    {"", ""},
		"lf":      {"A=1\n", "node_modules/\n"},
		"crlf":    {"A=1\r\n# c\r\n", "dist\r\n"},
		"no eol":  {"A=1", "dist"},
		"bom":     {"\xef\xbb\xbfA=1\n", "\xef\xbb\xbfdist\n"},
		"spaces":  {"A=1   \n\n\n", "dist   \n\n"},
		"comment": {"# only\n", "# only\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			if existing[0] != "" {
				write(t, p.path(EnvFile), existing[0])
				write(t, p.path(GitignoreFile), existing[1])
			}
			values := map[string]string{"KEY": "v"}
			if _, err := p.write(t, values); err != nil {
				t.Fatal(err)
			}
			env, gitignore := p.env(t), p.gitignore(t)
			report, err := p.write(t, values)
			if err != nil {
				t.Fatal(err)
			}
			if report.Env != Unchanged || report.Gitignore != Unchanged || p.env(t) != env || p.gitignore(t) != gitignore {
				t.Fatalf("second write changed something: %+v", report)
			}
			if strings.Count(gitignore, "..env.tmp-*") != 1 || strings.Count(gitignore, StartMarker) != 1 {
				t.Fatalf("entries duplicated:\n%s", gitignore)
			}
			// The user's bytes are a prefix of each file, unchanged.
			if !strings.HasPrefix(env, existing[0]) || !strings.HasPrefix(gitignore, existing[1]) {
				t.Fatalf("user lines changed:\n%q\n%q", env, gitignore)
			}
		})
	}
}

func TestTheBlockUsesTheFilesLineEndingAndKeepsAMissingFinalNewline(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	write(t, p.path(EnvFile), "A=1\r\nB=2")
	write(t, p.path(GitignoreFile), "dist\r\n")
	if _, err := p.write(t, map[string]string{"K": "v"}); err != nil {
		t.Fatal(err)
	}
	body := []string{`K="v"`}
	if want := "A=1\r\nB=2\r\n" + StartMarker + "\r\n" + body[0] + "\r\n" + endMarker(body); p.env(t) != want {
		t.Fatalf(".env %q\nwant %q", p.env(t), want)
	}
	if !strings.HasPrefix(p.gitignore(t), "dist\r\n"+StartMarker+"\r\n.env\r\n") || !strings.HasSuffix(p.gitignore(t), "<<<\r\n") {
		t.Fatalf(".gitignore %q", p.gitignore(t))
	}
	if strings.Contains(strings.ReplaceAll(p.env(t), "\r\n", ""), "\n") {
		t.Fatalf("an LF line in a CRLF file: %q", p.env(t))
	}
}

func TestAnExistingBlockIsRewrittenWhereItIs(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	if _, err := p.write(t, map[string]string{"A": "1"}); err != nil {
		t.Fatal(err)
	}
	block := p.env(t)
	write(t, p.path(EnvFile), "BEFORE=1\n"+block+"AFTER=2 \n\n# end")
	if _, err := p.write(t, map[string]string{"B": "2"}); err != nil {
		t.Fatal(err)
	}
	body := []string{`A="1"`, `B="2"`}
	if want := "BEFORE=1\n" + StartMarker + "\n" + strings.Join(body, "\n") + "\n" + endMarker(body) + "\nAFTER=2 \n\n# end"; p.env(t) != want {
		t.Fatalf(".env %q\nwant %q", p.env(t), want)
	}
}

func TestALineEditedOrMovedIntoTheBlockIsReportedAndKept(t *testing.T) {
	t.Parallel()
	for name, edit := range map[string]func(string) string{
		"edited value": func(s string) string { return strings.Replace(s, `A="1"`, `A="2"`, 1) },
		"moved in": func(s string) string {
			return strings.Replace(s, StartMarker+"\n", StartMarker+"\nMINE="+canary+"\n", 1)
		},
		"comment in": func(s string) string { return strings.Replace(s, StartMarker+"\n", StartMarker+"\n# note\n", 1) },
		"removed":    func(s string) string { return strings.Replace(s, "A=\"1\"\n", "", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			if _, err := p.write(t, map[string]string{"A": "1"}); err != nil {
				t.Fatal(err)
			}
			write(t, p.path(EnvFile), edit(p.env(t)))
			before := snapshot(t, p.pipe)
			_, err := p.write(t, map[string]string{"B": canary})
			refusedWith(t, err, KindBlockEdited)
			sameSnapshot(t, before, snapshot(t, p.pipe))
		})
	}
}

func TestMarkersThatDoNotMakeOneBlockAreReportedAndLeftAlone(t *testing.T) {
	t.Parallel()
	end := endMarker(nil)
	for name, text := range map[string]string{
		"start only":    "A=1\n" + StartMarker + "\nB=2\n",
		"end only":      "A=1\n" + end + "\n",
		"end first":     end + "\n" + StartMarker + "\n",
		"two blocks":    StartMarker + "\n" + end + "\n" + StartMarker + "\n" + end + "\n",
		"two starts":    StartMarker + "\n" + StartMarker + "\n" + end + "\n",
		"broken digest": StartMarker + "\n" + "# <<< kpv managed block <<<\n",
	} {
		for _, file := range []string{EnvFile, GitignoreFile} {
			t.Run(name+" in "+file, func(t *testing.T) {
				t.Parallel()
				p := newProject(t)
				write(t, p.path(file), text)
				before := snapshot(t, p.pipe)
				_, err := p.write(t, map[string]string{"K": canary})
				want := KindMarkers
				if name == "broken digest" {
					want = KindBlockEdited
				}
				refusal := refusedWith(t, err, want)
				if refusal[0].File != file || len(refusal[0].Lines) == 0 {
					t.Fatalf("%+v", refusal[0])
				}
				sameSnapshot(t, before, snapshot(t, p.pipe))
				if file == GitignoreFile {
					_, err := p.files.WriteGitignore(context.Background(), p.pipe)
					refusedWith(t, err, want)
					sameSnapshot(t, before, snapshot(t, p.pipe))
				}
			})
		}
	}
}

func TestNothingIsWrittenOutsideTheTwoFiles(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	before := snapshot(t, p.root, "pipe/.env", "pipe/.gitignore")
	if _, err := p.write(t, map[string]string{"A": canary}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.write(t, map[string]string{"B": canary}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.files.WriteGitignore(context.Background(), p.pipe); err != nil {
		t.Fatal(err)
	}
	sameSnapshot(t, before, snapshot(t, p.root, "pipe/.env", "pipe/.gitignore"))
}
