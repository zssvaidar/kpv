package version_test

import (
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/version"
)

func TestStringReportsCurrentVersion(t *testing.T) {
	if got := version.String(); got != version.Current {
		t.Errorf("version.String() = %q, want %q", got, version.Current)
	}
}
