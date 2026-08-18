package target

import (
	"context"
	"net"
	"testing"
)

func TestValidate(t *testing.T) {
	p := Policy{AllowHTTP: true}
	if _, e := p.Validate("https://example.com/path?x=1"); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Validate("http://127.0.0.1:8080"); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Validate("ftp://example.com"); e == nil {
		t.Fatal("expected scheme rejection")
	}
	if _, e := p.Validate("https://user@example.com"); e == nil {
		t.Fatal("expected userinfo rejection")
	}
}
func TestPrivateDialRejected(t *testing.T) {
	p := Policy{AllowHTTP: true}
	if _, e := p.DialContext(context.Background(), "tcp", "127.0.0.1:80"); e == nil {
		t.Fatal("expected private address rejection")
	}
}
func TestReservedRangesRejected(t *testing.T) {
	for _, raw := range []string{"192.0.2.1", "198.19.0.1", "203.0.113.8", "2001:db8::1"} {
		if safe(net.ParseIP(raw)) {
			t.Errorf("%s should be rejected", raw)
		}
	}
}
func TestAllowlist(t *testing.T) {
	p := Policy{Allowlist: []string{"example.com", "*.vendor.test"}}
	if _, e := p.Validate("https://api.vendor.test"); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Validate("https://other.test"); e == nil {
		t.Fatal("expected allowlist rejection")
	}
}
