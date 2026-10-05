module github.com/go-fleettools/fleettools

go 1.27.1

// ⛔ v0.1.0 CONTAINS TWO COMPILED EXECUTABLES, 8.4 MB of them: `gopin` and
// `docscan`, tracked at the repository root because there was no .gitignore at
// all and `git add -A` does not ask. The tag was cut over that tree.
//
// It cannot be unpublished. proxy.golang.org holds its own copy of the zip
// (5 168 705 bytes, measured) and sum.golang.org has the hash in an append-only
// transparency log:
//
//	github.com/go-fleettools/fleettools v0.1.0 h1:s3jd7LmaEsFsBMyyPfp1qucMNYOh0qO4QWeHBI8CNxU=
//
// Deleting the git tag would change nothing. So the honest remedy is the one Go
// provides for exactly this: say so, in the module, where `go get` will read it.
retract v0.1.0 // tracked 8.4 MB of compiled executables; use v0.1.1 or later
