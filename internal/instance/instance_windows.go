//go:build windows

package instance

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type lockHandle windows.Handle

func tryLock(path string) (lockHandle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	h, err := windows.CreateFile(
		p,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, // no sharing — exclusive
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return 0, err
	}

	// Lock the first byte (non-blocking)
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(
		h,
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1, 0,
		ol,
	)
	if err != nil {
		windows.CloseHandle(h)
		return 0, err
	}

	return lockHandle(h), nil
}

func writePID(fd lockHandle, path string, pid string) {
	// Write PID via os.File wrapping
	// We need to write directly since we hold the handle
	data := []byte(pid)
	var written uint32
	windows.WriteFile(windows.Handle(fd), data, &written, nil)
	_ = written
}

func unlock(fd lockHandle) {
	ol := new(windows.Overlapped)
	windows.UnlockFileEx(windows.Handle(fd), 0, 1, 0, ol)
	windows.CloseHandle(windows.Handle(fd))
}

// Keep unsafe import used for compilation (windows.Overlapped uses it internally)
var _ = unsafe.Sizeof(0)
