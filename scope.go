package pages

import (
	"sync"

	"github.com/dpotapov/go-pages/chtml"
)

// Scope wraps chtml.ScopeMap with modification propagation callback and closing behavior.
type scope struct {
	chtml.Scope
	closed     chan struct{}
	onChangeCB func()

	mu sync.Mutex // protects onChangeCB
}

var _ chtml.Scope = (*scope)(nil)

func newScope(vars map[string]any) *scope {
	m := chtml.NewScopeMap(nil)
	m.SetVars(vars)

	return &scope{
		Scope:  m,
		closed: make(chan struct{}),
	}
}

func (s *scope) Spawn(vars map[string]any) chtml.Scope {
	return &scope{
		Scope:      s.Scope.Spawn(vars),
		closed:     make(chan struct{}),
		onChangeCB: s.onChangeCB,
	}
}

func (s *scope) Touch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.onChangeCB != nil {
		s.onChangeCB()
	}
}

func (s *scope) setOnChangeCallback(cb func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChangeCB = cb
}

func (s *scope) close() {
	if s.closed == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.onChangeCB = nil
	close(s.closed)
	s.closed = nil
}

func (s *scope) setVars(m map[string]any) {
	scopeMap := s.Scope.(*chtml.ScopeMap)
	scopeMap.SetVars(m)
}
