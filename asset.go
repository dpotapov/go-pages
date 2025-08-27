package pages

import (
	"fmt"
	"hash"
	"hash/fnv"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dpotapov/go-pages/chtml"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// AssetCollector manages the collection, deduplication, versioning,
// and serving of assets discovered during template parsing or added manually.
type AssetCollector interface {
	// AddAsset stores the asset content associated with a given name.
	// The type of the asset (e.g., "css", "js") is determined from the name's extension.
	// Behavior for existing names depends on the implementation (append, replace).
	// It returns an error if the asset type is unsupported or adding fails.
	AddAsset(name string, content []byte) error

	// AssetPath returns the versioned, serveable path for an asset by name.
	// Example: "styles.css" might return "/css/styles.a1b2c3d4.css".
	// If the asset doesn't exist, it returns an empty string.
	AssetPath(name string) string

	// ServeAsset handles HTTP requests for assets managed by the collector.
	// It writes the asset content to the response writer if the request path matches
	// a known asset path.
	// Returns true if the request was handled (asset found and served or error occurred),
	// otherwise false (request path doesn't match a known asset).
	// Returns an error if serving the asset fails.
	ServeAsset(w http.ResponseWriter, r *http.Request) (handled bool, err error)
}

// --- AssetRegistry ---

// AssetRegistry holds multiple AssetCollector implementations, routing calls
// based on asset type derived from file extensions.
type AssetRegistry struct {
	logger     *slog.Logger
	collectors map[string]AssetCollector // Keyed by file extension (e.g., ".css", ".js")
	mu         sync.RWMutex
	basePath   string // Base path prefix for served assets (e.g., "/assets")
}

// NewAssetRegistry creates a new AssetRegistry.
func NewAssetRegistry(basePath string, logger *slog.Logger) *AssetRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	// Ensure basePath starts and ends with a slash if not empty
	bp := strings.Trim(basePath, "/")
	if bp != "" {
		bp = "/" + bp + "/"
	} else {
		bp = "/"
	}

	return &AssetRegistry{
		logger:     logger,
		collectors: make(map[string]AssetCollector),
		basePath:   bp,
	}
}

// RegisterCollector associates an AssetCollector with a specific file extension.
func (r *AssetRegistry) RegisterCollector(ext string, collector AssetCollector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext // Ensure extension starts with a dot
	}
	r.collectors[ext] = collector
	r.logger.Debug("Registered asset collector", "extension", ext)
}

// AddAsset routes the asset to the appropriate collector based on its extension.
func (r *AssetRegistry) AddAsset(name string, content []byte) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ext := filepath.Ext(name)
	collector, ok := r.collectors[ext]
	if !ok {
		return fmt.Errorf("no asset collector registered for type %s (asset: %s)", ext, name)
	}
	return collector.AddAsset(name, content)
}

// AssetPath finds the appropriate collector and returns the asset's path.
func (r *AssetRegistry) AssetPath(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ext := filepath.Ext(name)
	collector, ok := r.collectors[ext]
	if !ok {
		return ""
	}

	// Get the relative path from the specific collector
	relativePath := collector.AssetPath(name)
	if relativePath == "" {
		return ""
	}

	// Prepend the registry's base path
	// Note: collector.AssetPath should return path *without* the leading slash
	// if basePath is "/" to avoid "//"
	fullPath := r.basePath + strings.TrimPrefix(relativePath, "/")
	return fullPath
}

// ServeAsset tries to serve the request using the appropriate collector.
func (r *AssetRegistry) ServeAsset(w http.ResponseWriter, req *http.Request) (bool, error) {
	r.mu.RLock()
	collectorsSnapshot := maps.Clone(r.collectors) // Avoid holding lock during potentially long I/O
	r.mu.RUnlock()

	// Check each collector to see if it can handle the request path.
	// This requires the collector's ServeAsset method to determine if the path belongs to it.
	for ext, collector := range collectorsSnapshot {
		handled, err := collector.ServeAsset(w, req)
		if err != nil {
			r.logger.ErrorContext(req.Context(), "Error serving asset", "path", req.URL.Path, "extension", ext, "error", err)
			// Return true because we attempted to handle it, even if it failed.
			// The error is returned to the caller (e.g., pages.Handler).
			return true, fmt.Errorf("serve asset %s: %w", req.URL.Path, err)
		}
		if handled {
			return true, nil // Asset served successfully by this collector
		}
	}

	// No collector handled the request
	return false, nil
}

