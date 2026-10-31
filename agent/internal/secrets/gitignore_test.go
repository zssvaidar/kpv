package secrets

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The module's answer for whether .env is ignored agrees with git's, run at
// the pipeline root after each fixture, the block written.
func TestWhetherEnvIsIgnoredAgreesWithGitCheckIgnore(t *testing.T) {
	t.Parallel()
	fixtures := map[string]string{
		"none":               "",
		"plain":              ".env\n",
		"anchored":           "/.env\n",
		"star suffix":        "*.env\n",
		"star prefix":        ".env*\n",
		"any directory":      "**/.env\n",
		"everything":         "*\n",
		"class":              ".e[n]v\n",
		"negated class":      ".e[!x]v\n",
		"question":           "?env\n",
		"escaped dot":        "\\.env\n",
		"directory only":     ".env/\n",
		"comment":            "# .env\n",
		"escaped hash":       "\\#.env\n",
		"trailing spaces":    ".env   \n",
		"escaped space":      ".env\\ \n",
		"trailing tab":       ".env\t\n",
		"upper case":         ".ENV\n",
		"crlf":               ".env\r\n",
		"bom":                "\xef\xbb\xbf.env\n",
		"negated":            "!.env\n",
		"ignore then negate": ".env\n!.env\n",
		"negate then ignore": "!.env\n.env\n",
		"wide then negate":   "*\n!.env\n",
		"escaped bang":       "\\!.env\n",
		"middle slash":       "a/.env\n",
		"reverse range":      ".e[z-a]v\n",
		"posix class":        ".e[[:alpha:]]v\n",
		"tmp negated":        "!..env.tmp-*\n",
	}
	for name, text := range fixtures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			gitInit(t, p.ceiling(), p.pipe)
			if text != "" {
				write(t, p.path(GitignoreFile), text)
			}
			report, err := p.write(t, map[string]string{"K": "v"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(p.gitignore(t), text) {
				t.Fatalf("the user's lines changed: %q", p.gitignore(t))
			}
			git := gitIgnores(t, p.ceiling(), p.pipe, EnvFile)
			if report.EnvIgnored != git {
				t.Fatalf("module says ignored=%v, git says %v, for\n%s", report.EnvIgnored, git, p.gitignore(t))
			}
			if !report.EnvIgnored && hasKind(report.Problems, KindNotIgnored) == nil {
				t.Fatalf("not ignored and no warning: %+v", report.Problems)
			}
			if name != "tmp negated" {
				if !gitIgnores(t, p.ceiling(), p.pipe, "..env.tmp-0011223344556677") {
					t.Fatalf("the temporary file name is not ignored:\n%s", p.gitignore(t))
				}
			}
		})
	}
}

func TestAUserLineThatIgnoresEnvGetsNoExtraLine(t *testing.T) {
	t.Parallel()
	for _, user := range []string{".env", "/.env", "*.env", ".env*"} {
		t.Run(user, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			write(t, p.path(GitignoreFile), "dist/\n"+user+"\n")
			report, err := p.files.WriteGitignore(context.Background(), p.pipe)
			if err != nil || !report.EnvIgnored || len(report.Problems) != 0 {
				t.Fatalf("%+v %v", report, err)
			}
			body := []string{"..env.tmp-*"}
			if want := "dist/\n" + user + "\n" + StartMarker + "\n" + strings.Join(append(body, ""), "\n") + endMarker(body) + "\n"; p.gitignore(t) != want {
				t.Fatalf("%q\nwant %q", p.gitignore(t), want)
			}
		})
	}
}

func TestANegationIsKeptAndWarnedAboutAndTheBlockDoesNotOverrideIt(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	gitInit(t, p.ceiling(), p.pipe)
	write(t, p.path(GitignoreFile), "dist\n!.env\n")
	report, err := p.write(t, map[string]string{"K": "v"})
	if err != nil {
		t.Fatal(err)
	}
	problem := hasKind(report.Problems, KindNotIgnored)
	if problem == nil || problem.File != GitignoreFile || problem.Lines[0] != 2 || !strings.Contains(problem.Message, ".env is not ignored") {
		t.Fatalf("%+v", report.Problems)
	}
	if strings.Contains(p.gitignore(t), "\n.env\n") || gitIgnores(t, p.ceiling(), p.pipe, EnvFile) {
		t.Fatalf("the block overrides the user's negation:\n%s", p.gitignore(t))
	}
	if !strings.Contains(p.logs.String(), "notIgnored") {
		t.Fatal("the warning was not logged")
	}
}

