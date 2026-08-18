package target

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

type Policy struct {
	AllowHTTP, AllowPrivate bool
	Allowlist               []string
}

func (p Policy) Validate(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return nil, fmt.Errorf("invalid url")
	}
	if u.User != nil || u.Host == "" || u.Hostname() == "" || u.Fragment != "" {
		return nil, fmt.Errorf("url must have a valid host and no userinfo or fragment")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && p.AllowHTTP) {
		return nil, fmt.Errorf("only https targets are allowed")
	}
	host := strings.ToLower(u.Hostname())
	if !p.allowed(host) {
		return nil, fmt.Errorf("target host is not allowlisted")
	}
	return u, nil
}
func (p Policy) allowed(host string) bool {
	if len(p.Allowlist) == 0 {
		return true
	}
	for _, x := range p.Allowlist {
		if x == host || strings.HasPrefix(x, "*.") && strings.HasSuffix(host, x[1:]) {
			return true
		}
	}
	return false
}
func (p Policy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	ips, e := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if e != nil {
		return nil, e
	}
	for _, ip := range ips {
		if !p.AllowPrivate && !safe(ip) {
			continue
		}
		d := net.Dialer{}
		c, e := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("no permitted address for target")
}

var deniedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func safe(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	a, e := netip.ParseAddr(ip.String())
	if e != nil {
		return false
	}
	a = a.Unmap()
	for _, prefix := range deniedPrefixes {
		if prefix.Contains(a) {
			return false
		}
	}
	return a.IsGlobalUnicast()
}
