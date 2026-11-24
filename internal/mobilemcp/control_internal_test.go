package mobilemcp

import "time"

// SetProgressEvery changes how often a waiting control tool sends progress, and gives the old value back.
func SetProgressEvery(d time.Duration) (old time.Duration) {
	old, progressEvery = progressEvery, d
	return old
}
