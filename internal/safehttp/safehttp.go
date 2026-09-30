// Package safehttp builds HTTP clients that only reach public internet hosts.
// Home News fetches feeds and images from URLs it does not control, so every
// outbound request is checked before the request is made and again when the
// connection is dialled; a hostname cannot be re-resolved to a private address
// in between.
package safehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// NewClient returns a client that refuses non-public destinations, limits
// redirects, and validates every redirect target.
func NewClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var last error
		for _, candidate := range ips {
			if !IsPublicIP(candidate.IP) {
				continue
			}
			conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		if last != nil {
			return nil, last
		}
		return nil, errors.New("host resolved only to non-public addresses")
	}
	return &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return ValidatePublicURL(req.URL)
	}}
}

// ValidatePublicURL rejects anything that is not an http(s) URL resolving
// entirely to public unicast addresses.
func ValidatePublicURL(u *url.URL) error {
	if u == nil || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("only http and https URLs are allowed")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("URL has no hostname")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve host: %w", err)
	}
	for _, ip := range ips {
		if !IsPublicIP(ip) {
			return errors.New("host resolves to a non-public address")
		}
	}
	return nil
}

// IsPublicIP reports whether ip is routable on the public internet.
func IsPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		if !v4.IsGlobalUnicast() || v4[0] == 0 || v4[0] >= 224 || v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 || v4[0] == 198 && (v4[1] == 18 || v4[1] == 19) {
			return false
		}
		for _, cidr := range []string{"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.51.100.0/24", "203.0.113.0/24"} {
			_, block, _ := net.ParseCIDR(cidr)
			if block.Contains(v4) {
				return false
			}
		}
		return true
	}
	if !ip.IsGlobalUnicast() {
		return false
	}
	_, documentation, _ := net.ParseCIDR("2001:db8::/32")
	return !documentation.Contains(ip)
}
