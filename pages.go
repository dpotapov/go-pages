//go:build !dev

package pages

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/dpotapov/go-pages/chtml"

	"github.com/gorilla/websocket"
	"golang.org/x/net/html"
)

// chtmlExt is the extension of the HTML component files. It is used when matching files
// in the file system.
const chtmlExt = ".chtml"

const (
	// specialRequestArg is the name of the HTTP request object passed to the root-level component.
	specialRequestArg = "$req"

	// specialResponseArg is the name of the HTTP reply object to carry between components.
	specialResponseArg = "$rep"
)

// defaultSearchPath is the default list of directories to search for components when importing.
var defaultSearchPath = []string{".", ".lib", "/", "/.lib"}

// validIdentifierRegex is a regular expression that matches valid keywords for dynamic
// matching purposes.
var validIdentifierRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// wsUpgrader is a Gorilla WebSocket instance, used to respond HTTP requests with WebSocket.
var wsUpgrader = websocket.Upgrader{}

type Handler struct {
	// FileSystem to serve HTML components and other web assets from.
	FileSystem fs.FS

	// ComponentSearchPath is a list of directories in the FileSystem to search for CHTML components.
	// The list may contain absolute or relative paths. Relative paths are resolved
	// relative to the rendered component's path.
	//
	// If not set, the following default paths are used:
	// 1. "." (the directory of the rendered component)
	// 2. ".lib" (a directory named ".lib" in the directory of the rendered component)
	// 3. "/" (the root directory of the FileSystem)
	// 4. "/.lib" (a directory named ".lib" in the root directory of the FileSystem)
	ComponentSearchPath []string

	// CustomImporter is called to import user-defined components before looking in the FileSystem.
	// If CustomImporter returns chtml.ErrComponentNotFound, the default import process is used.
	CustomImporter chtml.Importer

	// BuiltinComponents is a map of built-in components that can be used in CHTML files.
	BuiltinComponents map[string]chtml.Component

	// OnError is a callback that is called when an error occurs while serving a page.
	OnError func(*http.Request, error)

	// Logger configures logging for internal events.
	Logger *slog.Logger

	// init is used to initialize the handler only once.
	init sync.Once

	// logger is a private logger instance that is used to log internal events.
	logger *slog.Logger
}

// ServeHTTP implements the http.Handler interface.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.init.Do(func() {
		// initialize the logger:
		// TODO: replace with DiscardHandler in the future - https://go-review.googlesource.com/c/go/+/548335
		h.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
		if h.Logger != nil {
			h.logger = h.Logger
		}
	})

	if err := h.handleRequest(w, r); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

		h.logger.Error("Serve HTTP request", "url", r.URL.Redacted(), "error", err)

		if h.OnError != nil {
			h.OnError(r, err)
		}
	}
}

func (h *Handler) handleRequest(w http.ResponseWriter, r *http.Request) error {
	urlPath := cleanPath(r.URL.EscapedPath())

	params := map[string]string{}

	fsPath, err := h.matchFS(urlPath, ".", params)
	if err != nil {
		return err
	}

	if fsPath == "" {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return nil
	}

	if strings.HasSuffix(fsPath, chtmlExt) {
		args := map[string]any{}
		for k, v := range params {
			args[k] = v
		}

		if _, ok := args[specialRequestArg]; !ok {
			args[specialRequestArg] = NewRequestArg(r)
		}

		return h.servePage(w, r, fsPath, args)
	}

	return h.serveFile(w, r, fsPath)
}

func (h *Handler) servePage(w http.ResponseWriter, r *http.Request, fsPath string, args map[string]any) error {
	imp := h.importer(path.Dir(fsPath))

	comp, err := imp.Import(path.Base(strings.TrimSuffix(fsPath, chtmlExt)))
	if err != nil {
		return fmt.Errorf("get component %s: %w", fsPath, err)
	}

	scope := newScope(args)
	defer scope.close()

	if websocket.IsWebSocketUpgrade(r) {
		ws, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		// Render the component on:
		// 1. each incoming websocket message
		// 2. whenever a component is updated
		// Stop either when the websocket connection is closed or when the component will never be
		// changed.

		rc := make(chan struct{}) // renderer event channel
		done := make(chan error)  // channel to communicate the completion of the rendering loop

		scope.setOnChangeCallback(func() {
			select {
			case rc <- struct{}{}:
			default:
			}
		})

		go func() {
			for {
				if err := ws.ReadJSON(&args); err != nil {
					if websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
						err = nil
					} else {
						err = fmt.Errorf("read websocket message: %w", err)
					}
					done <- err // stop rendering loop
					return
				}

				// Trigger render on WebSocket message receipt
				select {
				case rc <- struct{}{}:
				default: // If rc is already pending, don't block
				}
			}
		}()

		for {
			select {
			case <-rc:
				// render the component
				w, err := ws.NextWriter(websocket.TextMessage)
				if err != nil {
					return fmt.Errorf("get websocket writer: %w", err)
				}

				// update scope vars with args
				scope.setVars(args)

				if err := comp.Execute(r.Context(), scope); err != nil {
					return fmt.Errorf("render component: %w", err)
				}

				if htmlDoc, ok := scope.Vars()["$html"].(*html.Node); ok {
					if err := html.Render(w, htmlDoc); err != nil {
						return fmt.Errorf("render HTML: %w", err)
					}
				}

				if err := w.Close(); err != nil {
					return fmt.Errorf("close websocket writer: %w", err)
				}

				args = nil // clear args
			case err := <-done:
				return err
			}
		}
	} else {
		if err := comp.Execute(r.Context(), scope); err != nil {
			return fmt.Errorf("execute component: %w", err)
		}

		rep := scope.Vars()[specialResponseArg]
		if rep != nil {
			// TODO: handle things like custom status codes, headers, redirects, etc.
			h.logger.Warn("Response object is not supported yet", "response", rep)
		}

		if htmlDoc, ok := scope.Vars()["$html"].(*html.Node); ok {
			if err := html.Render(w, htmlDoc); err != nil {
				return fmt.Errorf("render HTML: %w", err)
			}
		}
	}

	return nil
}

