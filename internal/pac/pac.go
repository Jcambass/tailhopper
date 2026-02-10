// Package pac provides PAC (Proxy Auto-Configuration) file generation and serving.
package pac

import (
	"fmt"
	"net/http"

	"github.com/jcambass/tailhopper/internal/ts"
)

// URLPath is the default URL path for serving the PAC file.
const URLPath = "/proxy.pac"

func Handler(tailnet *ts.Tailnet, socksAddr string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if tailnet.State.Connected() {
			http.Error(w, "not connected to Tailnet yet", http.StatusServiceUnavailable)
			return
		}
		suffix := tailnet.State.MagicDNSSuffix()

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
