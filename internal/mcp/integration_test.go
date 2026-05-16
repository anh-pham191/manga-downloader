package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/pipeline"
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

func TestTool_ListAndInspect(t *testing.T) {
	srv, client := newTestPair(t)
	writeFakeArchive(t, srv.opts.Root, "Foo.cbz", []string{"chap-001/001.jpg"})

	listRes, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "list_manga"})
	if err != nil {
		t.Fatal(err)
	}
	var list []MangaEntry
	mustUnmarshalStructuredAs(t, listRes, &struct {
		Items *[]MangaEntry `json:"items"`
	}{Items: &list})
	if len(list) != 1 || list[0].Name != "Foo" {
		t.Fatalf("list = %+v", list)
	}

	inspRes, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "inspect_manga",
		Arguments: map[string]any{"name": "Foo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var entry MangaEntry
	mustUnmarshalStructured(t, inspRes, &entry)
	if entry.Chapters != 1 {
		t.Fatalf("chapters = %d", entry.Chapters)
	}

	missing, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "inspect_manga",
		Arguments: map[string]any{"name": "Nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !missing.IsError || missing.Meta["error_code"] != CodeNoArchive {
		t.Fatalf("expected NO_ARCHIVE, got IsError=%v meta=%v text=%q",
			missing.IsError, missing.Meta["error_code"], contentText(missing))
	}
}

func mustUnmarshalStructuredAs(t *testing.T, res *sdk.CallToolResult, wrapper any) {
	t.Helper()
	if res.StructuredContent == nil {
		t.Fatalf("no structured content; text: %s", contentText(res))
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, wrapper); err != nil {
		t.Fatal(err)
	}
}

func TestTool_SyncCFExpiredFlow(t *testing.T) {
	srv, client := newTestPair(t)
	srv.sync.runFn = func(ctx context.Context, _ pipeline.Opts) error {
		return fetcher.ErrCloudflareExpired
	}
	// Seed a cookie file so LoadCookieFile succeeds.
	mustWriteJSON(t, srv.opts.CookiesPath, fetcher.CookieFile{
		Cookies: []fetcher.CookieRecord{{Name: "cf_clearance", Value: "x", Domain: ".x"}},
	})

	res, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "resume",
		Arguments: map[string]any{"url": "https://truyenqqko.com/truyen-tranh/x-1", "name": "X"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected IsError on CF expiry")
	}
	if got := res.Meta["error_code"]; got != CodeCFTokenExpired {
		t.Fatalf("Meta.error_code = %v, want %q", got, CodeCFTokenExpired)
	}
	if !strings.Contains(contentText(res), CodeCFTokenExpired) {
		t.Fatalf("missing %s in %q", CodeCFTokenExpired, contentText(res))
	}
}

func TestTool_CancelRun(t *testing.T) {
	srv, client := newTestPair(t)
	mustWriteJSON(t, srv.opts.CookiesPath, fetcher.CookieFile{
		Cookies: []fetcher.CookieRecord{{Name: "cf_clearance", Value: "x", Domain: ".x"}},
	})

	started := make(chan struct{})
	srv.sync.runFn = func(ctx context.Context, _ pipeline.Opts) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	go func() {
		_, _ = client.CallTool(context.Background(), &sdk.CallToolParams{
			Name:      "resume",
			Arguments: map[string]any{"url": "https://example.com/x-1", "name": "X"},
		})
	}()
	<-started

	cancelRes, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "cancel_run"})
	if err != nil {
		t.Fatal(err)
	}
	if cancelRes.IsError {
		t.Fatalf("cancel returned error: %s", contentText(cancelRes))
	}
	// Allow the goroutine to settle.
	deadline := time.Now().Add(2 * time.Second)
	for srv.runState.Snapshot() != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.runState.Snapshot() != nil {
		t.Fatal("run still active after cancel")
	}
}
