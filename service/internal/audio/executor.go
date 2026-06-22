package audio

import "os/exec"

type OSExecutor struct{}

func (OSExecutor) Run(cmd string, args ...string) ([]byte, error) {
	return exec.Command(cmd, args...).Output()
}
