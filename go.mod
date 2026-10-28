module github.com/go-fleettools/fleettools

go 1.27.3

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

require (
	github.com/go-kramdown/kramdown v0.3.1
	github.com/go-pdfkit/extract v0.9.0
	github.com/go-pdfkit/html2pdf v0.3.0
	github.com/go-pdfkit/reader v0.9.0
	github.com/spf13/cobra v1.10.2
	github.com/ulikunitz/xz v0.5.17
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/mod v0.42.0
	golang.org/x/net v0.62.0
	golang.org/x/text v0.43.0
	golang.org/x/tools v0.52.0
	golang.org/x/vuln v1.9.0
)

require (
	github.com/andybalholm/brotli v1.2.6 // indirect
	github.com/breml/rootcerts v0.3.7 // indirect
	github.com/dlclark/regexp2/v2 v2.5.2 // indirect
	github.com/dop251/goja v0.0.0-20260926152631-39ec2650adc9 // indirect
	github.com/evanw/esbuild v0.28.2 // indirect
	github.com/go-browserhttp/browserhttp v0.2.0 // indirect
	github.com/go-gfx/gfx v0.36.1 // indirect
	github.com/go-gfx/oksvg v0.2.0 // indirect
	github.com/go-images/gif v0.2.0 // indirect
	github.com/go-images/images v0.1.0 // indirect
	github.com/go-images/jpeg v0.3.0 // indirect
	github.com/go-images/jpeg2000 v0.13.8 // indirect
	github.com/go-images/png v0.2.0 // indirect
	github.com/go-opentype/fonts v0.14.0 // indirect
	github.com/go-opentype/opentype v0.16.0 // indirect
	github.com/go-pdfkit/pdffont v0.4.0 // indirect
	github.com/go-pdfkit/pdfkit v0.20.0 // indirect
	github.com/go-regexp/engine v0.3.2 // indirect
	github.com/go-rouge/rouge v0.7.1 // indirect
	github.com/go-ruby-regexp/regexp v0.1.1 // indirect
	github.com/go-simd/highway v0.0.14 // indirect
	github.com/go-sourcemap/sourcemap v2.1.4+incompatible // indirect
	github.com/go-webengine/engine v0.18.0 // indirect
	github.com/go-webengine/esbuildsandbox v0.1.0 // indirect
	github.com/go-widgets/painter v0.16.0 // indirect
	github.com/google/pprof v0.0.0-20240727154555-813a5fbdbec8 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/klauspost/compress v1.18.7 // indirect
	github.com/refraction-networking/utls v1.8.2 // indirect
	github.com/sergeymakinen/go-bmp v1.0.0 // indirect
	github.com/sergeymakinen/go-ico v1.0.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef // indirect
	github.com/tannevaled/gobig2 v0.2.0 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/image v0.47.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/telemetry v0.0.0-20261010193011-f71dc92da45e // indirect
)
