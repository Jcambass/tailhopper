// Package pac provides PAC (Proxy Auto-Configuration) file generation and serving.
package pac

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jcambass/tailhopper/internal/logging"
	"github.com/jcambass/tailhopper/internal/ts"
	"tailscale.com/ipn"
)

// URLPath is the default URL path for serving the PAC file.
const URLPath = "/proxy.pac"

func writePAC(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
	w.Header().Set("Content-Disposition", "inline; filename=\"proxy.pac\"")
	// set caching headers to encourage browsers to refresh the PAC file on each request, as the content may change based on Tailnet state
	// No guarantees though.
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Write([]byte(content))
}

func buildPACForTailnets(tailnets []*ts.Tailnet) (string, []string) {
	sb := strings.Builder{}
	sb.WriteString("function FindProxyForURL(url, host) {\n")

	var suffixes []string

	for _, t := range tailnets {
		state := t.LatestState()
		if state.State == nil || *state.State != ipn.Running {
			continue
		}
		if state.MagicDNSSuffix == "" {
			panic("running state without magicDNSSuffix should not happen!")
		}
		suffixes = append(suffixes, state.MagicDNSSuffix)
		socksAddr, ready := t.SocksAddr()
		if !ready {
			continue
		}

		sb.WriteString(fmt.Sprintf("    if (shExpMatch(host, \"*.%s\")) {\n", state.MagicDNSSuffix))
		sb.WriteString(fmt.Sprintf("        return \"SOCKS5 %s; SOCKS %s; DIRECT\";\n", socksAddr, socksAddr))
		sb.WriteString("    }\n")
	}

	sb.WriteString("    return \"DIRECT\";\n")
	sb.WriteString("}\n")
	return sb.String(), suffixes
}

func Handler(registry *ts.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := logging.FromContext(r.Context()).With("component", "pac")
		pac, suffixes := buildPACForTailnets(registry.List())
		logger.Printf("Serving PAC file for suffixes: %s", strings.Join(suffixes, ", "))
		writePAC(w, pac)
	}
}
