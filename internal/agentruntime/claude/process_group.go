package claude

import "time"

const (
	managedProcessTerminateWait = 100 * time.Millisecond
	managedProcessKillWait      = 500 * time.Millisecond
)
