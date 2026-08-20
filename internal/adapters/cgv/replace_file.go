//go:build !windows

package cgv

import "os"

func replaceFileAtomic(source, target string) error {
	return os.Rename(source, target)
}
