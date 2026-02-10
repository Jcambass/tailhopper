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

func buildPACForTailnets(tailnets []*ts.Tailnet) string {
	sb := strings.Builder{}
	sb.WriteString("function FindProxyForURL(url, host) {\n")
	for _, t := range tailnets {
		// TODO: Remove?
		if t.State.Disabled() || t.State.Disabling() {
			continue
		}
		suffix := t.State.BestEffortMagicDNSSuffix()
		if suffix == "" {
			continue
		}
		socksAddr := t.SocksAddr()
		sb.WriteString(fmt.Sprintf("    if (shExpMatch(host, \"*.%s\")) {\n", suffix))
		sb.WriteString(fmt.Sprintf("        return \"SOCKS5 %s; SOCKS %s; DIRECT\";\n", socksAddr, socksAddr))
		sb.WriteString("    }\n")
	}

	sb.WriteString("    return \"DIRECT\";\n")
	sb.WriteString("}\n")
	return sb.String()
}

func Handler(tailnet *ts.Tailnet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := logging.FromContext(r.Context()).With("component", "pac")
		tailnets := []*ts.Tailnet{tailnet}
		writePAC(w, buildPACForTailnets(tailnets))

		tailnetSuffixes := []string{}
		for _, t := range tailnets {
			suffix := t.State.BestEffortMagicDNSSuffix()
			if suffix != "" {
				tailnetSuffixes = append(tailnetSuffixes, suffix)
			}
		}
		logger.Printf("Served PAC file for tailnets: %v", tailnetSuffixes)
	}
}
