package cfclear

import (
	"os"
	"os/exec"
	"syscall"
)

// dieWithParent has the kernel end the browser if ppgmods itself dies
// (a crash, a kill), so a browser kept running between reads is never left
// behind. chromedp does the same for Chrome.
func dieWithParent(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

// tieToParent: Linux ties the browser to ppgmods before it starts
// (dieWithParent).
func tieToParent(*os.Process) {}
