// Package inbound re-exports from pkg/auth/inbound. See pkg/auth/inbound for documentation.
package inbound

import (
	pkginbound "github.com/gaarutyunov/mcp-anything/pkg/auth/inbound"
)

// DeniedError aliases pkg/auth/inbound.DeniedError.
type DeniedError = pkginbound.DeniedError

// TokenInfo aliases pkg/auth/inbound.TokenInfo.
type TokenInfo = pkginbound.TokenInfo

// TokenValidator aliases pkg/auth/inbound.TokenValidator.
type TokenValidator = pkginbound.TokenValidator

// RegistryReader aliases pkg/auth/inbound.RegistryReader.
type RegistryReader = pkginbound.RegistryReader

// ValidatorSelector aliases pkg/auth/inbound.ValidatorSelector.
type ValidatorSelector = pkginbound.ValidatorSelector

// ValidatorFactory aliases pkg/auth/inbound.ValidatorFactory.
type ValidatorFactory = pkginbound.ValidatorFactory

// Register aliases pkg/auth/inbound.Register.
var Register = pkginbound.Register

// Middleware aliases pkg/auth/inbound.Middleware.
var Middleware = pkginbound.Middleware

// MiddlewareWithSelector aliases pkg/auth/inbound.MiddlewareWithSelector.
var MiddlewareWithSelector = pkginbound.MiddlewareWithSelector

// TokenInfoFromContext aliases pkg/auth/inbound.TokenInfoFromContext.
var TokenInfoFromContext = pkginbound.TokenInfoFromContext

// WellKnownHandler aliases pkg/auth/inbound.WellKnownHandler.
var WellKnownHandler = pkginbound.WellKnownHandler
