package appserver

import (
	"time"
)

const (
	managedProcessTerminateWait = 100 * time.Millisecond
	managedProcessKillWait      = 500 * time.Millisecond
)

func CleanupLingeringAppServerProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	// Once the original process leader exits, the numeric PID is no longer a
	// stable process-group identifier and may have been recycled.
	if !managedProcessLeaderExists(pid) {
		return nil
	}
	if !managedProcessGroupExists(pid) {
		return nil
	}
	return terminateManagedProcessTree(pid, managedProcessTerminateWait, managedProcessKillWait)
}
