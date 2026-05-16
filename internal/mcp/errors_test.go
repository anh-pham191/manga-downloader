package mcp

import (
	"errors"
	"testing"

	"github.com/anhpham/downloader/internal/fetcher"
)

func TestMapError_CFExpired(t *testing.T) {
	te := MapError(fetcher.ErrCloudflareExpired)
	if te.Code != CodeCFTokenExpired {
		t.Fatalf("code = %q, want %q", te.Code, CodeCFTokenExpired)
	}
	if te.Message == "" {
		t.Fatal("message must guide the caller to update_cookie")
	}
}

func TestMapError_Generic(t *testing.T) {
	te := MapError(errors.New("boom"))
	if te.Code != CodeInternal {
		t.Fatalf("code = %q, want INTERNAL", te.Code)
	}
}

func TestMapError_Nil(t *testing.T) {
	if MapError(nil) != nil {
		t.Fatal("MapError(nil) must return nil")
	}
}
