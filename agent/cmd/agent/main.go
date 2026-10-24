// Command agent is the kpv agent. It is a skeleton: it has no routes yet, those
// arrive with issue #4.
package main

import (
	"fmt"

	"github.com/zssvaidar/kpv/agent/internal/version"
)

func main() {
	fmt.Printf("kpv agent %s\n", version.String())
}
