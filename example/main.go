package main

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/dpotapov/go-pages"
	"github.com/dpotapov/go-pages/chtml"
)

func LoggerMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("HTTP request", "method", r.Method, "url", r.URL)
		next.ServeHTTP(w, r)
	})
}

type todos struct {
	todos         []string
	subscriptions map[chtml.Scope]chan struct{}
	mu            sync.Mutex
}

var _ chtml.Component = (*todos)(nil)

func (t *todos) subscription(s chtml.Scope) chan struct{} {
	if sub, ok := t.subscriptions[s]; ok {
		return sub
	}
	sub := make(chan struct{})
	t.subscriptions[s] = sub
	return sub
}

func (t *todos) Render(s chtml.Scope) (*chtml.RenderResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	changed := false

	if todo, ok := s.Vars()["add"].(string); ok && todo != "" {
		t.todos = append(t.todos, todo)
		changed = true
	}

	if todoDoneID, ok := s.Vars()["del"].(string); ok && todoDoneID != "" {
		i, err := strconv.ParseInt(todoDoneID, 10, 64)
		if err == nil && i >= 0 && i < int64(len(t.todos)) {
			t.todos = append(t.todos[:i], t.todos[i+1:]...)
			changed = true
		}
	}

	sub := t.subscription(s)

	if changed {
		for _, s := range t.subscriptions {
			if s != sub {
				s <- struct{}{}
			}
		}
	}

	go func() {
		// if the scope is closed, remove the subscription
		defer delete(t.subscriptions, s)
		for {
			select {
			case <-s.Closed():
				return
			case <-sub:
				s.Touch()
			}
		}
	}()

	return &chtml.RenderResult{
		Data: t.todos,
	}, nil
}

func (t *todos) ResultSchema() any {
	return []string{}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	ph := &pages.Handler{
		FileSystem: os.DirFS("./pages"),
		BuiltinComponents: map[string]chtml.Component{
			"todos-store": &todos{
				todos:         []string{},
				subscriptions: make(map[chtml.Scope]chan struct{}),
			},
		},
		OnError: nil,
		Logger:  logger,
	}

	logger.Info("Starting HTTP server", "address", "http://localhost:8080")

	err := http.ListenAndServe(":8080", LoggerMiddleware(ph, logger))

	logger.Error("HTTP server error", "error", err)
}
