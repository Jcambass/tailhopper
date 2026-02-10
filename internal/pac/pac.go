// Package pac provides PAC (Proxy Auto-Configuration) file generation and serving.
package pac

import (
	"fmt"
	"net/http"

	"github.com/jcambass/tailhopper/internal/logging"
	"github.com/jcambass/tailhopper/internal/ts"
)

// URLPath is the default URL path for serving the PAC file.
const URLPath = "/proxy.pac"

func Handler(tailnet *ts.Tailnet, socksAddr string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := logging.FromContext(r.Context()).With("component", "pac")
		connected, suffix := tailnet.State.Connected()
		if !connected {
			http.Error(w, "not connected to Tailnet yet", http.StatusServiceUnavailable)
			logger.Printf("PAC requested while tailnet disconnected")
			return
		}

		content := fmt.Sprintf(`function FindProxyForURL(url, host) {
    // Route all *.%s traffic through SOCKS5 proxy
    if (shExpMatch(host, "*.%s")) {
        return "SOCKS5 %s; SOCKS %s; DIRECT";
    }
    // Direct connection for everything else
    return "DIRECT";
}
`, suffix, suffix, socksAddr, socksAddr)

		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		w.Header().Set("Content-Disposition", "inline; filename=\"proxy.pac\"")
		w.Write([]byte(content))
	}
}
