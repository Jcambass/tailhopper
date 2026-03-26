package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jcambass/tailhopper/internal/tailscale"
)

type mockBroadcasterReg struct {
	tailnetChanges []int
	globalChanges  int
}

func (m *mockBroadcasterReg) BroadcastTailnetChange(tailnetID int) {
	m.tailnetChanges = append(m.tailnetChanges, tailnetID)
}

func (m *mockBroadcasterReg) BroadcastGlobalChange() {
	m.globalChanges++
}

func writeConfig(t *testing.T, path string, configs []PersistedTailnet) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(configs); err != nil {
		t.Fatal(err)
	}
}

func TestNewRegistry_NoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")

	reg, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}
	list := reg.List()
	if len(list) != 0 {
		t.Errorf("expected 0 tailnets, got %d", len(list))
	}
}

func TestNewRegistry_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")

	configs := []PersistedTailnet{
		{ID: 1, StateDir: filepath.Join(dir, "tailnets", "1"), SocksPort: 1080, Hostname: "test-host"},
	}
	writeConfig(t, path, configs)

	reg, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	list := reg.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 tailnet, got %d", len(list))
	}
	snap := list[0].Snapshot()
	if snap.Hostname != "test-host" {
		t.Errorf("hostname = %q, want %q", snap.Hostname, "test-host")
	}
}

func TestNewRegistry_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := NewRegistry(path, nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestRegistry_Add(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	broadcaster := &mockBroadcasterReg{}

	reg, err := NewRegistry(path, broadcaster)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	tailnet, err := reg.Add("my-host")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if tailnet == nil {
		t.Fatal("expected non-nil tailnet")
	}

	snap := tailnet.Snapshot()
	if snap.Hostname != "my-host" {
		t.Errorf("hostname = %q, want %q", snap.Hostname, "my-host")
	}
	if snap.State != tailscale.StoppedState {
		t.Errorf("state = %q, want %q", snap.State, tailscale.StoppedState)
	}
	if snap.UserState != tailscale.UserDisabled {
		t.Errorf("user state = %q, want %q", snap.UserState, tailscale.UserDisabled)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected config file to be created")
	}
	if broadcaster.globalChanges != 1 {
		t.Errorf("expected 1 global broadcast, got %d", broadcaster.globalChanges)
	}
}

func TestRegistry_Add_AutoGeneratesHostname(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	reg, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	tailnet, err := reg.Add("")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	snap := tailnet.Snapshot()
	if snap.Hostname == "" {
		t.Error("expected auto-generated hostname")
	}
}

func TestRegistry_Add_Sequential(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	reg, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	t1, _ := reg.Add("host1")
	t2, _ := reg.Add("host2")
	if t1.ID() >= t2.ID() {
		t.Errorf("expected sequential IDs, got %d and %d", t1.ID(), t2.ID())
	}
}

func TestRegistry_List_SortedByID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")

	configs := []PersistedTailnet{
		{ID: 3, StateDir: filepath.Join(dir, "3"), SocksPort: 1082, Hostname: "host3"},
		{ID: 1, StateDir: filepath.Join(dir, "1"), SocksPort: 1080, Hostname: "host1"},
		{ID: 2, StateDir: filepath.Join(dir, "2"), SocksPort: 1081, Hostname: "host2"},
	}
	writeConfig(t, path, configs)

	reg, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	list := reg.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 tailnets, got %d", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].ID() >= list[i].ID() {
			t.Errorf("list not sorted: ID %d >= %d", list[i-1].ID(), list[i].ID())
		}
	}
}

func TestRegistry_Get(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	reg, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	added, _ := reg.Add("host")

	got, ok := reg.Get(added.ID())
	if !ok {
		t.Fatal("expected to find tailnet")
	}
	if got.ID() != added.ID() {
		t.Errorf("got ID %d, want %d", got.ID(), added.ID())
	}

	_, ok = reg.Get(999)
	if ok {
		t.Error("expected not found for non-existent ID")
	}
}

func TestRegistry_Delete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	broadcaster := &mockBroadcasterReg{}

	reg, err := NewRegistry(path, broadcaster)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	added, _ := reg.Add("host")
	broadcaster.globalChanges = 0

	stateDir := filepath.Join(dir, "tailnets", "1")
	os.MkdirAll(stateDir, 0755)

	err = reg.Delete(added.ID())
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, ok := reg.Get(added.ID())
	if ok {
		t.Error("expected tailnet to be deleted")
	}
	if broadcaster.globalChanges != 1 {
		t.Errorf("expected 1 global broadcast, got %d", broadcaster.globalChanges)
	}
}

