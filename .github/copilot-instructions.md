# gopowerwall

A Go port of [pypowerwall](https://github.com/jasonacox/pypowerwall): a library and proxy for
the Tesla Energy Gateway / Powerwall.

# Session Management

- Commit and push after completing each task.
- Your max session is 1 hour - when approaching the limit, create `checkpoint.md` at the repo
  root (what is done, what remains, any blockers) and push. Remove `checkpoint.md` when the
  full issue is complete.
- Run `make test` after each task before committing. Resolve all lint issues via `make lint-fix`.
- Min test coverage is 85%.
- Add integration tests in `test/integration/` as you go for everything you implement.
- Run `make build` before pushing.

# Rules

The authoritative coding rules live in `.agent/rules/`:

- `go-style-guide.md` - idiomatic Go, naming, error handling, concurrency, HTTP clients.
- `testing.md` - table tests, testify, parallelism, coverage.
- `workspace.md` - project-wide constraints.
- `powerwall.md` - the pypowerwall parity contract, connection modes, protobuf regeneration.

Read them before writing code. The most frequently violated ones:

- Tests MUST be table tests, always parallel, using `require`/`assert` from testify. Never
  `t.Fatal` or `t.Error`. Use `t.Context()`.
- Logging MUST be via `log/slog`, pulled from the context.
- Errors should be sentinel errors, wrapped with `%w`.
- Avoid `break`; factor the loop body into a function with a fast return.
- Avoid anonymous structs; name the `testCase` type in table tests.
- `nolint` and removing linter rules are forbidden unless no other fix exists.

# Commands

```
make build            # build both binaries into bin/
make lint             # golangci-lint + govulncheck
make lint-fix         # fieldalignment + golangci-lint --fix
make test             # unit tests, race, shuffled
make integration-test # integration tests
make total-coverage   # merged coverage profile and HTML report
make proto            # regenerate protobuf bindings
```
