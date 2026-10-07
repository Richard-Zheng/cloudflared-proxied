package connection

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProtocolSelectorWithProxy(t *testing.T) {
	logger := zerolog.Nop()
	for _, variable := range []string{"", "ALL_PROXY", "all_proxy"} {
		for _, protocol := range []string{AutoSelectFlag, "quic", "http2"} {
			t.Run(variable+"/"+protocol, func(t *testing.T) {
				t.Setenv("ALL_PROXY", "")
				t.Setenv("all_proxy", "")
				if variable != "" {
					t.Setenv(variable, "socks5://127.0.0.1:1080")
				}
				selector, err := NewProtocolSelector(protocol, &logger)
				require.NoError(t, err)
				expectedProtocol := HTTP2
				expectedFallback := false
				if variable == "" && protocol != "http2" {
					expectedProtocol = QUIC
					expectedFallback = protocol == AutoSelectFlag
				}
				assert.Equal(t, expectedProtocol, selector.Current())
				fallback, hasFallback := selector.Fallback()
				assert.Equal(t, expectedFallback, hasFallback)
				if hasFallback {
					assert.Equal(t, HTTP2, fallback)
				}
			})
		}
	}
}
