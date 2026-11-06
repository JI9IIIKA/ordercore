package platform

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

func PublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}
func ValidateURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return Bad("absolute http(s) URL without credentials required")
	}
	port := u.Port()
	if port != "" && port != "80" && port != "443" {
		return Bad("only ports 80 and 443 allowed")
	}
	if ip, e := netip.ParseAddr(u.Hostname()); e == nil && !PublicIP(ip) {
		return Bad("non-public address forbidden")
	}
	return nil
}

// Resolve inside DialContext and dial the validated IP, preventing DNS rebinding.
func SafeClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil {
			return nil, e
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("empty DNS response")
		}
		for _, ip := range ips {
			if !PublicIP(ip) {
				return nil, fmt.Errorf("non-public address forbidden")
			}
		}
		var dial net.Dialer
		return dial.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}}}
}
