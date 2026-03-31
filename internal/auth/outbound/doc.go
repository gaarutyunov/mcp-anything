// Package outbound implements per-upstream outbound authentication providers.
// Supported strategies are static Bearer token, API key injection, OAuth2
// client credentials (with automatic token refresh), and Lua scripting.
// Providers are instantiated at config load time and called per upstream request.
package outbound
