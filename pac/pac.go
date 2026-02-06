// Package pac provides PAC (Proxy Auto-Configuration) file generation and serving.
package pac

import (
	"fmt"
	"net/http"
)

// URLPath is the default URL path for serving the PAC file.
const URLPath = "/proxy.pac"

// BaseDomainGetter provides the Tailnet base domain dynamically.
type BaseDomainGetter interface {
	BaseDomain() string
}

func Handler(bdg BaseDomainGetter, socksAddr string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		baseDomain := bdg.BaseDomain()
		if baseDomain == "" {
			http.Error(w, "not connected to Tailnet yet", http.StatusServiceUnavailable)
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
`, baseDomain, baseDomain, socksAddr, socksAddr)

		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		w.Header().Set("Content-Disposition", "inline; filename=\"proxy.pac\"")
		w.Write([]byte(content))
	}
}
