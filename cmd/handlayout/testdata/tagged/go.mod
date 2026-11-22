module example.com/tagged

go 1.22

require (
	github.com/go-widgets/painter v0.17.1
	github.com/go-widgets/toolkit v0.395.3
)

replace (
	github.com/go-widgets/painter => ../stubs/painter
	github.com/go-widgets/toolkit => ../stubs/toolkit
)
