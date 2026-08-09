package registry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jcambass/tailhopper/internal/tailscale"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

func newRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := NewRegistry(filepath.Join(t.TempDir(), "tailhopper.json"))
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func writeConfigs(t *testing.T, path string, configs []TailnetConfig) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(configs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

type fakeRuntime struct {
	mu         sync.Mutex
	status     *ipnstate.Status
	startBlock <-chan struct{}
	started    bool
	closed     *bool
}

func (r *fakeRuntime) Start(context.Context) error {
	if r.startBlock != nil {
		<-r.startBlock
	}
	r.mu.Lock()
	r.started = true
	r.mu.Unlock()
	return nil
}

func (r *fakeRuntime) Stop(context.Context) error {
	r.mu.Lock()
	r.started = false
	if r.closed != nil {
		*r.closed = true
	}
	r.mu.Unlock()
	return nil
}

func (r *fakeRuntime) Info(context.Context) tailscale.Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	return tailscale.Info{Started: r.started, Status: r.status}
}

func mockRuntime(status *ipnstate.Status, startBlock <-chan struct{}, closed *bool) runtime {
	return &fakeRuntime{status: status, startBlock: startBlock, closed: closed}
}

func TestRegistryLoadsAndListsConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	writeConfigs(t, path, []TailnetConfig{
		{ID: 3, StateDir: filepath.Join(dir, "3"), Hostname: "three", SocksPort: 1083},
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "one", SocksPort: 1081, UserEnabled: true},
	})

	registry, err := NewRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	views := registry.List(context.Background())
	if len(views) != 2 || views[0].ID != 1 || views[1].ID != 3 {
		t.Fatalf("unexpected views: %#v", views)
	}
	if !views[0].UserEnabled || views[0].ConfiguredHostname != "one" {
		t.Fatalf("unexpected first view: %#v", views[0])
	}
}

func TestAddIsAtomicAndPersists(t *testing.T) {
	registry := newRegistry(t)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := registry.Add("host")
			results <- err
		}()
	}

	var added, rejected int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			added++
		case errors.Is(err, ErrUnconfiguredTailnet):
			rejected++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if added != 1 || rejected != 1 {
		t.Fatalf("added=%d rejected=%d", added, rejected)
	}

	data, err := os.ReadFile(registry.path)
	if err != nil {
		t.Fatal(err)
	}
	var configs []TailnetConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || configs[0].Hostname != "host" {
		t.Fatalf("unexpected config: %#v", configs)
	}
}

func TestListUsesAuthoritativeStatusAndClaimsSuffix(t *testing.T) {
	registry := newRegistry(t)
	id, err := registry.Add("host")
	if err != nil {
		t.Fatal(err)
	}
	status := &ipnstate.Status{
		BackendState: ipn.Running.String(),
		AuthURL:      "https://login.example",
		Self:         &ipnstate.PeerStatus{HostName: "actual-host"},
		CurrentTailnet: &ipnstate.TailnetStatus{
			MagicDNSSuffix: "example.ts.net",
		},
	}
	runtime := mockRuntime(status, nil, nil)
	registry.mu.Lock()
	registry.entries[id].runtime = runtime
	registry.mu.Unlock()
	if err := registry.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	view := registry.List(context.Background())[0]
	if view.BackendState != ipn.Running.String() || view.NodeHostname != "actual-host" {
		t.Fatalf("status was not projected: %#v", view)
	}
	if view.MagicDNSSuffix != "example.ts.net" {
		t.Fatalf("suffix = %q", view.MagicDNSSuffix)
	}
	registry.mu.RLock()
	claimed := registry.entries[id].config.ClaimedMagicDNSSuffix
	registry.mu.RUnlock()
	if claimed != "example.ts.net" {
		t.Fatalf("persisted suffix = %q", claimed)
	}
}

func TestListReportsSuffixConflictWithoutRuntimeState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	writeConfigs(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "one", SocksPort: 1081, ClaimedMagicDNSSuffix: "example.ts.net"},
		{ID: 2, StateDir: filepath.Join(dir, "2"), Hostname: "two", SocksPort: 1082},
	})
	registry, err := NewRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	status := &ipnstate.Status{CurrentTailnet: &ipnstate.TailnetStatus{MagicDNSSuffix: "example.ts.net"}}
	registry.mu.Lock()
	registry.entries[2].runtime = mockRuntime(status, nil, nil)
	registry.mu.Unlock()
	if err := registry.Start(context.Background(), 2); err != nil {
		t.Fatal(err)
	}

	views := registry.List(context.Background())
	if views[1].Error == "" {
		t.Fatalf("expected conflict: %#v", views[1])
	}
}

func TestCommandsOnDifferentTailnetsDoNotBlockEachOther(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	writeConfigs(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "one", SocksPort: 1081, ClaimedMagicDNSSuffix: "one.ts.net"},
		{ID: 2, StateDir: filepath.Join(dir, "2"), Hostname: "two", SocksPort: 1082, ClaimedMagicDNSSuffix: "two.ts.net"},
	})
	registry, err := NewRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	registry.mu.Lock()
	registry.entries[1].runtime = mockRuntime(&ipnstate.Status{}, release, nil)
	registry.entries[2].runtime = mockRuntime(&ipnstate.Status{}, nil, nil)
	registry.mu.Unlock()

	startDone := make(chan error, 1)
	go func() { startDone <- registry.Start(context.Background(), 1) }()
	if err := registry.Stop(context.Background(), 2); err != nil {
		t.Fatalf("independent Stop blocked or failed: %v", err)
	}
	close(release)
	if err := <-startDone; err != nil {
		t.Fatal(err)
	}
}

func TestDeleteStopsRuntimeAndRemovesState(t *testing.T) {
	registry := newRegistry(t)
	id, err := registry.Add("host")
	if err != nil {
		t.Fatal(err)
	}
	registry.mu.RLock()
	stateDir := registry.entries[id].config.StateDir
	registry.mu.RUnlock()
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	closed := false
	runtime := mockRuntime(&ipnstate.Status{}, nil, &closed)
	registry.mu.Lock()
	registry.entries[id].runtime = runtime
	registry.mu.Unlock()
	if err := registry.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := registry.Delete(id); err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Error("runtime was not closed")
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Error("state directory still exists")
	}
	if len(registry.List(context.Background())) != 0 {
		t.Error("registry still contains deleted tailnet")
	}
}
