package proxyenv

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigureDNS(t *testing.T) {
	original := net.DefaultResolver
	t.Cleanup(func() { net.DefaultResolver = original })
	t.Setenv("ALL_PROXY", "")
	t.Setenv("all_proxy", "")
	ConfigureDNS()
	assert.Same(t, original, net.DefaultResolver)
	assert.False(t, Enabled())
	t.Setenv("all_proxy", "socks5://127.0.0.1:1080")
	ConfigureDNS()
	assert.True(t, Enabled())
	assert.NotSame(t, original, net.DefaultResolver)
	assert.True(t, net.DefaultResolver.PreferGo)
	require.NotNil(t, net.DefaultResolver.Dial)
}

func TestDialDNSUsesProxy(t *testing.T) {
	for _, variable := range []string{"ALL_PROXY", "all_proxy"} {
		for _, network := range []string{"udp", "tcp"} {
			t.Run(variable+"/"+network, func(t *testing.T) {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				t.Cleanup(func() { assert.NoError(t, listener.Close()) })
				serverResult := make(chan error, 1)
				go func() { serverResult <- serveSOCKS(listener) }()
				t.Setenv("ALL_PROXY", "")
				t.Setenv("all_proxy", "")
				t.Setenv(variable, "socks5://"+listener.Addr().String())
				t.Setenv("NO_PROXY", "*")
				t.Setenv("no_proxy", "*")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				connection, err := DialDNS(ctx, network, "127.0.0.1:53")
				require.NoError(t, err)
				assert.IsType(t, &tls.Conn{}, connection)
				require.NoError(t, connection.Close())
				select {
				case err := <-serverResult:
					require.NoError(t, err)
				case <-ctx.Done():
					t.Fatal("DNS did not reach the SOCKS5 proxy")
				}
			})
		}
	}
}

func TestDialContextUsesProxy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, listener.Close()) })
	serverResult := make(chan error, 1)
	go func() { serverResult <- serveSOCKS(listener) }()
	t.Setenv("ALL_PROXY", "socks5://"+listener.Addr().String())
	t.Setenv("all_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := DialContext(ctx, "tcp", "1.1.1.1:853", &net.Dialer{})
	require.NoError(t, err)
	require.NoError(t, connection.Close())
	select {
	case err := <-serverResult:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("TCP connection did not reach the SOCKS5 proxy")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	connection, err = DialContext(canceled, "tcp", "1.1.1.1:853", &net.Dialer{})
	require.True(t, errors.Is(err, context.Canceled), "unexpected error: %v", err)
	assert.Nil(t, connection)
}

func TestDialDNSRejectsInvalidProxy(t *testing.T) {
	for _, address := range []string{"://invalid", "http://127.0.0.1:8080", "socks5://proxy.example:1080"} {
		t.Run(address, func(t *testing.T) {
			t.Setenv("ALL_PROXY", address)
			t.Setenv("all_proxy", "socks5://127.0.0.1:1080")
			connection, err := DialDNS(context.Background(), "udp", "127.0.0.1:53")
			require.Error(t, err)
			assert.Nil(t, connection)
		})
	}
}

func serveSOCKS(listener net.Listener) (result error) {
	connection, err := listener.Accept()
	if err != nil {
		return err
	}
	defer func() {
		if err := connection.Close(); result == nil {
			result = err
		}
	}()
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	var greeting [2]byte
	if _, err := io.ReadFull(connection, greeting[:]); err != nil {
		return err
	}
	methods := make([]byte, int(greeting[1]))
	if _, err := io.ReadFull(connection, methods); err != nil {
		return err
	}
	if _, err := connection.Write([]byte{5, 0}); err != nil {
		return err
	}
	var request [10]byte
	if _, err := io.ReadFull(connection, request[:]); err != nil {
		return err
	}
	if request[0] != 5 || request[1] != 1 || request[3] != 1 || net.IP(request[4:8]).String() != "1.1.1.1" || binary.BigEndian.Uint16(request[8:]) != 853 {
		return fmt.Errorf("unexpected SOCKS5 DNS destination: %v", request)
	}
	_, err = connection.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	return err
}
