package config

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

var tailnetRange = mustCIDR("100.64.0.0/10")

// ValidatePrivateOrigin accepts an HTTP(S) origin whose host is loopback,
// private, link-local, tailnet, or a private DNS name. Credentials, paths,
// queries, and fragments are rejected so callers cannot accidentally grant a
// remote service a broader URL boundary than intended.
func ValidatePrivateOrigin(raw string) error {
	origin, err := url.ParseRequestURI(raw)
	if err != nil {
		return err
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return errors.New("must use http or https")
	}
	if origin.Hostname() == "" {
		return errors.New("must include a host")
	}
	if origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" ||
		(origin.Path != "" && origin.Path != "/") {
		return errors.New("must be an origin without credentials, query, fragment, or path")
	}
	if !isPrivateHost(origin.Hostname()) {
		return errors.New("must target a private host")
	}
	return nil
}

func isPrivateHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() || ip.IsMulticast() {
			return false
		}
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || tailnetRange.Contains(ip)
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "localhost" || !strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".local")
}

func mustCIDR(cidr string) *net.IPNet {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	return network
}
