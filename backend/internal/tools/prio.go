package tools

import "golang.org/x/sys/unix"

func setPriority(pid, prio int) error {
	return unix.Setpriority(unix.PRIO_PROCESS, pid, prio)
}
