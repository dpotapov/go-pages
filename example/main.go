package main

import (
	"log/slog"
	"net/http"
	"os"
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
    db  *todoDB
    sub chan struct{}
}

var _ chtml.Component = (*todos)(nil)
var _ chtml.Disposable = (*todos)(nil)

type todosArgs struct {
	Add string
	Del int
}

func (t *todos) Render(s chtml.Scope) (any, error) {
	var args todosArgs
	if err := chtml.UnmarshalScope(s, &args); err != nil {
		return nil, err
	}

	t.db.Add(args.Add)
	t.db.Del(args.Del)

	if t.sub == nil {
		t.sub = t.db.Subscribe()
		go func() {
			for range t.sub {
				s.Touch()
			}
		}()
	}

	todos := t.db.Todos()

	return todos, nil
}

func (t *todos) InputShape() *chtml.Shape {
    return chtml.Object(map[string]*chtml.Shape{
        "add": chtml.String,
        "del": chtml.Number,
    })
}

func (t *todos) OutputShape() *chtml.Shape { return chtml.ArrayOf(chtml.String) }

func (t *todos) Dispose() error {
	if t.sub != nil {
		t.db.Unsubscribe(t.sub)
	}
	return nil
}

type todoDB struct {
	todos       []string
	subscribers map[chan struct{}]struct{}
	mu          sync.Mutex
}

func newTodoDB() *todoDB {
	return &todoDB{
		subscribers: make(map[chan struct{}]struct{}),
		todos:       []string{},
	}
}

func (b *todoDB) Todos() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	todos := make([]string, len(b.todos))
	copy(todos, b.todos)
	return todos
}

func (b *todoDB) Add(todo string) {
	if todo == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.todos = append(b.todos, todo)
	b.notify()
}

func (b *todoDB) Del(index int) {
	if index <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if index <= len(b.todos) {
		b.todos = append(b.todos[:index-1], b.todos[index:]...)
		b.notify()
	}
}

func (b *todoDB) Subscribe() chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	sub := make(chan struct{}, 1)
	b.subscribers[sub] = struct{}{}
	return sub
}

func (b *todoDB) Unsubscribe(sub chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subscribers, sub)
	close(sub)
}

func (b *todoDB) notify() {
	for sub := range b.subscribers {
		select {
		case sub <- struct{}{}:
		default:
		}
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	assets := pages.NewAssetRegistry("", logger)
	assets.RegisterCollector("css", pages.NewStylesheetAssetCollector())
	assets.RegisterCollector("js", pages.NewJavascriptAssetCollector())

	ph := &pages.Handler{
		FileSystem: os.DirFS("./example/pages"),
		BuiltinComponents: map[string]func() chtml.Component{
			"request": pages.NewRequestComponentFactory(),
			"style":   pages.NewStyleComponentFactory(assets),
			"script":  pages.NewScriptComponentFactory(assets),
			"asset":   pages.NewAssetComponentFactory(assets),
		},
		CustomImporter: &todoStoreImporter{
			db: newTodoDB(),
		},
		FragmentSelector: pages.HTMXFragmentSelector,
		AssetCollector:   assets,
		OnError:          nil,
		Logger:           logger,
	}

	logger.Info("Starting HTTP server", "address", "http://localhost:8080")

	err := http.ListenAndServe(":8080", LoggerMiddleware(ph, logger))

	logger.Error("HTTP server error", "error", err)
}

type todoStoreImporter struct {
	db *todoDB
}

var _ chtml.Importer = (*todoStoreImporter)(nil)

func (i *todoStoreImporter) Import(name string) (chtml.Component, error) {
	if name == "todos-store" {
		return &todos{
			db: i.db,
		}, nil
	}
	return nil, chtml.ErrComponentNotFound
}
