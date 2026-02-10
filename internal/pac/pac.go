// Package pac provides PAC (Proxy Auto-Configuration) file generation and serving.
package pac

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jcambass/tailhopper/internal/logging"
	"github.com/jcambass/tailhopper/internal/ts"
)

// URLPath is the default URL path for serving the PAC file.
const URLPath = "/proxy.pac"

func writePAC(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
	w.Header().Set("Content-Disposition", "inline; filename=\"proxy.pac\"")
	w.Write([]byte(content))
}

func buildPACForSuffixes(suffixes []string, socksAddr string) string {
	sb := strings.Builder{}
	sb.WriteString("function FindProxyForURL(url, host) {\n")
	for _, suffix := range suffixes {
		if suffix == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("    if (shExpMatch(host, \"*.%s\")) {\n", suffix))
		sb.WriteString(fmt.Sprintf("        return \"SOCKS5 %s; SOCKS %s; DIRECT\";\n", socksAddr, socksAddr))
		sb.WriteString("    }\n")
	}

	sb.WriteString("    return \"DIRECT\";\n")
	sb.WriteString("}\n")
	return sb.String()
}

func Handler(tailnet *ts.Tailnet, socksAddr string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := logging.FromContext(r.Context()).With("component", "pac")
		suffix := tailnet.State.BestEffortMagicDNSSuffix()
		suffixes := []string{suffix}

		writePAC(w, buildPACForSuffixes(suffixes, socksAddr))
		logger.Printf("Served PAC file with suffixes and socksAddr: %v, %s", suffixes, socksAddr)
	}
}
