package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jcambass/tailhopper/internal/sse"
	"github.com/jcambass/tailhopper/internal/tailscale"
)

// mockBroadcaster records broadcast calls for assertions.
type mockBroadcaster struct {
	mu             sync.Mutex
	tailnetChanges []int
	globalChanges  int
}

func (b *mockBroadcaster) BroadcastTailnetChange(tailnetID int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tailnetChanges = append(b.tailnetChanges, tailnetID)
}

func (b *mockBroadcaster) BroadcastGlobalChange() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.globalChanges++
}

func (b *mockBroadcaster) tailnetChangeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.tailnetChanges)
}

func (b *mockBroadcaster) globalChangeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.globalChanges
}

// newTestRegistry creates a registry using a temp dir, with no pre-existing config file.
func newTestRegistry(t *testing.T, broadcaster *mockBroadcaster) *Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")

	var bc sse.Broadcaster
	if broadcaster != nil {
		bc = broadcaster
	}

	reg, err := NewRegistry(path, bc)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

// writeConfigFile writes a JSON config file for Load() tests.
func writeConfigFile(t *testing.T, path string, configs []TailnetConfig) {
	t.Helper()
	data, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// --- NewRegistry ---

func TestNewRegistry_NoExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	reg, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if len(reg.List()) != 0 {
		t.Errorf("expected empty registry, got %d tailnets", len(reg.List()))
	}
}

func TestNewRegistry_LoadsExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "host-1", SocksPort: 1080, UserEnabled: true, ClaimedMagicDNSSuffix: "corp.ts.net"},
		{ID: 3, StateDir: filepath.Join(dir, "3"), Hostname: "host-3", SocksPort: 1082},
	})

	reg, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	list := reg.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 tailnets, got %d", len(list))
	}
	if list[0].ID() != 1 {
		t.Errorf("first tailnet ID = %d, want 1", list[0].ID())
	}
	if list[1].ID() != 3 {
		t.Errorf("second tailnet ID = %d, want 3", list[1].ID())
	}
}

func TestNewRegistry_NextIDFromLoaded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 5, StateDir: filepath.Join(dir, "5"), Hostname: "host", SocksPort: 1080},
	})

	reg, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	// Add a new tailnet — its ID should be 6 (5 + 1).
	tn, err := reg.Add("new-host")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if tn.ID() != 6 {
		t.Errorf("new tailnet ID = %d, want 6", tn.ID())
	}
}

func TestNewRegistry_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := NewRegistry(path, nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- Add ---

func TestAdd_AssignsIncrementingIDs(t *testing.T) {
	reg := newTestRegistry(t, nil)

	t1, err := reg.Add("host-1")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	t2, err := reg.Add("host-2")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if t1.ID() != 1 {
		t.Errorf("first ID = %d, want 1", t1.ID())
	}
	if t2.ID() != 2 {
		t.Errorf("second ID = %d, want 2", t2.ID())
	}
}

func TestAdd_PersistsToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	reg, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	if _, err := reg.Add("my-host"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Read the file back and verify
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var configs []TailnetConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 config on disk, got %d", len(configs))
	}
	if configs[0].Hostname != "my-host" {
		t.Errorf("hostname = %q, want %q", configs[0].Hostname, "my-host")
	}
}

func TestAdd_GeneratesHostnameWhenEmpty(t *testing.T) {
	reg := newTestRegistry(t, nil)

	tn, err := reg.Add("")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	snap := tn.Snapshot()
	if snap.Hostname == "" {
		t.Error("expected non-empty hostname when adding with empty string")
	}
}