// A negation added after an existing block, which does hold ".env", turns
// the block's ".env" line off on the next write, which git agrees with.
func TestANegationAfterTheBlockIsHonoured(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	gitInit(t, p.ceiling(), p.pipe)
	write(t, p.path(GitignoreFile), "dist\n")
	if _, err := p.files.WriteGitignore(context.Background(), p.pipe); err != nil {
		t.Fatal(err)
	}
	if !gitIgnores(t, p.ceiling(), p.pipe, EnvFile) {
		t.Fatal("not ignored after the first write")
	}
	write(t, p.path(GitignoreFile), p.gitignore(t)+"!.env\n")
	report, err := p.files.WriteGitignore(context.Background(), p.pipe)
	if err != nil {
		t.Fatal(err)
	}
	if report.EnvIgnored || gitIgnores(t, p.ceiling(), p.pipe, EnvFile) {
		t.Fatalf("module %v, git %v:\n%s", report.EnvIgnored, gitIgnores(t, p.ceiling(), p.pipe, EnvFile), p.gitignore(t))
	}
	if !strings.HasSuffix(p.gitignore(t), "<<<\n!.env\n") || hasKind(report.Problems, KindNotIgnored).Lines[0] != 6 {
		t.Fatalf("%+v\n%s", report.Problems, p.gitignore(t))
	}
}

// The same files, with no warning about git, whether the pipeline directory
// is in no repository, is one's root, or is a subdirectory of one.
func TestThePipelineDirectoryNeedsNoRepository(t *testing.T) {
	t.Parallel()
	var envs, gitignores []string
	for _, layout := range []string{"none", "root", "subdirectory"} {
		p := newProject(t)
		switch layout {
		case "root":
			gitInit(t, p.ceiling(), p.pipe)
		case "subdirectory":
			gitInit(t, p.ceiling(), p.root)
		}
		report, err := p.write(t, map[string]string{"K": "v"})
		if err != nil || len(report.Problems) != 0 || !report.EnvIgnored {
			t.Fatalf("%s: %+v %v", layout, report, err)
		}
		if strings.Contains(strings.ToLower(p.logs.String()), "git") && !strings.Contains(p.logs.String(), ".gitignore") {
			t.Fatalf("%s: a log line about git: %s", layout, p.logs.String())
		}
		if p.logs.String() != "" {
			t.Fatalf("%s: logged %s", layout, p.logs.String())
		}
		if layout != "none" {
			if !gitIgnores(t, p.ceiling(), p.pipe, EnvFile) {
				t.Fatalf("%s: git does not ignore .env", layout)
			}
			out, _ := git(t, p.ceiling(), p.pipe, "status", "--porcelain", "--untracked-files=all", ".")
			if strings.Contains(out, ".env") {
				t.Fatalf("%s: git status shows .env:\n%s", layout, out)
			}
		} else if _, err := os.Stat(filepath.Join(p.pipe, ".git")); err == nil {
			t.Fatal("a repository appeared")
		}
		envs = append(envs, p.env(t))
		gitignores = append(gitignores, p.gitignore(t))
	}
	for i := 1; i < len(envs); i++ {
		if envs[i] != envs[0] || gitignores[i] != gitignores[0] {
			t.Fatalf("the layouts got different files")
		}
	}
}

func TestWildmatch(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		pattern, name string
		want          bool
	}{
		{"*", ".env", true}, {".e*", ".env", true}, {"*v", ".env", true}, {"*x", ".env", false},
		{"[.]env", ".env", true}, {"[]]", "]", true}, {"[!]]", "a", true}, {"[a-", "a", false},
		{"[[:digit:]]", "5", true}, {"[[:nope:]]", "a", false}, {"\\*", "*", true}, {"\\", "a", false},
		{"a\\", "a", false}, {"?", "", false}, {"[\\]]", "]", true}, {"[a\\-z]", "-", true},
	} {
		if got := wildmatch(c.pattern, c.name); got != c.want {
			t.Errorf("wildmatch(%q, %q) = %v", c.pattern, c.name, got)
		}
	}
}
