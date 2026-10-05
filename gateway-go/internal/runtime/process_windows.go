//go:build windows

package runtime

import "os/exec"

func configureCommand(cmd *exec.Cmd) {}

func killCommandGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