func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, fsPath string) error {
	r.URL.Path = fsPath
	r.URL.RawPath = fsPath
	http.FileServerFS(h.FileSystem).ServeHTTP(w, r)
	return nil
}

// match examples:
// - /foo/bar -> /foo/bar.chtml
// - /foo -> /foo/index.chtml
// - / -> /index.chtml
// - /foo/bar/ -> /foo/bar/index.chtml
// - /foo/bar/baz -> /foo/bar/baz.chtml
// - /foo/bar/baz/ -> /foo/bar/baz/index.chtml
// - /foo/file.txt -> /foo/file.txt
func (h *Handler) matchFS(urlPath, dir string, params map[string]string) (string, error) {
	if urlPath == "" {
		return "", nil
	}

	entries, err := fs.ReadDir(h.FileSystem, dir)
	if err != nil {
		return "", fmt.Errorf("read directory %s: %w", dir, err)
	}

	seg, rest := firstSegment(urlPath)

	// skip hidden files and directories
	if seg[0] == '.' {
		return "", nil
	}

	var m string

	if rest != "" {
		dir, err = h.matchDir(seg, dir, entries, params)
		if err != nil {
			return "", err
		}
		if dir != "" {
			m, err = h.matchFS(rest, dir, params)
		}
	} else {
		m, err = h.matchFile(seg, dir, entries, params)
	}
	if m != "" || err != nil {
		return m, err
	}

	// no match, try catch-all
	catchAllFile, err := findCatchAllFile(entries)
	if err != nil {
		return "", err
	}

	if catchAllFile != "" {
		argName := catchAllFile[2 : len(catchAllFile)-len(chtmlExt)]
		params[argName] = urlPath

		return catchAllFile, nil
	}

	return "", nil // no match
}

func (h *Handler) matchDir(seg, dir string, entries []fs.DirEntry, params map[string]string) (string, error) {
	dynamicMatch := ""

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()

		// check exact match
		if name == seg {
			return path.Join(dir, name), nil
		}

		if name[0] == '_' {
			if !validIdentifierRegex.MatchString(name[1:]) {
				return "", fmt.Errorf("invalid dynamic match in %s", dir)
			}
			if dynamicMatch != "" {
				return "", fmt.Errorf("multiple dynamic matches in %s", dir)
			}
			if params[name[1:]] != "" {
				return "", fmt.Errorf("duplicate dynamic match in %s", dir)
			}
			dynamicMatch = name
		}
	}

	// if no exact match, use the dynamic match
	if dynamicMatch != "" {
		params[dynamicMatch[1:]] = seg
		return path.Join(dir, dynamicMatch), nil
	}

	return "", nil // no match
}

func (h *Handler) matchFile(seg, dir string, entries []fs.DirEntry, params map[string]string) (string, error) {
	dynamicMatch := ""

	if seg == "/" {
		seg = "index"
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		entry.Type()
		name := entry.Name()

		if path.Ext(name) == chtmlExt {
			// match component by base name
			if strings.TrimSuffix(name, chtmlExt) == seg {
				return path.Join(dir, name), nil
			}

			if name[0] == '_' && len(name) > len(chtmlExt)+1 && !strings.HasPrefix(name, "__") {
				pn := name[1 : len(name)-len(chtmlExt)]
				if !validIdentifierRegex.MatchString(pn) {
					return "", fmt.Errorf("invalid dynamic match in %s", dir)
				}
				if dynamicMatch != "" {
					return "", fmt.Errorf("multiple dynamic matches in %s", dir)
				}
				if params[pn] != "" {
					return "", fmt.Errorf("duplicate dynamic match in %s", dir)
				}
				dynamicMatch = name
			}
		} else {
			// check exact match
			if name == seg {
				return path.Join(dir, name), nil
			}
		}
	}

	// if no exact match, use the dynamic match
	if dynamicMatch != "" {
		pn := dynamicMatch[1 : len(dynamicMatch)-len(chtmlExt)]
		params[pn] = seg
		return path.Join(dir, dynamicMatch), nil
	}

	return "", nil // no match
}

