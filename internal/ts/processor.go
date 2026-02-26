package ts

// ipnProcessor is a fluent builder for processing IPN state transitions.
type ipnProcessor struct {
	ipnState IPNState
	always   []func(IPNState) bool
	oneOf    []func(IPNState) bool
}

// ProcessIPN returns a new IPN processor for the given state.
func (t *Tailnet) ProcessIPN(ipnState IPNState) *ipnProcessor {
	return &ipnProcessor{
		ipnState: ipnState,
	}
}

// Always adds operations that always execute.
func (p *ipnProcessor) Always(ops ...func(IPNState) bool) *ipnProcessor {
	p.always = append(p.always, ops...)
	return p
}

// OneOf adds mutually exclusive transitions (only one can succeed).
func (p *ipnProcessor) OneOf(transitions ...func(IPNState) bool) *ipnProcessor {
	p.oneOf = append(p.oneOf, transitions...)
	return p
}

// Process executes the state processing logic and returns whether anything changed.
func (p *ipnProcessor) Process() bool {
	changed := false
	for _, op := range p.always {
		changed = op(p.ipnState) || changed
	}
	for _, transition := range p.oneOf {
		if transition(p.ipnState) {
			changed = true
			break
		}
	}
	return changed
}