func TestAdd_BroadcastsGlobalChange(t *testing.T) {
	bc := &mockBroadcaster{}
	reg := newTestRegistry(t, bc)

	if _, err := reg.Add("host"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if bc.globalChangeCount() != 1 {
		t.Errorf("global changes = %d, want 1", bc.globalChangeCount())
	}
}

func TestAdd_NilBroadcaster(t *testing.T) {
	reg := newTestRegistry(t, nil)

	// Should not panic with nil broadcaster
	if _, err := reg.Add("host"); err != nil {
		t.Fatalf("Add: %v", err)
	}
}

// --- Get ---

func TestGet_Found(t *testing.T) {
	reg := newTestRegistry(t, nil)

	added, _ := reg.Add("host")
	got, ok := reg.Get(added.ID())
	if !ok {
		t.Fatal("expected Get to return true")
	}
	if got.ID() != added.ID() {
		t.Errorf("got ID %d, want %d", got.ID(), added.ID())
	}
}

func TestGet_NotFound(t *testing.T) {
	reg := newTestRegistry(t, nil)

	_, ok := reg.Get(999)
	if ok {
		t.Error("expected Get to return false for nonexistent ID")
	}
}

// --- Delete ---

func TestDelete_RemovesFromRegistry(t *testing.T) {
	reg := newTestRegistry(t, nil)

	tn, _ := reg.Add("host")
	id := tn.ID()

	if err := reg.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, ok := reg.Get(id)
	if ok {
		t.Error("expected tailnet to be removed after Delete")
	}
	if len(reg.List()) != 0 {
		t.Errorf("expected empty list, got %d", len(reg.List()))
	}
}

func TestDelete_NotFound(t *testing.T) {
	reg := newTestRegistry(t, nil)

	err := reg.Delete(999)
	if err == nil {
		t.Fatal("expected error when deleting nonexistent ID")
	}
}

func TestDelete_PersistsToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	reg, _ := NewRegistry(path, nil)
	tn, _ := reg.Add("host")
	reg.Delete(tn.ID())

	// Read file and verify it's empty
	data, _ := os.ReadFile(path)
	var configs []TailnetConfig
	json.Unmarshal(data, &configs)
	if len(configs) != 0 {
		t.Errorf("expected 0 configs on disk after delete, got %d", len(configs))
	}
}

func TestDelete_BroadcastsGlobalChange(t *testing.T) {
	bc := &mockBroadcaster{}
	reg := newTestRegistry(t, bc)

	tn, _ := reg.Add("host")
	bc.mu.Lock()
	bc.globalChanges = 0 // reset from Add
	bc.mu.Unlock()

	reg.Delete(tn.ID())

	if bc.globalChangeCount() != 1 {
		t.Errorf("global changes = %d, want 1", bc.globalChangeCount())
	}
}

func TestDelete_RemovesStateDirectory(t *testing.T) {
	reg := newTestRegistry(t, nil)

	tn, _ := reg.Add("host")
	id := tn.ID()

	// Get the state dir from config before deletion.
	reg.mu.RLock()
	stateDir := reg.configs[id].StateDir
	reg.mu.RUnlock()

	// Create the state directory so we can verify it's removed
	os.MkdirAll(stateDir, 0700)
	os.WriteFile(filepath.Join(stateDir, "test"), []byte("data"), 0600)

	if err := reg.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Error("expected state directory to be removed")
	}
}

// --- List ---

func TestList_Empty(t *testing.T) {
	reg := newTestRegistry(t, nil)
	if len(reg.List()) != 0 {
		t.Errorf("expected empty list, got %d", len(reg.List()))
	}
}

func TestList_SortedByID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Write configs in reverse order
	writeConfigFile(t, path, []TailnetConfig{
		{ID: 3, StateDir: filepath.Join(dir, "3"), Hostname: "c", SocksPort: 1082},
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "a", SocksPort: 1080},
		{ID: 2, StateDir: filepath.Join(dir, "2"), Hostname: "b", SocksPort: 1081},
	})

	reg, _ := NewRegistry(path, nil)
	list := reg.List()

	if len(list) != 3 {
		t.Fatalf("expected 3 tailnets, got %d", len(list))
	}
	for i, want := range []int{1, 2, 3} {
		if list[i].ID() != want {
			t.Errorf("list[%d].ID() = %d, want %d", i, list[i].ID(), want)
		}
	}
}

// --- HasUnconfiguredTailnets ---

func TestHasUnconfiguredTailnets_AllConfigured(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "h", SocksPort: 1080, ClaimedMagicDNSSuffix: "corp.ts.net"},
	})

	reg, _ := NewRegistry(path, nil)
	if reg.HasUnconfiguredTailnets() {
		t.Error("expected false when all tailnets have a suffix")
	}
}

func TestHasUnconfiguredTailnets_OneUnconfigured(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "h1", SocksPort: 1080, ClaimedMagicDNSSuffix: "corp.ts.net"},
		{ID: 2, StateDir: filepath.Join(dir, "2"), Hostname: "h2", SocksPort: 1081},
	})

	reg, _ := NewRegistry(path, nil)
	if !reg.HasUnconfiguredTailnets() {
		t.Error("expected true when a tailnet has no suffix")
	}
}