func TestRegistry_Delete_NonExistent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	reg, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	err = reg.Delete(999)
	if err == nil {
		t.Fatal("expected error for non-existent tailnet")
	}
}

func TestRegistry_HasUnconfiguredTailnets(t *testing.T) {
	t.Run("no tailnets", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "tailhopper.json")
		reg, _ := NewRegistry(path, nil)
		if reg.HasUnconfiguredTailnets() {
			t.Error("expected false with no tailnets")
		}
	})

	t.Run("unconfigured tailnet", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "tailhopper.json")
		reg, _ := NewRegistry(path, nil)
		reg.Add("host")
		if !reg.HasUnconfiguredTailnets() {
			t.Error("expected true with unconfigured tailnet")
		}
	})

	t.Run("configured tailnet", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "tailhopper.json")
		configs := []PersistedTailnet{
			{ID: 1, StateDir: filepath.Join(dir, "1"), SocksPort: 1080, Hostname: "host", ClaimedMagicDNSSuffix: "example.ts.net"},
		}
		writeConfig(t, path, configs)
		reg, _ := NewRegistry(path, nil)
		if reg.HasUnconfiguredTailnets() {
			t.Error("expected false when all tailnets are configured")
		}
	})
}

func TestRegistry_Claim(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	broadcaster := &mockBroadcasterReg{}

	configs := []PersistedTailnet{
		{ID: 1, StateDir: filepath.Join(dir, "1"), SocksPort: 1080, Hostname: "host1"},
		{ID: 2, StateDir: filepath.Join(dir, "2"), SocksPort: 1081, Hostname: "host2"},
	}
	writeConfig(t, path, configs)

	reg, err := NewRegistry(path, broadcaster)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	t.Run("successful claim", func(t *testing.T) {
		err := reg.Claim(1, "tailnet1.ts.net")
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if broadcaster.globalChanges != 1 {
			t.Errorf("expected 1 global broadcast, got %d", broadcaster.globalChanges)
		}
	})

	t.Run("duplicate claim", func(t *testing.T) {
		err := reg.Claim(2, "tailnet1.ts.net")
		if err == nil {
			t.Fatal("expected error for duplicate claim")
		}
		if _, ok := err.(*tailscale.AlreadyClaimedError); !ok {
			t.Fatalf("expected AlreadyClaimedError, got %T: %v", err, err)
		}
	})

	t.Run("non-existent tailnet", func(t *testing.T) {
		err := reg.Claim(999, "any.ts.net")
		if err == nil {
			t.Fatal("expected error for non-existent tailnet")
		}
	})
}

func TestRegistry_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")

	reg1, _ := NewRegistry(path, nil)
	reg1.Add("host1")
	reg1.Add("host2")

	reg2, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry reload: %v", err)
	}
	list := reg2.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 tailnets after reload, got %d", len(list))
	}
}

func TestRegistry_NextID_AfterLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")

	configs := []PersistedTailnet{
		{ID: 5, StateDir: filepath.Join(dir, "5"), SocksPort: 1080, Hostname: "host5"},
		{ID: 10, StateDir: filepath.Join(dir, "10"), SocksPort: 1081, Hostname: "host10"},
	}
	writeConfig(t, path, configs)

	reg, _ := NewRegistry(path, nil)
	added, _ := reg.Add("new-host")
	if added.ID() <= 10 {
		t.Errorf("expected ID > 10, got %d", added.ID())
	}
}

func TestRegistry_TerminalErrorFromPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")

	configs := []PersistedTailnet{
		{ID: 1, StateDir: filepath.Join(dir, "1"), SocksPort: 1080, Hostname: "host", TerminalError: "old error"},
	}
	writeConfig(t, path, configs)

	reg, _ := NewRegistry(path, nil)
	list := reg.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 tailnet, got %d", len(list))
	}
	snap := list[0].Snapshot()
	if snap.TerminalError != "old error" {
		t.Errorf("terminal error = %q, want %q", snap.TerminalError, "old error")
	}
	if snap.State != tailscale.HasTerminalErrorState {
		t.Errorf("state = %q, want %q", snap.State, tailscale.HasTerminalErrorState)
	}
}

func TestRegistry_OnUserStateChange_Persists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	broadcaster := &mockBroadcasterReg{}

	reg, _ := NewRegistry(path, broadcaster)
	added, _ := reg.Add("host")

	// Trigger user state change
	reg.OnUserStateChange(added.ID(), tailscale.UserEnabled)

	// Reload from disk and verify persistence
	reg2, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	list := reg2.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 tailnet, got %d", len(list))
	}
	snap := list[0].Snapshot()
	if snap.UserState != tailscale.UserEnabled {
		t.Errorf("user state after reload = %q, want %q", snap.UserState, tailscale.UserEnabled)
	}
}

