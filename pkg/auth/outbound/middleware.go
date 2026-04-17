package outbound

import (
	"errors"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// AuthErrResult converts an outbound auth error into a CallToolResult.
// AuthRequiredError produces the OAuth2 authorization redirect message.
func AuthErrResult(err error) *sdkmcp.CallToolResult {
	var authErr *AuthRequiredError
	if errors.As(err, &authErr) {
		return &sdkmcp.CallToolResult{
			IsError: true,
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: "Authorization required. Please visit the following URL to grant access:\n" + authErr.AuthURL},
			},
		}
	}
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: fmt.Sprintf("outbound auth error: %v", err)},
		},
	}
}
