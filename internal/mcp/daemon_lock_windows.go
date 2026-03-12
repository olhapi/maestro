//go:build windows

package mcp

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

var errDaemonLockAlreadyHeld = errors.New("daemon lock already held")

func tryLockFile(lockFile *os.File) error {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(
		windows.Handle(lockFile.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		^uint32(0),
		^uint32(0),
		&overlapped,
	)
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errDaemonLockAlreadyHeld
	}
	return err
}

func unlockFile(lockFile *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(
		windows.Handle(lockFile.Fd()),
		0,
		^uint32(0),
		^uint32(0),
		&overlapped,
	)
}
