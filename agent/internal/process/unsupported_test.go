//go:build !unix

package process_test

import (
	"context"
	"testing"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/process"
)

// Without process groups nothing is started at all.
func TestRunIsUnsupportedOffUnix(t *testing.T) {
	t.Parallel()
	program := process.ForTest("go", []string{"version"}, nil, process.Limits{Timeout: time.Second, MaxStdout: 1024})
	_, err := process.Run(context.Background(), program, nil)
	if process.KindOf(err) != process.KindUnsupported {
		t.Fatalf("got %v, want unsupported", err)
	}
}