func TestHasUnconfiguredTailnets_Empty(t *testing.T) {
	reg := newTestRegistry(t, nil)
	if reg.HasUnconfiguredTailnets() {
		t.Error("expected false for empty registry")
	}
}

// --- OnChange ---

func TestOnChange_PersistsUserState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "h", SocksPort: 1080},
	})

	reg, _ := NewRegistry(path, nil)

	reg.OnChange(tailscale.TailnetSnapshot{
		ID:        1,
		UserState: tailscale.UserEnabled,
		State:     tailscale.ConnectedState,
	})

	// Verify config updated in memory
	reg.mu.RLock()
	cfg := reg.configs[1]
	reg.mu.RUnlock()

	if !cfg.UserEnabled {
		t.Error("expected UserEnabled to be true after OnChange with UserEnabled")
	}
}

func TestOnChange_PersistsTerminalError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "h", SocksPort: 1080},
	})

	reg, _ := NewRegistry(path, nil)

	reg.OnChange(tailscale.TailnetSnapshot{
		ID:            1,
		TerminalError: "fatal error",
	})

	reg.mu.RLock()
	cfg := reg.configs[1]
	reg.mu.RUnlock()

	if cfg.TerminalError != "fatal error" {
		t.Errorf("TerminalError = %q, want %q", cfg.TerminalError, "fatal error")
	}
}

func TestOnChange_ClaimsMagicDNSSuffix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "h", SocksPort: 1080},
	})

	bc := &mockBroadcaster{}
	reg, _ := NewRegistry(path, bc)

	reg.OnChange(tailscale.TailnetSnapshot{
		ID:             1,
		MagicDNSSuffix: "corp.ts.net",
		UserState:      tailscale.UserEnabled,
	})

	reg.mu.RLock()
	cfg := reg.configs[1]
	reg.mu.RUnlock()

	if cfg.ClaimedMagicDNSSuffix != "corp.ts.net" {
		t.Errorf("ClaimedMagicDNSSuffix = %q, want %q", cfg.ClaimedMagicDNSSuffix, "corp.ts.net")
	}

	// New suffix should trigger global broadcast
	if bc.globalChangeCount() != 1 {
		t.Errorf("global changes = %d, want 1", bc.globalChangeCount())
	}
}

func TestOnChange_SuffixNotClaimedWhenTerminalError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "h", SocksPort: 1080},
	})

	reg, _ := NewRegistry(path, nil)

	reg.OnChange(tailscale.TailnetSnapshot{
		ID:             1,
		MagicDNSSuffix: "corp.ts.net",
		TerminalError:  "some error",
	})

	reg.mu.RLock()
	cfg := reg.configs[1]
	reg.mu.RUnlock()

	if cfg.ClaimedMagicDNSSuffix != "" {
		t.Errorf("ClaimedMagicDNSSuffix = %q, want empty (should not claim with terminal error)", cfg.ClaimedMagicDNSSuffix)
	}
}

func TestOnChange_SuffixAlreadyClaimed_NoChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "h", SocksPort: 1080, ClaimedMagicDNSSuffix: "corp.ts.net"},
	})

	bc := &mockBroadcaster{}
	reg, _ := NewRegistry(path, bc)

	reg.OnChange(tailscale.TailnetSnapshot{
		ID:             1,
		MagicDNSSuffix: "corp.ts.net",
		UserState:      tailscale.UserEnabled,
	})

	// Same suffix — should NOT trigger global broadcast
	if bc.globalChangeCount() != 0 {
		t.Errorf("global changes = %d, want 0 (suffix unchanged)", bc.globalChangeCount())
	}
	// Should still broadcast tailnet change
	if bc.tailnetChangeCount() != 1 {
		t.Errorf("tailnet changes = %d, want 1", bc.tailnetChangeCount())
	}
}

