package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Every failure path, and every warning, with a known value in the write and
// in the files: the value appears in no error string and no log line.
func TestAKnownValueAppearsInNoErrorAndNoLogLine(t *testing.T) {
	t.Parallel()
	value := map[string]string{"KNOWN": canary}
	paths := map[string]func(p *project) (string, map[string]string){
		"invalid key": func(p *project) (string, map[string]string) {
			return p.pipe, map[string]string{"1BAD": canary}
		},
		"newline": func(p *project) (string, map[string]string) {
			return p.pipe, map[string]string{"K": canary + "\n" + canary}
		},
		"nul": func(p *project) (string, map[string]string) {
			return p.pipe, map[string]string{"K": canary + "\x00"}
		},
		"carriage": func(p *project) (string, map[string]string) {
			return p.pipe, map[string]string{"K": "\r" + canary}
		},
		"set outside": func(p *project) (string, map[string]string) {
			write(p.t, p.path(EnvFile), "KNOWN="+canary+"\n")
			return p.pipe, value
		},
		"duplicate": func(p *project) (string, map[string]string) {
			write(p.t, p.path(EnvFile), "D="+canary+"\nD="+canary+"\n")
			return p.pipe, value
		},
		"malformed": func(p *project) (string, map[string]string) {
			write(p.t, p.path(EnvFile), canary+"\nX=\""+canary+"\n")
			return p.pipe, value
		},
		"edited block": func(p *project) (string, map[string]string) {
			write(p.t, p.path(EnvFile), StartMarker+"\nKNOWN=\""+canary+"\"\n"+endMarker(nil)+"\n")
			return p.pipe, value
		},
		"markers": func(p *project) (string, map[string]string) {
			write(p.t, p.path(EnvFile), StartMarker+"\nKNOWN="+canary+"\n")
			return p.pipe, value
		},
		"link env": func(p *project) (string, map[string]string) {
			_ = os.Symlink(canary, p.path(EnvFile))
			return p.pipe, value
		},
		"link gitignore": func(p *project) (string, map[string]string) {
			_ = os.Symlink(canary, p.path(GitignoreFile))
			return p.pipe, value
		},
		"directory": func(p *project) (string, map[string]string) {
			_ = os.Mkdir(p.path(EnvFile), 0o755)
			return p.pipe, value
		},
		"too large": func(p *project) (string, map[string]string) {
			write(p.t, p.path(EnvFile), strings.Repeat("KNOWN="+canary+"\n", MaxFileSize/len(canary)+1))
			return p.pipe, value
		},
		"unreadable": func(p *project) (string, map[string]string) {
			write(p.t, p.path(EnvFile), "KNOWN="+canary+"\n")
			_ = os.Chmod(p.path(EnvFile), 0)
			return p.pipe, value
		},
		"unwritable directory": func(p *project) (string, map[string]string) {
			_ = os.Chmod(p.pipe, 0o555)
			p.t.Cleanup(func() { _ = os.Chmod(p.pipe, 0o755) })
			return p.pipe, value
		},
		"forbidden": func(p *project) (string, map[string]string) { return p.t.TempDir(), value },
		"missing":   func(p *project) (string, map[string]string) { return p.path("missing"), value },
		"not ignored": func(p *project) (string, map[string]string) {
			write(p.t, p.path(GitignoreFile), "!.env\n")
			return p.pipe, value
		},
		"exposed": func(p *project) (string, map[string]string) {
			write(p.t, p.path(EnvFile), "OTHER="+canary+"\n")
			return p.pipe, value
		},
		"interrupted": func(p *project) (string, map[string]string) {
			p.files.hooks.envEmit = func() error { return fmt.Errorf("interrupted with %v", NewSecret(canary)) }
			return p.pipe, value
		},
		"revoked": func(p *project) (string, map[string]string) {
			_, _ = p.guard.Revoke(p.root)
			return p.pipe, value
		},
	}
	for name, setup := range paths {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			dir, values := setup(p)
			report, err := p.files.WriteEnv(context.Background(), dir, secretsOf(values))
			env, readErr := p.files.ReadEnv(dir)
			_, gitErr := p.files.WriteGitignore(context.Background(), dir)
			var out strings.Builder
			for _, e := range []error{err, readErr, gitErr} {
				if e == nil {
					continue
				}
				fmt.Fprintf(&out, "%v\n%+v\n%#v\n%s\n%q\n", e, e, e, e, e)
				for unwrapped := errors.Unwrap(e); unwrapped != nil; unwrapped = errors.Unwrap(unwrapped) {
					fmt.Fprintf(&out, "%v\n", unwrapped)
				}
			}
			fmt.Fprintf(&out, "%+v\n%#v\n%+v\n", report, report, env)
			for _, problem := range append(report.Problems, env.Problems...) {
				out.WriteString(problem.String() + problem.Message + problem.Key)
			}
			out.WriteString(p.logs.String())
			if strings.Contains(out.String(), "Pa55w0rd") {
				t.Fatalf("the value escaped:\n%s", out.String())
			}
			if name != "exposed" && name != "duplicate" && err == nil && readErr == nil && len(report.Problems) == 0 && len(env.Problems) == 0 {
				t.Fatalf("this path did not fail or warn at all")
			}
			if strings.Count(p.logs.String(), "\n") == 0 && os.Geteuid() != 0 {
				t.Fatalf("nothing was logged, so this checked nothing")
			}
		})
	}
}
