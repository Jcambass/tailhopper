package pac

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jcambass/tailhopper/internal/tailscale"
)

func mockTailnet(id int, suffix string, socksPort int) *tailscale.Tailnet {
	return tailscale.NewTailnet(id, "/tmp/test", "host", suffix, "", false, socksPort, nil, nil, nil, nil, nil)
}

func TestBuildPACForTailnets_Empty(t *testing.T) {
	pac, suffixes := buildPACForTailnets(nil)

	if len(suffixes) != 0 {
		t.Errorf("expected 0 suffixes, got %d", len(suffixes))
	}
	if !strings.Contains(pac, "FindProxyForURL") {
		t.Error("expected PAC to contain FindProxyForURL function")
	}
	if !strings.Contains(pac, `return "DIRECT"`) {
		t.Error("expected PAC to contain DIRECT return")
	}
}

func TestBuildPACForTailnets_SingleTailnet(t *testing.T) {
	tailnets := []*tailscale.Tailnet{
		mockTailnet(1, "my-tailnet.ts.net", 1080),
	}

	pac, suffixes := buildPACForTailnets(tailnets)

	if len(suffixes) != 1 || suffixes[0] != "my-tailnet.ts.net" {
		t.Errorf("expected suffix [my-tailnet.ts.net], got %v", suffixes)
	}
	if !strings.Contains(pac, "*.my-tailnet.ts.net") {
		t.Error("expected PAC to match *.my-tailnet.ts.net")
	}
	if !strings.Contains(pac, "SOCKS5 localhost:1080") {
		t.Error("expected PAC to contain SOCKS5 proxy directive")
	}
	if !strings.Contains(pac, "SOCKS localhost:1080") {
		t.Error("expected PAC to contain SOCKS fallback")
	}
}

func TestBuildPACForTailnets_MultipleTailnets(t *testing.T) {
	tailnets := []*tailscale.Tailnet{
		mockTailnet(1, "first.ts.net", 1080),
		mockTailnet(2, "second.ts.net", 1081),
	}

	pac, suffixes := buildPACForTailnets(tailnets)

	if len(suffixes) != 2 {
		t.Errorf("expected 2 suffixes, got %d", len(suffixes))
	}
	if !strings.Contains(pac, "*.first.ts.net") {
		t.Error("expected PAC to match *.first.ts.net")
	}
	if !strings.Contains(pac, "*.second.ts.net") {
		t.Error("expected PAC to match *.second.ts.net")
	}
	if !strings.Contains(pac, "SOCKS5 localhost:1080") {
		t.Error("expected PAC to route first.ts.net through port 1080")
	}
	if !strings.Contains(pac, "SOCKS5 localhost:1081") {
		t.Error("expected PAC to route second.ts.net through port 1081")
	}
}

func TestBuildPACForTailnets_SkipsUnconfigured(t *testing.T) {
	tailnets := []*tailscale.Tailnet{
		mockTailnet(1, "configured.ts.net", 1080),
		mockTailnet(2, "", 1081),
	}

	pac, suffixes := buildPACForTailnets(tailnets)

	if len(suffixes) != 1 {
		t.Errorf("expected 1 suffix, got %d: %v", len(suffixes), suffixes)
	}
	if strings.Contains(pac, "1081") {
		t.Error("PAC should not contain unconfigured tailnet's port")
	}
}

func TestWritePAC(t *testing.T) {
	w := httptest.NewRecorder()
	writePAC(w, "test content")

	resp := w.Result()
	if resp.Header.Get("Content-Type") != "application/x-ns-proxy-autoconfig" {
		t.Errorf("Content-Type = %q, want application/x-ns-proxy-autoconfig", resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("Cache-Control") != "no-cache, no-store, must-revalidate" {
		t.Errorf("Cache-Control = %q", resp.Header.Get("Cache-Control"))
	}
	if resp.Header.Get("Pragma") != "no-cache" {
		t.Errorf("Pragma = %q", resp.Header.Get("Pragma"))
	}
	if resp.Header.Get("Expires") != "0" {
		t.Errorf("Expires = %q", resp.Header.Get("Expires"))
	}

	body := w.Body.String()
	if body != "test content" {
		t.Errorf("body = %q, want %q", body, "test content")
	}
}

func TestURLPath(t *testing.T) {
	if URLPath != "/proxy.pac" {
		t.Errorf("URLPath = %q, want /proxy.pac", URLPath)
	}
}
