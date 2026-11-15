The go.mod files of github.com/go-numerics/go-numerics.github.io (a real
landing, main at the merge of the go-notebook/playground v0.16.0 bump),
renamed go.mod.fixture so that Renovate and the go tool leave them alone. The
test copies them back to go.mod in a temp directory.

The landing pins github.com/go-notebook/playground in notebooks/go.mod, NOT in
its root go.mod: a reader of root go.mod files alone cannot see it.
