//go:build !windows

package instance

import (
	"os"
	"syscall"
)

type lockHandle *os.File

func tryLock(path string) (lockHandle, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		return nil, err
	}

	return f, nil
}

func writePID(fd lockHandle, _ string, pid string) {
	f := (*os.File)(fd)
	f.Truncate(0)
	f.Seek(0, 0)
	f.WriteString(pid)
	f.Sync()
}

func unlock(fd lockHandle) {
	f := (*os.File)(fd)
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}
