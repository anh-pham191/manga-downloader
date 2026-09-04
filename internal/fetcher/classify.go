package fetcher

import (
	"crypto/x509"
	"errors"
	"net"
	"syscall"
)

// Kind is a coarse classification of a fetch error, used by update-all
// to decide whether a failure means "refresh the Cloudflare token" or
// "the site moved to a new domain". Anything else is KindOther.
type Kind int

const (
	KindOther Kind = iota
	KindCloudflare
	KindHostUnreachable
)

func (k Kind) String() string {
	switch k {
	case KindCloudflare:
		return "cloudflare"
	case KindHostUnreachable:
		return "host-unreachable"
	default:
		return "other"
	}
}

// Classify inspects err's chain. Cloudflare wins over everything else
// because a 403 is a definite signal; host errors are transport-level
// failures that happen before any HTTP status exists.
func Classify(err error) Kind {
	if err == nil {
		return KindOther
	}
	if errors.Is(err, ErrCloudflareExpired) {
		return KindCloudflare
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return KindHostUnreachable
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return KindHostUnreachable
	}
	var hn x509.HostnameError
	var ua x509.UnknownAuthorityError
	var ci x509.CertificateInvalidError
	if errors.As(err, &hn) || errors.As(err, &ua) || errors.As(err, &ci) {
		return KindHostUnreachable
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return KindHostUnreachable
	}
	return KindOther
}