// --- BaseAssetCollector (Helper for JS/CSS) ---

// assetInfo holds the state for a single logical asset bundle (e.g., main.js)
// managed by a baseAssetCollector.
type assetInfo struct {
	contentBuilder strings.Builder
	versionHash    uint64 // FNV-1a hash of the content
	servePath      string // Calculated path like "js/main.abcdef1234.js"
}

// baseAssetCollector provides common functionality for CSS and JS collectors,
// handling deduplication via content hashing and versioning for multiple
// named assets (e.g., "main.js", "vendor.js") within a single collector instance.
type baseAssetCollector struct {
	mu sync.RWMutex
	// Stores the information for each named asset handled by this collector.
	assets map[string]*assetInfo // Key: logical asset name (e.g., "main.css")
	// Tracks hashes of content chunks already added globally within this collector
	// to prevent adding the exact same raw content block multiple times
	// across all managed assets.
	addedChunks map[uint64]struct{}
	// servePrefix (e.g., "/css", "/js") - defines the first part of the servePath.
	servePrefix string
	// contentType (e.g., "text/css") - HTTP content type for serving.
	contentType string
	// Hash function instance (reusable for chunk hashing)
	chunkHasher hash.Hash64
	// Maps the full, versioned serve path back to the logical asset name
	// for efficient request handling in ServeAsset.
	// Key: Full path like "/css/main.abcdef1234.css"
	// Value: Logical name like "main.css"
	servePathToName map[string]string
}

// newBaseAssetCollector creates a new collector for a specific type (CSS/JS).
func newBaseAssetCollector(servePrefix, contentType string) *baseAssetCollector {
	if !strings.HasPrefix(servePrefix, "/") {
		servePrefix = "/" + servePrefix
	}

	return &baseAssetCollector{
		assets:          make(map[string]*assetInfo),
		addedChunks:     make(map[uint64]struct{}),
		servePrefix:     servePrefix,
		contentType:     contentType,
		chunkHasher:     fnv.New64a(),
		servePathToName: make(map[string]string),
	}
}

// AddAsset adds content to a specific named asset (e.g., "main.js").
// If the asset name doesn't exist, it's created.
// Content is deduplicated based on its hash *before* being added.
// The version hash and serve path for the named asset are updated.
func (c *baseAssetCollector) AddAsset(name string, content []byte) error {
	// 1. Calculate chunk hash for deduplication
	c.chunkHasher.Reset()
	if _, err := c.chunkHasher.Write(content); err != nil {
		return fmt.Errorf("hash chunk for %s: %w", name, err)
	}
	chunkHash := c.chunkHasher.Sum64()

	// 2. Check if this exact chunk was already added *anywhere* in this collector
	c.mu.RLock() // Short RLock to check addedChunks
	_, exists := c.addedChunks[chunkHash]
	c.mu.RUnlock()

	if exists {
		return nil // Already processed this exact content block
	}

	// 3. Add chunk and update the specific asset
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check existence after acquiring write lock
	if _, exists := c.addedChunks[chunkHash]; exists {
		return nil
	}
	c.addedChunks[chunkHash] = struct{}{}

	// Find or create the assetInfo for this name
	ai, ok := c.assets[name]
	if !ok {
		ai = &assetInfo{}
		c.assets[name] = ai
	}

	// Store the old serve path before recalculating
	oldServePath := ai.servePath

	// Append content
	if ai.contentBuilder.Len() > 0 {
		ai.contentBuilder.WriteByte('\n')
	}
	ai.contentBuilder.Write(content)

	// Recalculate the FNV-1a hash for the *entire* content of this specific asset
	hashHasher := fnv.New64a()
	// WriteString is efficient as it avoids copying the builder's internal buffer
	if _, err := hashHasher.Write([]byte(ai.contentBuilder.String())); err != nil {
		return fmt.Errorf("hash content for %s: %w", name, err)
	}
	ai.versionHash = hashHasher.Sum64()

	// Regenerate the serve path with the new hash
	ext := filepath.Ext(name)
	baseName := strings.TrimSuffix(filepath.Base(name), ext)
	versionHex := fmt.Sprintf("%016x", ai.versionHash) // Use fixed width hex
	newServePath := fmt.Sprintf("%s/%s.%s%s", strings.TrimSuffix(c.servePrefix, "/"), baseName, versionHex[:16], ext)

	// Remove the old path from the lookup map if it existed and is different
	if oldServePath != "" && oldServePath != newServePath {
		delete(c.servePathToName, oldServePath)
	}

	// Update the lookup map with the new path
	c.servePathToName[newServePath] = name

	// Always serve the asset by name
	baseServePath := c.servePrefix + "/" + name
	c.servePathToName[baseServePath] = name

	// *** Assign the new path to the asset info struct ***
	ai.servePath = newServePath

	return nil
}

