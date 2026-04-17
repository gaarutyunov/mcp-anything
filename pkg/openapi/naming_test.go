package openapi

import (
	"strings"
	"testing"

	"github.com/gaarutyunov/mcp-anything/pkg/config"
)

var defaultRules = config.SlugRulesSpec{
	ReplaceSlashes:     true,
	ReplaceBraces:      true,
	ExpandCamelCase:    true,
	Lowercase:          true,
	CollapseSeparators: true,
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		method        string
		path          string
		hasPathParams bool
		want          string
	}{
		{"GET", "/pets", false, "list_pets"},
		{"GET", "/pets/{petId}", true, "get_pets_petid"},
		{"POST", "/pets", false, "create_pets"},
		{"PUT", "/pets/{petId}", true, "update_pets_petid"},
		{"DELETE", "/pets/{petId}", true, "delete_pets_petid"},
		{"PATCH", "/pets/{petId}", true, "patch_pets_petid"},
		{"GET", "/stores/{storeId}/items", true, "get_stores_storeid_items"},
		{"GET", "/v2/users.list", false, "list_v2_users_list"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			got := Slugify(tt.method, tt.path, tt.hasPathParams, defaultRules)
			if got != tt.want {
				t.Errorf("Slugify(%q, %q, %v) = %q, want %q", tt.method, tt.path, tt.hasPathParams, got, tt.want)
			}
		})
	}
}

func TestTruncateDescription(t *testing.T) {
	tests := []struct {
		desc      string
		maxLength int
		suffix    string
		want      string
	}{
		{"", 100, "...", ""},
		{"hello", 10, "...", "hello"},
		{"hello world", 11, "...", "hello world"},
		{"hello world", 10, "...", "hello w..."},
		{"hello world", 0, "...", "hello world"},
		{"hi", 2, "...", "hi"},
		{"abc", 3, "...", "abc"},
		{"abcd", 3, "...", "..."},
		// suffix longer than maxLength — clip to maxLength.
		{"hello", 2, "...", ".."},
	}

	for _, tt := range tests {
		name := strings.ReplaceAll(tt.desc, " ", "_")
		t.Run(name, func(t *testing.T) {
			got := TruncateDescription(tt.desc, tt.maxLength, tt.suffix)
			if got != tt.want {
				t.Errorf("TruncateDescription(%q, %d, %q) = %q, want %q", tt.desc, tt.maxLength, tt.suffix, got, tt.want)
			}
		})
	}
}

func TestPrefixedName(t *testing.T) {
	tests := []struct {
		baseName  string
		prefix    string
		separator string
		maxLength int
		want      string
	}{
		{"list_pets", "shop", "__", 128, "shop__list_pets"},
		{"list_pets", "shop", "__", 20, "shop__list_pets"},
		// "shop__list_pets" is exactly 15 chars — no truncation.
		{"list_pets", "shop", "__", 15, "shop__list_pets"},
		// maxLength=14 truncates the base name by 1.
		{"list_pets", "shop", "__", 14, "shop__list_pet"},
		{"list_pets", "shop", "__", 0, "shop__list_pets"},
		// prefix+sep ("shop__") is 6 runes, maxLength=4 — truncate prefix+sep to maxLength.
		{"list_pets", "shop", "__", 4, "shop"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := PrefixedName(tt.baseName, tt.prefix, tt.separator, tt.maxLength)
			if got != tt.want {
				t.Errorf("PrefixedName(%q, %q, %q, %d) = %q, want %q", tt.baseName, tt.prefix, tt.separator, tt.maxLength, got, tt.want)
			}
			if tt.maxLength > 0 && len(got) > tt.maxLength {
				t.Errorf("result length %d exceeds maxLength %d", len(got), tt.maxLength)
			}
		})
	}
}

func TestDetectConflicts_NoConflicts(t *testing.T) {
	tools := []PrefixedTool{
		{PrefixedName: "shop__list_pets", OriginalPath: "/pets", OriginalMethod: "GET"},
		{PrefixedName: "shop__create_pets", OriginalPath: "/pets", OriginalMethod: "POST"},
	}
	result, err := DetectConflicts(tools, "error")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 tools, got %d", len(result))
	}
}

func TestDetectConflicts_ErrorMode(t *testing.T) {
	tools := []PrefixedTool{
		{PrefixedName: "shop__list_pets", OriginalPath: "/pets", OriginalMethod: "GET"},
		{PrefixedName: "shop__list_pets", OriginalPath: "/animals", OriginalMethod: "GET"},
	}
	_, err := DetectConflicts(tools, "error")
	if err == nil {
		t.Fatal("expected error for conflicting tools, got nil")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("error should mention 'conflict', got: %v", err)
	}
}

func TestDetectConflicts_FirstWins(t *testing.T) {
	tools := []PrefixedTool{
		{PrefixedName: "shop__list_pets", OriginalPath: "/pets", OriginalMethod: "GET"},
		{PrefixedName: "shop__list_pets", OriginalPath: "/animals", OriginalMethod: "GET"},
		{PrefixedName: "shop__create_pets", OriginalPath: "/pets", OriginalMethod: "POST"},
	}
	result, err := DetectConflicts(tools, "first_wins")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 tools after first_wins, got %d", len(result))
	}
	// First occurrence of the duplicate should be kept.
	if result[0].OriginalPath != "/pets" {
		t.Errorf("expected first occurrence (/pets) to be kept, got %q", result[0].OriginalPath)
	}
	if result[1].PrefixedName != "shop__create_pets" {
		t.Errorf("expected create_pets to remain, got %q", result[1].PrefixedName)
	}
}

func TestDetectConflicts_Skip(t *testing.T) {
	tools := []PrefixedTool{
		{PrefixedName: "shop__list_pets", OriginalPath: "/pets", OriginalMethod: "GET"},
		{PrefixedName: "shop__list_pets", OriginalPath: "/animals", OriginalMethod: "GET"},
		{PrefixedName: "shop__create_pets", OriginalPath: "/pets", OriginalMethod: "POST"},
	}
	result, err := DetectConflicts(tools, "skip")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 tool after skip, got %d", len(result))
	}
	if result[0].PrefixedName != "shop__create_pets" {
		t.Errorf("expected create_pets to survive skip, got %q", result[0].PrefixedName)
	}
}

func TestDetectConflicts_UnknownMode(t *testing.T) {
	tools := []PrefixedTool{
		{PrefixedName: "shop__list_pets", OriginalPath: "/pets", OriginalMethod: "GET"},
		{PrefixedName: "shop__list_pets", OriginalPath: "/animals", OriginalMethod: "GET"},
	}
	_, err := DetectConflicts(tools, "unknown_mode")
	if err == nil {
		t.Fatal("expected error for unknown resolution mode")
	}
}

func TestSanitizeIdentifier_CamelCase(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"getGreeting", "get_greeting"},
		{"postEcho", "post_echo"},
		{"JWTAuth", "jwt_auth"},
		{"getHTTPSResponse", "get_https_response"},
		{"listUserAccounts", "list_user_accounts"},
		{"already_snake_case", "already_snake_case"},
		{"MixedABC123Def", "mixed_abc123_def"},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := sanitizeIdentifier(tt.id, defaultRules)
			if got != tt.want {
				t.Errorf("sanitizeIdentifier(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}
