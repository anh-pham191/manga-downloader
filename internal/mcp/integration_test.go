package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTool_UpdateCookieAndStatus(t *testing.T) {
	_, client := newTestPair(t)

	res, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "update_cookie",
		Arguments: map[string]any{
			"value":  "ABCDEFGHIJKLMNOP",
			"domain": ".example.com",
		},
	})
	if err != nil {
		t.Fatalf("update_cookie: %v", err)
	}
	if res.IsError {
		t.Fatalf("update_cookie returned error: %v", contentText(res))
	}

	got, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "get_cookie_status",
	})
	if err != nil {
		t.Fatalf("get_cookie_status: %v", err)
	}
	var out CookieStatusResult
	mustUnmarshalStructured(t, got, &out)
	if !out.HasClearance {
		t.Fatal("expected HasClearance=true after update_cookie")
	}
	if out.Last8 != "IJKLMNOP" {
		t.Fatalf("Last8 = %q", out.Last8)
	}
	if !strings.HasSuffix(out.CookiePath, "cookies.json") {
		t.Fatalf("cookie_path = %q", out.CookiePath)
	}
}

func TestTool_UpdateCookieRejectsEmpty(t *testing.T) {
	_, client := newTestPair(t)
	res, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "update_cookie",
		Arguments: map[string]any{"value": ""},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true on empty value")
	}
	// Two channels for the code so Claude can branch deterministically
	// without parsing free-form text: Meta.error_code, plus the body.
	if got := res.Meta["error_code"]; got != CodeBadInput {
		t.Fatalf("Meta.error_code = %v, want %q", got, CodeBadInput)
	}
	if !strings.Contains(contentText(res), CodeBadInput) {
		t.Fatalf("error text %q must contain code %q", contentText(res), CodeBadInput)
	}
}

func contentText(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*sdk.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// mustUnmarshalStructured round-trips res.StructuredContent through
// JSON into dst. The SDK field is `any` containing the raw Output
// struct the handler returned; marshalling it produces the same JSON
// the wire would carry. If a future SDK version wraps this in a
// container type, drill into the wrapper's exported value field here.
func mustUnmarshalStructured(t *testing.T, res *sdk.CallToolResult, dst any) {
	t.Helper()
	if res.StructuredContent == nil {
		t.Fatalf("no structured content; got text: %s", contentText(res))
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		t.Fatalf("unmarshal %s into %T: %v", b, dst, err)
	}
}