// AssetPath returns the currently active, versioned path for a named asset.
// Example: "css/main.abcdef1234.css"
func (c *baseAssetCollector) AssetPath(name string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if ai, ok := c.assets[name]; ok && ai.versionHash != 0 {
		return ai.servePath // Return the relative path part
	}
	return ""
}

// ServeAsset serves the content for a specific versioned asset path if it matches
// any asset managed by this collector.
func (c *baseAssetCollector) ServeAsset(w http.ResponseWriter, r *http.Request) (bool, error) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false, nil // Only handle GET/HEAD
	}

	requestPath := r.URL.Path

	// 1. Find the logical asset name corresponding to the request path.
	c.mu.RLock()
	assetName, nameFound := c.servePathToName[requestPath]
	var ai *assetInfo
	var ok bool
	if nameFound {
		// Now get the actual asset info using the name
		ai, ok = c.assets[assetName]
	}

	// Snapshot needed data if found
	var content string
	var contentType string
	var versionHash uint64
	if nameFound && ok {
		content = ai.contentBuilder.String()
		contentType = c.contentType // Use collector's content type
		versionHash = ai.versionHash
	}
	c.mu.RUnlock()

	// 2. If not found, this collector doesn't handle this path.
	if !nameFound || !ok {
		return false, nil
	}

	// 3. Serve the asset content
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	versionHex := fmt.Sprintf("%x", versionHash)
	w.Header().Set("ETag", `"`+versionHex+`"`)

	// Check ETag for 304 Not Modified
	if match := r.Header.Get("If-None-Match"); match != "" {
		if strings.Contains(match, versionHex) {
			w.WriteHeader(http.StatusNotModified)
			return true, nil
		}
	}

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return true, nil
	}

	_, err := io.WriteString(w, content)
	if err != nil {
		return true, fmt.Errorf("write asset content %s: %w", requestPath, err)
	}

	return true, nil // Handled successfully
}

// --- StylesheetAssetCollector ---

type StylesheetAssetCollector struct {
	*baseAssetCollector
}

// NewStylesheetAssetCollector creates a collector for CSS assets.
func NewStylesheetAssetCollector() *StylesheetAssetCollector {
	return &StylesheetAssetCollector{
		baseAssetCollector: newBaseAssetCollector("css", "text/css; charset=utf-8"),
	}
}

// --- JavascriptAssetCollector ---

type JavascriptAssetCollector struct {
	*baseAssetCollector
}

// NewJavascriptAssetCollector creates a collector for JavaScript assets.
func NewJavascriptAssetCollector() *JavascriptAssetCollector {
	return &JavascriptAssetCollector{
		baseAssetCollector: newBaseAssetCollector("js", "application/javascript; charset=utf-8"),
	}
}

// --- Builtin Components ---

// --- ScriptComponent ---

type ScriptArgs struct {
	Name    string // e.g., "main.js" or just "main" -> determines target collector/bundle
	Content string // Content is the body of the <c:script> tag
}

type ScriptComponent struct {
	collector AssetCollector
}

func NewScriptComponentFactory(collector AssetCollector) func() chtml.Component {
	instance := &ScriptComponent{collector: collector}
	return func() chtml.Component {
		return instance
	}
}

