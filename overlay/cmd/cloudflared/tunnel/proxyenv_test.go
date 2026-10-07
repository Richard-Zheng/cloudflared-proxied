package tunnel

import (
	"testing"

	"github.com/cloudflare/cloudflared/features"
)

func TestRunPrechecksWithProxy(t *testing.T) {
	for _, variable := range []string{"ALL_PROXY", "all_proxy"} {
		t.Run(variable, func(t *testing.T) {
			t.Setenv("ALL_PROXY", "")
			t.Setenv("all_proxy", "")
			t.Setenv(variable, "socks5://127.0.0.1:1080")
			runPrechecks(nil, nil, "", features.PostQuantumPrefer)
		})
	}
}
