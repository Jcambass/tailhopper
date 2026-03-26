package tailscale

import (
	"fmt"
	"testing"
)

func TestAlreadyClaimedError(t *testing.T) {
	err := &AlreadyClaimedError{Suffix: "example.ts.net"}
	want := "magic DNS suffix 'example.ts.net' is already claimed by another tailnet"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}
}

type mockObserver struct {
	claims           map[string]int
	failError        error
	broadcastCalls   []int
	userStateCalls   []mockUserStateCall
	terminalErrCalls []mockTerminalErrCall
}

type mockUserStateCall struct {
	id    int
	state UserState
}

type mockTerminalErrCall struct {
	id  int
	err string
}

func newMockObserver() *mockObserver {
	return &mockObserver{claims: make(map[string]int)}
}

func (m *mockObserver) Claim(tailnetID int, suffix string) error {
	if m.failError != nil {
		return m.failError
	}
	if existingID, ok := m.claims[suffix]; ok && existingID != tailnetID {
		return &AlreadyClaimedError{Suffix: suffix}
	}
	m.claims[suffix] = tailnetID
	return nil
}

func (m *mockObserver) OnBroadcast(tailnetID int) {
	m.broadcastCalls = append(m.broadcastCalls, tailnetID)
}

func (m *mockObserver) OnUserStateChange(tailnetID int, state UserState) {
	m.userStateCalls = append(m.userStateCalls, mockUserStateCall{tailnetID, state})
}

func (m *mockObserver) OnTerminalErrorChange(tailnetID int, err string) {
	m.terminalErrCalls = append(m.terminalErrCalls, mockTerminalErrCall{tailnetID, err})
}

func TestMockObserver_Claim(t *testing.T) {
	reg := newMockObserver()

	// First claim should succeed
	if err := reg.Claim(1, "example.ts.net"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Same tailnet, same suffix should succeed
	if err := reg.Claim(1, "example.ts.net"); err != nil {
		t.Fatalf("unexpected error for same tailnet: %v", err)
	}

	// Different tailnet, same suffix should fail
	err := reg.Claim(2, "example.ts.net")
	if err == nil {
		t.Fatal("expected error for duplicate claim")
	}
	if _, ok := err.(*AlreadyClaimedError); !ok {
		t.Fatalf("expected AlreadyClaimedError, got %T", err)
	}
}

func TestMockObserver_ClaimError(t *testing.T) {
	reg := newMockObserver()
	reg.failError = fmt.Errorf("disk full")

	err := reg.Claim(1, "example.ts.net")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "disk full" {
		t.Fatalf("expected 'disk full', got %q", err.Error())
	}
}
