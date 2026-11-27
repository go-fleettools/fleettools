//go:build unix

package main

import (
	"io/fs"
	"syscall"
)

// fstat is the part of a file's metadata this command needs.
type fstat struct {
	dev, ino, nlink uint64
	alloc           int64 // allocated bytes: st_blocks * 512
}

func statOf(fi fs.FileInfo) fstat {
	if s, ok := fi.Sys().(*syscall.Stat_t); ok {
		return fstat{dev: uint64(s.Dev), ino: uint64(s.Ino), nlink: uint64(s.Nlink), alloc: int64(s.Blocks) * 512}
	}
	return fstat{alloc: fi.Size()}
}
