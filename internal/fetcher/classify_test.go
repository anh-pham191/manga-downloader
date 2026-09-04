package fetcher

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Kind
	}{
		{"nil", nil, KindOther},
		{"cloudflare direct", ErrCloudflareExpired, KindCloudflare},
		{"cloudflare wrapped", fmt.Errorf("list chapters: %w", ErrCloudflareExpired), KindCloudflare},
		{"dns", &net.DNSError{Err: "no such host", Name: "truyenqqko.com", IsNotFound: true}, KindHostUnreachable},
		{"dns wrapped in op", &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host"}}, KindHostUnreachable},
		{"conn refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, KindHostUnreachable},
		{"conn reset", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, KindHostUnreachable},
		{"tls hostname", x509.HostnameError{Host: "truyenqqko.com"}, KindHostUnreachable},
		{"tls unknown authority", x509.UnknownAuthorityError{}, KindHostUnreachable},
		{"generic", errors.New("unexpected status 404"), KindOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.err); got != c.want {
				t.Fatalf("Classify(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestKindString(t *testing.T) {
	if KindCloudflare.String() != "cloudflare" || KindHostUnreachable.String() != "host-unreachable" || KindOther.String() != "other" {
		t.Fatal("String() labels wrong")
	}
}
