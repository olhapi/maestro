//go:build !windows

package mcp

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var errDaemonLockAlreadyHeld = errors.New("daemon lock already held")

func tryLockFile(lockFile *os.File) error {
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return errDaemonLockAlreadyHeld
		}
		return err
	}
	return nil
}

func unlockFile(lockFile *os.File) error {
	return unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
}