func (c *ScriptComponent) Render(s chtml.Scope) (any, error) {
    var args ScriptArgs
    if err := chtml.UnmarshalScope(s, &args); err != nil {
        return nil, fmt.Errorf("unmarshal scope for c:script: %w", err)
    }

	name := args.Name
	if name == "" {
		return nil, fmt.Errorf("c:script requires a non-empty 'name' attribute")
	}
	// Ensure name has an extension for routing in AssetRegistry
	if filepath.Ext(name) == "" {
		name += ".js"
	}

	content := args.Content
	if len(strings.TrimSpace(content)) == 0 {
		return nil, nil // Nothing to add
	}

    if err := c.collector.AddAsset(name, []byte(content)); err != nil {
        return nil, fmt.Errorf("add script asset %s: %w", name, err)
    }

    return nil, nil // Component doesn't render anything itself
}

func (c *ScriptComponent) InputShape() *chtml.Shape {
    return chtml.Object(map[string]*chtml.Shape{"name": chtml.String, "_": chtml.String})
}

func (c *ScriptComponent) OutputShape() *chtml.Shape { return nil }

// --- StyleComponent ---

type StyleArgs struct {
	Name    string // e.g., "styles.css" or "theme" -> determines target collector/bundle
	Content string // Content is the body of the <c:style> tag
}

type StyleComponent struct {
	assets AssetCollector
}

func NewStyleComponentFactory(assets AssetCollector) func() chtml.Component {
	instance := &StyleComponent{assets: assets}
	return func() chtml.Component {
		return instance
	}
}

func (c *StyleComponent) Render(s chtml.Scope) (any, error) {
    var args StyleArgs
    if err := chtml.UnmarshalScope(s, &args); err != nil {
        return nil, fmt.Errorf("unmarshal scope for c:style: %w", err)
    }

	name := args.Name
	if name == "" {
		return nil, fmt.Errorf("c:style requires a non-empty 'name' attribute")
	}
	// Ensure name has an extension
	if filepath.Ext(name) == "" {
		name += ".css"
	}

	content := args.Content
	if len(strings.TrimSpace(content)) == 0 {
		return nil, nil // Nothing to add
	}

    if err := c.assets.AddAsset(name, []byte(content)); err != nil {
        return nil, fmt.Errorf("add style asset %s: %w", name, err)
    }

    return nil, nil
}

func (c *StyleComponent) InputShape() *chtml.Shape {
    return chtml.Object(map[string]*chtml.Shape{"name": chtml.String, "_": chtml.String})
}

func (c *StyleComponent) OutputShape() *chtml.Shape { return nil }

// --- AssetComponent ---

type AssetArgs struct {
	Name string
}

type AssetComponent struct {
	assets AssetCollector
}

func NewAssetComponentFactory(assets AssetCollector) func() chtml.Component {
	instance := &AssetComponent{assets: assets}
	return func() chtml.Component {
		return instance
	}
}

func (c *AssetComponent) Render(s chtml.Scope) (any, error) {

	var args AssetArgs
	if err := chtml.UnmarshalScope(s, &args); err != nil {
		return nil, fmt.Errorf("unmarshal scope for c:asset: %w", err)
	}

	if args.Name == "" {
		return nil, fmt.Errorf("c:asset requires a non-empty 'name' attribute")
	}

	assetPath := c.assets.AssetPath(args.Name)
	if assetPath == "" {
		return nil, nil
	}
	ext := path.Ext(assetPath)

	var n *html.Node

	switch ext {
	case ".css":
		n = &html.Node{
			Type:     html.ElementNode,
			Data:     "link",
			DataAtom: atom.Link,
			Attr: []html.Attribute{
				{Key: "rel", Val: "stylesheet"},
				{Key: "href", Val: assetPath},
			},
		}

	case ".js":
		n = &html.Node{
			Type:     html.ElementNode,
			Data:     "script",
			DataAtom: atom.Script,
			Attr: []html.Attribute{
				{Key: "src", Val: assetPath},
			},
		}
	default:
		return nil, fmt.Errorf("c:asset supports only '.css' or '.js' types, got '%s'", ext)
	}

    return n, nil
}

func (c *AssetComponent) InputShape() *chtml.Shape {
    return chtml.Object(map[string]*chtml.Shape{"name": chtml.String})
}

func (c *AssetComponent) OutputShape() *chtml.Shape { return chtml.Any }
