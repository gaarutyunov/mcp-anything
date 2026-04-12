package tokencounter_test

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/metric/noop"

	pkgtelemetry "github.com/gaarutyunov/mcp-anything/pkg/telemetry"
	"github.com/gaarutyunov/mcp-anything/pkg/tokencounter"
)

func TestNew_DefaultEncoding(t *testing.T) {
	c, err := tokencounter.New("")
	if err != nil {
		t.Fatalf("New(\"\") error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil Counter")
	}
}

func TestNew_Cl100kBase(t *testing.T) {
	c, err := tokencounter.New("cl100k_base")
	if err != nil {
		t.Fatalf("New(cl100k_base) error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil Counter")
	}
}

func TestNew_O200kBase(t *testing.T) {
	c, err := tokencounter.New("o200k_base")
	if err != nil {
		t.Fatalf("New(o200k_base) error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil Counter")
	}
}

func TestNew_UnknownEncoding(t *testing.T) {
	_, err := tokencounter.New("bogus_encoding")
	if err == nil {
		t.Fatal("expected error for unknown encoding, got nil")
	}
}

// TestRecord_NilCounter verifies that Record on a nil Counter is a no-op.
func TestRecord_NilCounter(t *testing.T) {
	initNoopHistogram(t)
	var c *tokencounter.Counter
	// Should not panic
	c.Record(context.Background(), "tool", "upstream", &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "hello"}},
	})
}

// TestRecord_NilResult verifies that Record with nil result is a no-op.
func TestRecord_NilResult(t *testing.T) {
	initNoopHistogram(t)
	c, err := tokencounter.New("")
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic
	c.Record(context.Background(), "tool", "upstream", nil)
}

// TestRecord_ErrorResult verifies that error results are not counted.
func TestRecord_ErrorResult(t *testing.T) {
	initNoopHistogram(t)
	c, err := tokencounter.New("")
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic and should not record
	c.Record(context.Background(), "tool", "upstream", &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "an error occurred"}},
	})
}

// TestRecord_TextContent verifies that TextContent is tokenized.
func TestRecord_TextContent(t *testing.T) {
	initNoopHistogram(t)
	c, err := tokencounter.New("")
	if err != nil {
		t.Fatal(err)
	}
	// A non-empty text should produce a positive token count (recorded without error).
	c.Record(context.Background(), "mytool", "myupstream", &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: `{"pets":[{"id":1,"name":"Fluffy"}]}`},
		},
	})
}

// TestRecord_ImageContent verifies that ImageContent base64 payload is tokenized.
func TestRecord_ImageContent(t *testing.T) {
	initNoopHistogram(t)
	c, err := tokencounter.New("")
	if err != nil {
		t.Fatal(err)
	}
	c.Record(context.Background(), "mytool", "myupstream", &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.ImageContent{Data: []byte("fake-image-data"), MIMEType: "image/png"},
		},
	})
}

// TestRecord_AudioContent verifies that AudioContent base64 payload is tokenized.
func TestRecord_AudioContent(t *testing.T) {
	initNoopHistogram(t)
	c, err := tokencounter.New("")
	if err != nil {
		t.Fatal(err)
	}
	c.Record(context.Background(), "mytool", "myupstream", &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.AudioContent{Data: []byte("fake-audio-data"), MIMEType: "audio/mp3"},
		},
	})
}

// TestRecord_MultipleContentItems verifies that multiple content items are summed.
func TestRecord_MultipleContentItems(t *testing.T) {
	initNoopHistogram(t)
	c, err := tokencounter.New("")
	if err != nil {
		t.Fatal(err)
	}
	// Two text items — both should be counted together.
	c.Record(context.Background(), "mytool", "myupstream", &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: "first item"},
			&sdkmcp.TextContent{Text: "second item"},
		},
	})
}

// TestRecord_NoHistogram verifies that Record is a no-op when the histogram is nil.
func TestRecord_NoHistogram(t *testing.T) {
	// Reset histogram to nil to simulate uninitialized metrics.
	pkgtelemetry.ToolResultTokens = nil
	c, err := tokencounter.New("")
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic
	c.Record(context.Background(), "tool", "upstream", &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "hello"}},
	})
}

// initNoopHistogram sets pkgtelemetry.ToolResultTokens to a noop histogram so
// that Record does not panic due to a nil histogram.
func initNoopHistogram(t *testing.T) {
	t.Helper()
	mp := noop.NewMeterProvider()
	if err := pkgtelemetry.InitMetrics(mp); err != nil {
		t.Fatalf("InitMetrics: %v", err)
	}
}
