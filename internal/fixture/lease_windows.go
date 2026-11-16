package fixture

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile locks the first byte of the open file. A lock belongs to the handle, so a second
// handle in the same process conflicts with the first.
func lockFile(f *os.File) (bool, error) {
	var ol windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION):
		return false, nil
	}
	return false, err
}
