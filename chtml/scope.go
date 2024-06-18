package chtml

// Scope defines an interface for managing arguments in a CHTML component.
// The CHTML component creates a new scope for each loop iteration, conditional branch, and
// component import with Spawn method.
type Scope interface {
	// Spawn creates a new child scope with extra variables added to it.
	Spawn(vars map[string]any) Scope

	// Vars returns all variables in the scope.
	Vars() map[string]any

	// Closed returns a channel that is closed when the scope is not going to be rendered.
	Closed() <-chan struct{}

	// Touch marks the scope as changed. The change is propagated to the parent scopes.
	Touch()
}

// ScopeMap is a simple implementation of the Scope interface based on map[string]any type and
// suitable to work with expr-lang's args. This implementation copies the variables from the parent
// scope to the child scope.
type ScopeMap struct {
	vars map[string]any
}

var _ Scope = (*ScopeMap)(nil)

func NewScopeMap(parent Scope) *ScopeMap {
	vars := make(map[string]any)
	if parent != nil {
		for k, v := range parent.Vars() {
			vars[k] = v
		}
	}
	return &ScopeMap{
		vars: vars,
	}
}

// Spawn creates a new child scope of the current scope with its own set of arguments.
func (s *ScopeMap) Spawn(vars map[string]any) Scope {
	sm := NewScopeMap(s)
	for k, v := range vars {
		sm.vars[k] = v
	}
	return sm
}

func (s *ScopeMap) Vars() map[string]any {
	return s.vars
}

func (s *ScopeMap) Closed() <-chan struct{} {
	return nil
}

func (s *ScopeMap) Touch() {}

// SetVars replaces internal variables with the given map.
func (s *ScopeMap) SetVars(vars map[string]any) {
	if vars == nil {
		vars = make(map[string]any)
	}
	s.vars = vars
}
