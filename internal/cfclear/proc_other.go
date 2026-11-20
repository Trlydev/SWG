//go:build !linux

package cfclear

import "os/exec"

// dieWithParent: only Linux can tie a child's life to its parent's this
// simply; elsewhere the kept browser is closed when idle and on quit.
func dieWithParent(*exec.Cmd) {}
