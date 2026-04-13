package transform

// DefaultResponseExpr is the identity jq expression used when no response transform is specified.
const DefaultResponseExpr = "."

// DefaultErrorExpr is the default error transform that handles problem+json and generic errors.
const DefaultErrorExpr = `if .title then
  {error: .title, detail: (.detail // ""), status: (.status // 0)}
else
  {error: ("upstream error: HTTP " + (if .status then (.status | tostring) else "unknown" end)), body: .}
end`
