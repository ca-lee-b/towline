//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package towline

import (
	"os"
	"syscall"
)

func lockJournal(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
