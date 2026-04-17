package outbound

import (
	"errors"
	"fmt"
	"net/http"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServeWithProvider resolves credentials from p and calls next with the appropriate
// context values set. Sub-packages call this to implement their Wrap method.
//
// On success, auth headers are stored via withHeaders.
// On AuthRequiredError (e.g. OAuth2 user redirect needed), a CallToolResult is stored
// via withAuthResult so the terminal handler can return it without making an upstream call.
// On any other error, an error CallToolResult is stored via withAuthResult.
//
// next is always called so that the terminal handler can write the result to the pipeline state.
// The terminal handler must check AuthResultFromContext before proceeding with the HTTP call.
func ServeWithProvider(w http.ResponseWriter, r *http.Request, next http.Handler, p TokenProvider) {
	ctx := r.Context()

	rawHeaders, err := p.RawHeaders(ctx)
	if err != nil {
		ctx = withAuthResult(ctx, authErrResult(err))
		next.ServeHTTP(w, r.WithContext(ctx))
		return
	}

	if len(rawHeaders) > 0 {
		ctx = withHeaders(ctx, rawHeaders)
	} else {
		token, tokenErr := p.Token(ctx)
		if tokenErr != nil {
			ctx = withAuthResult(ctx, authErrResult(tokenErr))
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if token != "" {
			ctx = withHeaders(ctx, map[string]string{"Authorization": "Bearer " + token})
		}
	}

	next.ServeHTTP(w, r.WithContext(ctx))
}

// authErrResult converts an outbound auth error into a CallToolResult.
// AuthRequiredError produces the OAuth2 authorization redirect message.
func authErrResult(err error) *sdkmcp.CallToolResult {
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
