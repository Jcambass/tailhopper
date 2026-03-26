package ts

import (
	"testing"

	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
)

func TestIPNState_Refresh_State(t *testing.T) {
	state := ipn.Running
	n := &ipn.Notify{State: &state}

	s := IPNState{}
	s = s.refresh(n)

	if s.State == nil || *s.State != ipn.Running {
		t.Fatalf("expected Running, got %v", s.State)
	}
}

func TestIPNState_Refresh_ErrMessage(t *testing.T) {
	msg := "something went wrong"
	n := &ipn.Notify{ErrMessage: &msg}

	s := IPNState{}
	s = s.refresh(n)

	if s.ErrMessage == nil || *s.ErrMessage != msg {
		t.Fatalf("expected %q, got %v", msg, s.ErrMessage)
	}
}

func TestIPNState_Refresh_BrowseToURL(t *testing.T) {
	url := "https://login.tailscale.com/..."
	n := &ipn.Notify{BrowseToURL: &url}

	s := IPNState{}
	s = s.refresh(n)

	if s.BrowseToURL == nil || *s.BrowseToURL != url {
		t.Fatalf("expected %q, got %v", url, s.BrowseToURL)
	}
}

func TestIPNState_Refresh_PreservesOldState(t *testing.T) {
	// First notification sets state
	running := ipn.Running
	s := IPNState{}
	s = s.refresh(&ipn.Notify{State: &running})

	// Second notification sets err message but should preserve state
	msg := "error"
	s = s.refresh(&ipn.Notify{ErrMessage: &msg})

	if s.State == nil || *s.State != ipn.Running {
		t.Fatalf("state should be preserved, got %v", s.State)
	}
	if s.ErrMessage == nil || *s.ErrMessage != msg {
		t.Fatalf("err message should be set, got %v", s.ErrMessage)
	}
}

func TestIPNState_Refresh_OverwritesState(t *testing.T) {
	running := ipn.Running
	s := IPNState{}
	s = s.refresh(&ipn.Notify{State: &running})

	needsLogin := ipn.NeedsLogin
	s = s.refresh(&ipn.Notify{State: &needsLogin})

	if *s.State != ipn.NeedsLogin {
		t.Fatalf("expected NeedsLogin, got %v", *s.State)
	}
}

func TestIPNState_String_Empty(t *testing.T) {
	s := IPNState{}
	got := s.String()
	if got != "IPNState{}" {
		t.Fatalf("expected empty string representation, got %q", got)
	}
}

func TestIPNState_String_WithState(t *testing.T) {
	running := ipn.Running
	s := IPNState{State: &running}
	got := s.String()
	if got == "IPNState{}" {
		t.Fatal("expected non-empty string representation")
	}
}

func TestIPNState_String_WithPeers(t *testing.T) {
	s := IPNState{
		Peers: []tailcfg.NodeView{{}, {}},
	}
	got := s.String()
	if got == "IPNState{}" {
		t.Fatal("expected non-empty string representation with peers")
	}
}

func TestExtractMagicDNSSuffix(t *testing.T) {
	tests := []struct {
		name string
		fqdn string
		want string
	}{
		{"with trailing dot", "host.tail-scale.ts.net.", "tail-scale.ts.net"},
		{"without trailing dot", "host.tail-scale.ts.net", "tail-scale.ts.net"},
		{"single label with dot", "host.", ""},
		{"single label", "host", ""},
		{"empty", "", ""},
		{"two labels", "host.example.com", "example.com"},
		{"many labels", "host.a.b.c.d", "a.b.c.d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractMagicDNSSuffix(tt.fqdn)
			if got != tt.want {
				t.Errorf("extractMagicDNSSuffix(%q) = %q, want %q", tt.fqdn, got, tt.want)
			}
		})
	}
}

func TestParseLogKeyValues(t *testing.T) {
	tests := []struct {
		name      string
		msg       string
		wantAttrs int
		wantClean string
	}{
		{
			name:      "plain message",
			msg:       "hello world",
			wantAttrs: 0,
			wantClean: "hello world",
		},
		{
			name:      "key=value pairs",
			msg:       "starting server host=localhost port=8080",
			wantAttrs: 2,
			wantClean: "starting server",
		},
		{
			name:      "only key=value",
			msg:       "host=localhost",
			wantAttrs: 1,
			wantClean: "",
		},
		{
			name:      "url-like value parsed as key=value",
			msg:       "url=http://example.com",
			wantAttrs: 1,
			wantClean: "",
		},
		{
			name:      "invalid key with special chars",
			msg:       "foo/bar=baz",
			wantAttrs: 0,
			wantClean: "foo/bar=baz",
		},
		{
			name:      "empty value",
			msg:       "key=",
			wantAttrs: 1,
			wantClean: "",
		},
		{
			name:      "empty string",
			msg:       "",
			wantAttrs: 0,
			wantClean: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs, clean := parseLogKeyValues(tt.msg)
			if len(attrs) != tt.wantAttrs {
				t.Errorf("got %d attrs, want %d", len(attrs), tt.wantAttrs)
			}
			if clean != tt.wantClean {
				t.Errorf("got clean %q, want %q", clean, tt.wantClean)
			}
		})
	}
}
