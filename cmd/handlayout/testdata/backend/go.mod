module example.com/backend

go 1.22

require (
	github.com/go-widgets/painter v0.22.0
	github.com/go-widgets/toolkit v0.409.0
)

replace (
	github.com/go-widgets/painter => ../stubs/painter
	github.com/go-widgets/toolkit => ../stubs/toolkit
)
