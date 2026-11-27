//go:build !unix

package main

import "io/fs"

// fstat is the part of a file's metadata this command needs. Without POSIX
// st_blocks the apparent size stands in for the allocated size, and the
// filesystem and hard-link checks are off (dev, ino and nlink stay zero).
type fstat struct {
	dev, ino, nlink uint64
	alloc           int64
}

func statOf(fi fs.FileInfo) fstat { return fstat{alloc: fi.Size()} }