func TestOnChange_SuffixConflict_SetsTerminalError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "h1", SocksPort: 1080, ClaimedMagicDNSSuffix: "corp.ts.net"},
		{ID: 2, StateDir: filepath.Join(dir, "2"), Hostname: "h2", SocksPort: 1081},
	})

	reg, _ := NewRegistry(path, nil)

	// Tailnet 2 tries to claim the same suffix as tailnet 1
	reg.OnChange(tailscale.TailnetSnapshot{
		ID:             2,
		MagicDNSSuffix: "corp.ts.net",
		UserState:      tailscale.UserEnabled,
	})

	// Tailnet 2 should have a terminal error
	tn2, _ := reg.Get(2)
	snap := tn2.Snapshot()
	if snap.State != tailscale.HasTerminalErrorState {
		t.Errorf("tailnet 2 state = %q, want %q", snap.State, tailscale.HasTerminalErrorState)
	}
	if snap.TerminalError == "" {
		t.Error("expected terminal error to be set on suffix conflict")
	}
}

func TestOnChange_SuffixConflict_DoesNotClaimSuffix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "h1", SocksPort: 1080, ClaimedMagicDNSSuffix: "corp.ts.net"},
		{ID: 2, StateDir: filepath.Join(dir, "2"), Hostname: "h2", SocksPort: 1081},
	})

	reg, _ := NewRegistry(path, nil)

	reg.OnChange(tailscale.TailnetSnapshot{
		ID:             2,
		MagicDNSSuffix: "corp.ts.net",
		UserState:      tailscale.UserEnabled,
	})

	// Tailnet 2's config should NOT have the suffix
	reg.mu.RLock()
	cfg2 := reg.configs[2]
	reg.mu.RUnlock()

	if cfg2.ClaimedMagicDNSSuffix == "corp.ts.net" {
		t.Error("conflicting suffix should not be claimed")
	}
}

func TestOnChange_UnknownID_Ignored(t *testing.T) {
	reg := newTestRegistry(t, nil)

	// Should not panic when snapshot has an unknown ID
	reg.OnChange(tailscale.TailnetSnapshot{
		ID:        999,
		UserState: tailscale.UserEnabled,
	})
}

func TestOnChange_BroadcastsTailnetChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "h", SocksPort: 1080},
	})

	bc := &mockBroadcaster{}
	reg, _ := NewRegistry(path, bc)

	reg.OnChange(tailscale.TailnetSnapshot{
		ID:        1,
		UserState: tailscale.UserEnabled,
	})

	if bc.tailnetChangeCount() != 1 {
		t.Errorf("tailnet changes = %d, want 1", bc.tailnetChangeCount())
	}
}

func TestOnChange_NilBroadcaster_NoPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "h", SocksPort: 1080},
	})

	reg, _ := NewRegistry(path, nil)

	// Should not panic
	reg.OnChange(tailscale.TailnetSnapshot{
		ID:             1,
		MagicDNSSuffix: "new.ts.net",
		UserState:      tailscale.UserEnabled,
	})
}

// --- Load / persistence round-trip ---

func TestLoad_PreservesTerminalError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "h", SocksPort: 1080, TerminalError: "bad things"},
	})

	reg, _ := NewRegistry(path, nil)
	tn, ok := reg.Get(1)
	if !ok {
		t.Fatal("expected tailnet 1")
	}
	snap := tn.Snapshot()
	if snap.TerminalError != "bad things" {
		t.Errorf("TerminalError = %q, want %q", snap.TerminalError, "bad things")
	}
	if snap.State != tailscale.HasTerminalErrorState {
		t.Errorf("state = %q, want %q", snap.State, tailscale.HasTerminalErrorState)
	}
}

func TestLoad_PreservesUserEnabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	writeConfigFile(t, path, []TailnetConfig{
		{ID: 1, StateDir: filepath.Join(dir, "1"), Hostname: "h", SocksPort: 1080, UserEnabled: true},
	})

	reg, _ := NewRegistry(path, nil)
	tn, _ := reg.Get(1)
	snap := tn.Snapshot()
	if snap.UserState != tailscale.UserEnabled {
		t.Errorf("UserState = %q, want %q", snap.UserState, tailscale.UserEnabled)
	}
}

func TestAddDeleteAdd_IDsIncrement(t *testing.T) {
	reg := newTestRegistry(t, nil)

	t1, _ := reg.Add("host-1")
	reg.Delete(t1.ID())

	t2, _ := reg.Add("host-2")
	if t2.ID() != 2 {
		t.Errorf("ID after delete+add = %d, want 2 (IDs should never be reused)", t2.ID())
	}
}
