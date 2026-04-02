# Common review issues

These patterns have been caught repeatedly by CodeRabbit in past PRs. Check for them before committing.

- `context.Context` must be the first parameter in functions that do I/O — not after `*testing.T` or other args
- Always bounds-check slices before indexing (e.g., check `len(cfg.Upstreams) == 0` before `cfg.Upstreams[0]`)
- HTTP clients used for remote fetching must have explicit timeouts; never use `http.DefaultClient` without a timeout
- Run `go mod tidy` after changing dependencies; ensure direct imports are not marked `// indirect`
- Functions that return `error` should actually use it; do not add unused error returns "for future use"
- Each integration test must use its own clean WireMock instance — never share WireMock between tests to avoid stub conflicts
- String truncation in Go (`s[:n]`) slices by byte index and can split multi-byte UTF-8 characters; use `[]rune` conversion for user-facing strings
