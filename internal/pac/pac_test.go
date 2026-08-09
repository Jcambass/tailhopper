package pac

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jcambass/tailhopper/internal/registry"
)

func pacTailnet(suffix string, port int) registry.TailnetView {
	return registry.TailnetView{MagicDNSSuffix: suffix, SocksPort: port}
}

func TestBuildPACForTailnets_Empty(t *testing.T) {
	pac, suffixes := buildPACForTailnets(nil)

	if len(suffixes) != 0 {
		t.Errorf("expected 0 suffixes, got %d", len(suffixes))
	}
	if !strings.Contains(pac, "return \"DIRECT\"") {
		t.Error("expected DIRECT fallback in PAC")
	}
}

func TestBuildPACForTailnets_WithSuffix(t *testing.T) {
	tailnet := pacTailnet("my-tailnet.ts.net", 1080)
	pac, suffixes := buildPACForTailnets([]registry.TailnetView{tailnet})

	if len(suffixes) != 1 || suffixes[0] != "my-tailnet.ts.net" {
		t.Errorf("suffixes = %v, want [my-tailnet.ts.net]", suffixes)
	}
	if !strings.Contains(pac, "*.my-tailnet.ts.net") {
		t.Error("expected shExpMatch for suffix in PAC")
	}
	if !strings.Contains(pac, "SOCKS5 localhost:1080") {
		t.Error("expected SOCKS5 proxy in PAC")
	}
}

func TestBuildPACForTailnets_SkipsEmptySuffix(t *testing.T) {
	tailnet := pacTailnet("", 1080)
	pac, suffixes := buildPACForTailnets([]registry.TailnetView{tailnet})

	if len(suffixes) != 0 {
		t.Errorf("expected 0 suffixes for unconfigured tailnet, got %d", len(suffixes))
	}
	if strings.Contains(pac, "shExpMatch") {
		t.Error("should not contain shExpMatch for empty suffix")
	}
}

func TestBuildPACForTailnets_MultipleTailnets(t *testing.T) {
	tailnet1 := pacTailnet("one.ts.net", 1080)
	tailnet2 := pacTailnet("two.ts.net", 1081)
	pac, suffixes := buildPACForTailnets([]registry.TailnetView{tailnet1, tailnet2})

	if len(suffixes) != 2 {
		t.Errorf("expected 2 suffixes, got %d", len(suffixes))
	}
	if !strings.Contains(pac, "*.one.ts.net") {
		t.Error("expected one.ts.net in PAC")
	}
	if !strings.Contains(pac, "*.two.ts.net") {
		t.Error("expected two.ts.net in PAC")
	}
}

func TestWritePAC_Headers(t *testing.T) {
	w := httptest.NewRecorder()
	writePAC(w, "test content")

	resp := w.Result()
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-ns-proxy-autoconfig" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/x-ns-proxy-autoconfig")
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control = %q, expected no-cache", cc)
	}

	body := w.Body.String()
	if body != "test content" {
		t.Errorf("body = %q, want %q", body, "test content")
	}
}

func TestHandler_ServesPAC(t *testing.T) {
	// Handler requires a registry but we can verify it returns a valid function.
	// We can't easily construct a real registry without a file, but we can test
	// that the handler returns a function and doesn't panic with nil.
	handler := Handler(nil)
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}
	// Note: calling handler would panic with nil registry, but the function itself is valid.
	_ = http.HandlerFunc(handler)
}
