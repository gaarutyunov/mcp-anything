// Package openapi re-exports from pkg/openapi. See pkg/openapi for documentation.
package openapi

import (
	pkgopenapi "github.com/gaarutyunov/mcp-anything/pkg/openapi"
)

// Type aliases — transparent to all callers; no behavior change.
// See pkg/openapi for full documentation of each type.

// ParamInfo holds metadata about a single OpenAPI parameter used for HTTP routing.
// See pkg/openapi.ParamInfo.
type ParamInfo = pkgopenapi.ParamInfo

// GeneratedTool associates an MCP tool definition with routing information.
// See pkg/openapi.GeneratedTool.
type GeneratedTool = pkgopenapi.GeneratedTool

// Validator holds a pre-built kin-openapi router for a single upstream spec.
// See pkg/openapi.Validator.
type Validator = pkgopenapi.Validator

// ValidatedTool is a GeneratedTool with its compiled jq transforms and runtime validator.
// See pkg/openapi.ValidatedTool.
type ValidatedTool = pkgopenapi.ValidatedTool

// ArgMapping maps "location:originalName" to the actual MCP argument name.
// See pkg/openapi.ArgMapping.
type ArgMapping = pkgopenapi.ArgMapping

// PrefixedTool holds the naming metadata for a generated tool.
// See pkg/openapi.PrefixedTool.
type PrefixedTool = pkgopenapi.PrefixedTool

// Function vars — forward all calls to pkg/openapi.

// LoadPipeline executes the full OpenAPI loading pipeline for a single upstream.
// See pkg/openapi.LoadPipeline.
var LoadPipeline = pkgopenapi.LoadPipeline

// FetchSpecConditional fetches spec bytes, optionally using conditional GET.
// See pkg/openapi.FetchSpecConditional.
var FetchSpecConditional = pkgopenapi.FetchSpecConditional

// LoadPipelineFromBytes runs the OpenAPI loading pipeline from pre-fetched spec bytes.
// See pkg/openapi.LoadPipelineFromBytes.
var LoadPipelineFromBytes = pkgopenapi.LoadPipelineFromBytes

// ApplyOverlay loads an overlay from the given config and applies it to the spec bytes.
// See pkg/openapi.ApplyOverlay.
var ApplyOverlay = pkgopenapi.ApplyOverlay

// ApplyOverlayBytes applies pre-loaded overlay bytes to spec bytes.
// See pkg/openapi.ApplyOverlayBytes.
var ApplyOverlayBytes = pkgopenapi.ApplyOverlayBytes

// FetchOverlayConditional fetches overlay bytes using conditional GET.
// See pkg/openapi.FetchOverlayConditional.
var FetchOverlayConditional = pkgopenapi.FetchOverlayConditional

// GenerateTools walks all operations in the OpenAPI document and returns MCP tools.
// See pkg/openapi.GenerateTools.
var GenerateTools = pkgopenapi.GenerateTools

// FindOperationYAMLNode navigates a parsed YAML spec tree to find the yaml.Node for an operation.
// See pkg/openapi.FindOperationYAMLNode.
var FindOperationYAMLNode = pkgopenapi.FindOperationYAMLNode

// NewValidator creates a Validator from a parsed OpenAPI document and its pre-built router.
// See pkg/openapi.NewValidator.
var NewValidator = pkgopenapi.NewValidator

// ValidateUpstream runs full config-time validation for a single upstream.
// See pkg/openapi.ValidateUpstream.
var ValidateUpstream = pkgopenapi.ValidateUpstream

// ValidateTool compiles jq expressions and runs dry-run validation for a single tool.
// See pkg/openapi.ValidateTool.
var ValidateTool = pkgopenapi.ValidateTool

// DeriveArgMapping builds the mapping from (location, name) to MCP arg name.
// See pkg/openapi.DeriveArgMapping.
var DeriveArgMapping = pkgopenapi.DeriveArgMapping

// DeriveInputSchema builds the MCP tool InputSchema from an OpenAPI operation.
// See pkg/openapi.DeriveInputSchema.
var DeriveInputSchema = pkgopenapi.DeriveInputSchema

// Slugify derives a tool base name from HTTP method and path.
// See pkg/openapi.Slugify.
var Slugify = pkgopenapi.Slugify

// ToolBaseName returns the base name (without upstream prefix) for a tool.
// See pkg/openapi.ToolBaseName.
var ToolBaseName = pkgopenapi.ToolBaseName

// PrefixedName returns "{prefix}{separator}{baseName}", truncating baseName as needed.
// See pkg/openapi.PrefixedName.
var PrefixedName = pkgopenapi.PrefixedName

// TruncateDescription truncates desc to at most maxLength runes.
// See pkg/openapi.TruncateDescription.
var TruncateDescription = pkgopenapi.TruncateDescription

// DetectConflicts checks for duplicate PrefixedName values and applies the resolution strategy.
// See pkg/openapi.DetectConflicts.
var DetectConflicts = pkgopenapi.DetectConflicts

// Generate produces a synthetic JSON value conforming to the given schema.
// See pkg/openapi.Generate.
var Generate = pkgopenapi.Generate

// GenerateThreeInstances produces the three synthetic instances required for dry-run validation.
// See pkg/openapi.GenerateThreeInstances.
var GenerateThreeInstances = pkgopenapi.GenerateThreeInstances