// importer builds a chtml.Importer that allows resolving components relative to
// provided dir path.
// Components are resolved by searching the name + ".chtml" extension in ComponentSearchPath.
func (h *Handler) importer(dir string) chtml.ImporterFunc {
	searchPath := h.ComponentSearchPath
	if len(searchPath) == 0 {
		searchPath = defaultSearchPath
	}

	return func(name string) (chtml.Component, error) {
		if h.CustomImporter != nil {
			prov, err := h.CustomImporter.Import(name)
			if err == nil || !errors.Is(err, chtml.ErrComponentNotFound) {
				return prov, err
			}
		}

		if cf, ok := h.BuiltinComponents[name]; ok {
			return cf, nil
		}

		for _, sp := range searchPath {
			p := name + chtmlExt

			// if the search path is absolute, ignore the source component's path:
			if path.IsAbs(sp) {
				p = path.Join(sp, p)
			} else {
				p = path.Join(dir, sp, p)
			}

			comp, err := chtml.ParseFile(h.FileSystem, p, h.importer(path.Dir(p)))
			if errors.Is(err, chtml.ErrComponentNotFound) {
				continue
			}

			return comp, err
		}

		return nil, chtml.ErrComponentNotFound
	}
}

// cleanPath returns the canonical path for p, eliminating . and .. elements.
//
// Copied from net/http/server.go
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	np := path.Clean(p)
	// path.Clean removes trailing slash except for root;
	// put the trailing slash back if necessary.
	if p[len(p)-1] == '/' && np != "/" {
		// Fast path for common case of p being the string we want:
		if len(p) == len(np)+1 && strings.HasPrefix(p, np) {
			np = p
		} else {
			np += "/"
		}
	}
	return np
}

// firstSegment splits path into its first segment, and the rest.
// The path must begin with "/".
// If path consists of only a slash, firstSegment returns ("/", "").
// The segment is returned unescaped, if possible.
//
// Copied from net/http/routing_tree.go.
func firstSegment(path string) (seg, rest string) {
	if path == "/" {
		return "/", ""
	}
	path = path[1:] // drop initial slash
	i := strings.IndexByte(path, '/')
	if i < 0 {
		i = len(path)
	}
	return pathUnescape(path[:i]), path[i:]
}

// Copied from net/http/routing_tree.go.
func pathUnescape(path string) string {
	u, err := url.PathUnescape(path)
	if err != nil {
		// Invalidly escaped path; use the original
		return path
	}
	return u
}

func findCatchAllFile(entries []fs.DirEntry) (string, error) {
	catchAll := ""

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, chtmlExt) || len(name) < 3 || name[:2] != "__" {
			continue
		}
		if catchAll != "" {
			return "", fmt.Errorf("multiple catch-all files found")
		}
		catchAll = name
	}

	return catchAll, nil
}

// RequestArg is a simplified model for http.Request suitable for expressions in templates.
type RequestArg struct {
	Method     string              `expr:"method"`
	URL        string              `expr:"url"`
	Host       string              `expr:"host"`
	Port       string              `expr:"port"`
	Scheme     string              `expr:"scheme"`
	Path       string              `expr:"path"`
	Query      map[string][]string `expr:"query"`
	RemoteAddr string              `expr:"remote_addr"`

	Headers map[string][]string `expr:"headers"`

	// Body is available only when the content type is either application/json or
	// application/x-www-form-urlencoded.
	Body map[string]any `expr:"body"`

	// RawBody is the Body field of the http.Request. If the content type is parseable as JSON or
	// form data, the RawBody will be closed.
	RawBody io.ReadCloser `expr:"raw_body"`
}

func NewRequestArg(r *http.Request) *RequestArg {
	model := &RequestArg{
		Method:     r.Method,
		URL:        r.RequestURI,
		Host:       r.URL.Hostname(),
		Port:       r.URL.Port(),
		Scheme:     r.URL.Scheme,
		Path:       r.URL.Path,
		Query:      r.URL.Query(),
		RemoteAddr: r.RemoteAddr,
		Headers:    r.Header,
		Body:       nil,
		RawBody:    r.Body,
	}

	// Parse JSON data
	ct := r.Header.Get("Content-Type")
	ct, _, _ = mime.ParseMediaType(ct)

	switch ct {
	case "application/json":
		_ = json.NewDecoder(r.Body).Decode(&model.Body) // TODO: log error
	case "application/x-www-form-urlencoded":
		err := r.ParseForm() // TODO: log error
		if err == nil {
			if len(r.PostForm) > 0 {
				model.Body = map[string]any{}
				for k, v := range r.PostForm {
					model.Body[k] = v
				}
			}
		}
	}

	return model
}