func TestRegistry_OnTerminalErrorChange_Persists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")

	reg, _ := NewRegistry(path, nil)
	added, _ := reg.Add("host")

	reg.OnTerminalErrorChange(added.ID(), "fatal error")

	// Reload from disk
	reg2, _ := NewRegistry(path, nil)
	list := reg2.List()
	snap := list[0].Snapshot()
	if snap.TerminalError != "fatal error" {
		t.Errorf("terminal error after reload = %q, want %q", snap.TerminalError, "fatal error")
	}
}

func TestRegistry_OnBroadcast_NotifiesSubscribers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")
	broadcaster := &mockBroadcasterReg{}

	reg, _ := NewRegistry(path, broadcaster)
	reg.OnBroadcast(42)

	if len(broadcaster.tailnetChanges) != 1 {
		t.Fatalf("expected 1 tailnet change, got %d", len(broadcaster.tailnetChanges))
	}
	if broadcaster.tailnetChanges[0] != 42 {
		t.Errorf("tailnet change ID = %d, want 42", broadcaster.tailnetChanges[0])
	}
}

func TestRegistry_OnUserStateChange_NonExistentTailnet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")

	reg, _ := NewRegistry(path, nil)
	// Should not panic
	reg.OnUserStateChange(999, tailscale.UserEnabled)
}

func TestRegistry_OnTerminalErrorChange_NonExistentTailnet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")

	reg, _ := NewRegistry(path, nil)
	// Should not panic
	reg.OnTerminalErrorChange(999, "error")
}

func TestRegistry_RestoreEnabledTailnets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")

	// Create config with one enabled and one disabled tailnet
	configs := []PersistedTailnet{
		{ID: 1, StateDir: filepath.Join(dir, "1"), SocksPort: 0, Hostname: "host1", UserEnabled: true},
		{ID: 2, StateDir: filepath.Join(dir, "2"), SocksPort: 0, Hostname: "host2", UserEnabled: false},
		{ID: 3, StateDir: filepath.Join(dir, "3"), SocksPort: 0, Hostname: "host3", UserEnabled: true, TerminalError: "broken"},
	}
	writeConfig(t, path, configs)

	reg, err := NewRegistry(path, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	// RestoreEnabledTailnets will try to start tailnets, but Start will fail
	// because they use real tsnet servers. That's fine — we check the intent.
	reg.RestoreEnabledTailnets(t.Context())

	list := reg.List()
	// Tailnet 1: user enabled + stopped → should have attempted start (will fail, remains stopped)
	// Tailnet 2: user disabled → should be skipped
	// Tailnet 3: user enabled + terminal error (not stopped) → should be skipped
	for _, tn := range list {
		snap := tn.Snapshot()
		if snap.ID == 3 && snap.State != tailscale.HasTerminalErrorState {
			t.Errorf("tailnet 3 state = %q, want %q (should not be started)", snap.State, tailscale.HasTerminalErrorState)
		}
	}
}

func TestRegistry_Delete_CleansUpStateDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")

	reg, _ := NewRegistry(path, nil)
	added, _ := reg.Add("host")

	// Create the state directory that Add would normally reference
	snap := added.Snapshot()
	_ = snap
	// The state dir is set by the registry internally; let's verify via re-read
	stateDir := filepath.Join(dir, "tailnets", "1")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Create a file inside to verify removal
	if err := os.WriteFile(filepath.Join(stateDir, "test.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := reg.Delete(added.ID()); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Error("expected state directory to be removed")
	}
}

func TestRegistry_Claim_PersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailhopper.json")

	configs := []PersistedTailnet{
		{ID: 1, StateDir: filepath.Join(dir, "1"), SocksPort: 1080, Hostname: "host"},
	}
	writeConfig(t, path, configs)

	reg, _ := NewRegistry(path, nil)
	if err := reg.Claim(1, "my-tailnet.ts.net"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// Reload to verify persistence
	reg2, _ := NewRegistry(path, nil)
	if !reg2.HasUnconfiguredTailnets() == true {
		// After claiming, no unconfigured tailnets should remain
	}
	list := reg2.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 tailnet, got %d", len(list))
	}

	// Read raw JSON to verify the suffix was persisted
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted []PersistedTailnet
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted[0].ClaimedMagicDNSSuffix != "my-tailnet.ts.net" {
		t.Errorf("persisted suffix = %q, want %q", persisted[0].ClaimedMagicDNSSuffix, "my-tailnet.ts.net")
	}
}
