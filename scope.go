package pages

import (
	"net/http"
	"sync"

	"github.com/dpotapov/go-pages/chtml"
)

// Scope wraps chtml.ScopeMap to provide extra functionality:
// - modification propagation callback
// - scope closing behavior
// - http request
type scope struct {
	chtml.Scope
	req        *http.Request
	closed     chan struct{}
	onChangeCB func()

	mu sync.Mutex // protects onChangeCB
}

var _ chtml.Scope = (*scope)(nil)

func newScope(vars map[string]any, req *http.Request) *scope {
	m := chtml.NewScopeMap(nil)
	m.SetVars(vars)

	return &scope{
		Scope:  m,
		req:    req,
		closed: make(chan struct{}),
	}
}

func (s *scope) Spawn(vars map[string]any) chtml.Scope {
	return &scope{
		Scope:      s.Scope.Spawn(vars),
		req:        s.req,
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
