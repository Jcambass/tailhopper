// Package pac provides PAC (Proxy Auto-Configuration) file generation and serving.
package pac

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jcambass/tailhopper/internal/registry"
	"github.com/jcambass/tailhopper/internal/ts"
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
		snapshot := t.Snapshot()
		suffix := snapshot.MagicDNSSuffix
		// Skip tailnets without a claimed MagicDNS suffix
		if suffix == "" {
			continue
		}

		suffixes = append(suffixes, suffix)
		socksAddr := t.SocksAddr()

		sb.WriteString(fmt.Sprintf("    if (shExpMatch(host, \"*.%s\")) {\n", suffix))
		sb.WriteString(fmt.Sprintf("        return \"SOCKS5 %s; SOCKS %s; DIRECT\";\n", socksAddr, socksAddr))
		sb.WriteString("    }\n")
	}

	sb.WriteString("    return \"DIRECT\";\n")
	sb.WriteString("}\n")
	return sb.String(), suffixes
}

func Handler(reg *registry.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		pac, suffixes := buildPACForTailnets(reg.List())
		slog.InfoContext(ctx, "Serving PAC file", slog.String("component", "pac"), slog.String("suffixes", strings.Join(suffixes, ", ")))
		writePAC(w, pac)
	}
}
