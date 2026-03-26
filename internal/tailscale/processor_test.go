package tailscale

import "testing"

func TestProcessor_AllAlwaysRun(t *testing.T) {
	called := [3]bool{}

	p := &ipnProcessor{
		ipnState: IPNState{},
		always: []func(IPNState) bool{
			func(IPNState) bool { called[0] = true; return false },
			func(IPNState) bool { called[1] = true; return true },
			func(IPNState) bool { called[2] = true; return false },
		},
	}

	changed := p.Process()

	for i, c := range called {
		if !c {
			t.Errorf("always[%d] was not called", i)
		}
	}
	if !changed {
		t.Error("expected changed=true since one always op returned true")
	}
}

func TestProcessor_NoneChanged(t *testing.T) {
	p := &ipnProcessor{
		ipnState: IPNState{},
		always: []func(IPNState) bool{
			func(IPNState) bool { return false },
		},
		oneOf: []func(IPNState) bool{
			func(IPNState) bool { return false },
		},
	}

	if p.Process() {
		t.Error("expected changed=false")
	}
}

func TestProcessor_OneOfStopsAfterFirstMatch(t *testing.T) {
	called := [3]bool{}

	p := &ipnProcessor{
		ipnState: IPNState{},
		oneOf: []func(IPNState) bool{
			func(IPNState) bool { called[0] = true; return false },
			func(IPNState) bool { called[1] = true; return true },
			func(IPNState) bool { called[2] = true; return true }, // should not be called
		},
	}

	changed := p.Process()

	if !changed {
		t.Error("expected changed=true")
	}
	if !called[0] {
		t.Error("expected oneOf[0] to be called")
	}
	if !called[1] {
		t.Error("expected oneOf[1] to be called (first match)")
	}
	if called[2] {
		t.Error("expected oneOf[2] to NOT be called after first match")
	}
}

func TestProcessor_OneOfAllNoMatch(t *testing.T) {
	p := &ipnProcessor{
		ipnState: IPNState{},
		oneOf: []func(IPNState) bool{
			func(IPNState) bool { return false },
			func(IPNState) bool { return false },
		},
	}

	if p.Process() {
		t.Error("expected changed=false when no oneOf matches")
	}
}

func TestProcessor_Empty(t *testing.T) {
	p := &ipnProcessor{ipnState: IPNState{}}

	if p.Process() {
		t.Error("expected changed=false for empty processor")
	}
}

func TestProcessor_AlwaysAndOneOf(t *testing.T) {
	p := &ipnProcessor{
		ipnState: IPNState{},
		always: []func(IPNState) bool{
			func(IPNState) bool { return true },
		},
		oneOf: []func(IPNState) bool{
			func(IPNState) bool { return false },
			func(IPNState) bool { return false },
		},
	}

	if !p.Process() {
		t.Error("expected changed=true")
	}
}

func TestProcessor_FluentBuilder(t *testing.T) {
	tailnet := NewTailnet(1, "/tmp/test", "test-host", "", "", false, 1080, nil, nil, nil, nil, nil)

	state := IPNState{}

	// Just verify the fluent API doesn't panic
	changed := tailnet.ProcessIPN(state).
		Always(
			func(IPNState) bool { return false },
		).
		OneOf(
			func(IPNState) bool { return false },
		).
		Process()

	if changed {
		t.Error("expected changed=false")
	}
}
