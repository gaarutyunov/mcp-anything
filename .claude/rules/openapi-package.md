---
paths:
  - "internal/openapi/**/*.go"
---

# internal/openapi review lessons

These issues were caught in past code reviews and must be avoided:

- **UTF-8 safety**: String truncation (`desc[:cutAt]`) slices by byte index and can split multi-byte characters. Use `[]rune` conversion for rune-aware truncation when dealing with user-facing strings like descriptions.
- **Parameter collision detection**: When merging path/query/header parameters with request body properties into a single InputSchema, check for name collisions before overwriting. Path parameters must always be treated as required regardless of the `Required` field.
- **HTTP client timeouts**: Never use `http.DefaultClient` without a timeout for remote spec/overlay fetching. Create a client with `Timeout: 30 * time.Second` or pass a configured client.
- **Extension value types**: OpenAPI extensions like `x-mcp-enabled` may be parsed as `bool` or `string` depending on the YAML source. Handle both types (e.g., `"false"` string as well as `false` bool).
- **JSON raw message extraction**: When extracting strings from `json.RawMessage` (e.g., `x-mcp-tool-name`), use `json.Unmarshal` or `strconv.Unquote` instead of manual quote stripping, which fails on escaped quotes.
- **Overlong prefix rejection**: If `prefix + separator` already exceeds `maxLength`, return an error rather than silently producing an overlong tool name.
