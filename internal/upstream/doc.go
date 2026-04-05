// Package upstream defines the core registry, builder, and executor interfaces
// for mcp-anything upstreams. Concrete implementations (HTTP, command, script)
// live in sub-packages and register via init() for IoC-based discovery.
// Import the desired sub-packages for side effects, or use the all/ convenience
// package to register all built-in upstream types.
package upstream
