package proxyenv

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"os"
	"time"

	"golang.org/x/net/proxy"
)

const dotTimeout = 15 * time.Second

func proxyAddress() string {
	if address := os.Getenv("ALL_PROXY"); address != "" {
		return address
	}
	return os.Getenv("all_proxy")
}

func Enabled() bool {
	return proxyAddress() != ""
}

func ConfigureDNS() {
	if Enabled() {
		net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: DialDNS}
	}
}

func DialDNS(ctx context.Context, _, _ string) (net.Conn, error) {
	proxyURL, err := url.Parse(proxyAddress())
	if err != nil {
		return nil, fmt.Errorf("invalid DNS proxy URL")
	}
	if net.ParseIP(proxyURL.Hostname()) == nil {
		return nil, fmt.Errorf("DNS proxy endpoint must use an IP literal")
	}
	proxyDialer, err := proxy.FromURL(proxyURL, &net.Dialer{Timeout: dotTimeout})
	if err != nil {
		return nil, fmt.Errorf("create DNS proxy dialer: %w", err)
	}
	dialer, ok := proxyDialer.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("DoT proxy dialer does not support context")
	}
	connection, err := dialer.DialContext(ctx, "tcp", "1.1.1.1:853")
	if err != nil {
		return nil, err
	}
	return tls.Client(connection, &tls.Config{
		ServerName: "cloudflare-dns.com",
		MinVersion: tls.VersionTLS12,
	}), nil
}

func LookupSRV(service, protocol, name string) (string, []*net.SRV, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dotTimeout)
	defer cancel()
	return net.DefaultResolver.LookupSRV(ctx, service, protocol, name)
}

func DialContext(ctx context.Context, network, address string, forward *net.Dialer) (net.Conn, error) {
	dialer, ok := proxy.FromEnvironmentUsing(forward).(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("edge proxy dialer does not support context")
	}
	return dialer.DialContext(ctx, network, address)
}
