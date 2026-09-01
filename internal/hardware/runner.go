package hardware

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// CLI is the real Runner. The binary path is bootstrap configuration (plan.md
// 21), never hard-coded.
type CLI struct {
	binary string
}

var _ Runner = (*CLI)(nil)

// NewCLI returns a Runner shelling out to the ffmpeg at binary.
func NewCLI(binary string) *CLI { return &CLI{binary: binary} }

// Run returns stdout and stderr together: `-version` prints to stdout while a
// codec failure prints to stderr, and the probe reads both.
func (c *CLI) Run(ctx context.Context, args []string) (string, error) {
	//nolint:gosec // G204: the binary is injected bootstrap config (21) and the args are built in this package.
	cmd := exec.CommandContext(ctx, c.binary, args...)

	var out bytes.Buffer

	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("run %s: %w", c.binary, err)
	}

	return out.String(), nil
}
