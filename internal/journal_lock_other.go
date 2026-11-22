//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || windows)

package towline

import (
	"errors"
	"os"
)

func lockJournal(*os.File) error {
	return errors.New("durable replay is unsupported on this platform")
}
