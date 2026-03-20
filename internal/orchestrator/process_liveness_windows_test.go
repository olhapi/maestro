//go:build windows

package orchestrator

func testProcessAlive(pid int) bool {
	return pid > 0
}

func testProcessGroupAlive(pid int) bool {
	return pid > 0
}
