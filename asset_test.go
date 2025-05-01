package pages

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dpotapov/go-pages/chtml"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
)

func TestScriptComponent_Render(t *testing.T) {
	assets := NewJavascriptAssetCollector()
	comp := NewScriptComponentFactory(assets)()

	s := chtml.NewDryRunScope(map[string]any{"name": "test.js", "_": "console.log('Hello, world!');"})

	_, err := comp.Render(s)
	require.NoError(t, err)

	assetPath := assets.AssetPath("test.js")
	require.Equal(t, "/js/test.c02ef03acd9bcafb.js", assetPath)

	assertAssetContent(t, assets, assetPath, "console.log('Hello, world!');")

	// Add more content to the test.js asset
	s = chtml.NewDryRunScope(map[string]any{"name": "test.js", "_": "console.log('Lorem ipsum dolor sit amet');"})

	_, err = comp.Render(s)
	require.NoError(t, err)

	assetPath = assets.AssetPath("test.js")
	require.Equal(t, "/js/test.b72d6cb04f0e4286.js", assetPath)

	assertAssetContent(t, assets, assetPath, "console.log('Hello, world!');\nconsole.log('Lorem ipsum dolor sit amet');")

	// There should be no asset anymore with previous hash
	assertAssetNotFound(t, assets, "/js/test.c02ef03acd9bcafb.js")

	// The non-hashed name should work
	assertAssetContent(t, assets, "/js/test.js", "console.log('Hello, world!');\nconsole.log('Lorem ipsum dolor sit amet');")

	// When rendering the same content again, there will be no changes to the asset
	s = chtml.NewDryRunScope(map[string]any{"name": "test.js", "_": "console.log('Hello, world!');"})
	_, err = comp.Render(s)
	require.NoError(t, err)

	assetPath = assets.AssetPath("test.js")
	require.Equal(t, "/js/test.b72d6cb04f0e4286.js", assetPath)

	assertAssetContent(t, assets, assetPath, "console.log('Hello, world!');\nconsole.log('Lorem ipsum dolor sit amet');")

	// Rendering with a regular Scope should return nil, nil and we expect no changes to the asset
	renderScope := chtml.NewBaseScope(map[string]any{"name": "test.js", "_": "console.log('Hello, world!');"})

	rr, err := comp.Render(renderScope)
	require.NoError(t, err)
	require.Nil(t, rr)

	assetPath = assets.AssetPath("test.js")
	require.Equal(t, "/js/test.b72d6cb04f0e4286.js", assetPath)
}

func TestStyleComponent_Render(t *testing.T) {
	assets := NewStylesheetAssetCollector()
	comp := NewStyleComponentFactory(assets)()

	// Initial content
	s := chtml.NewDryRunScope(map[string]any{"name": "test.css", "_": "body { color: red; }"})
	_, err := comp.Render(s)
	require.NoError(t, err)

	assetPath := assets.AssetPath("test.css")
	require.Equal(t, "/css/test.4a302240c13eaeb2.css", assetPath) // Hash depends on content
	assertAssetContent(t, assets, assetPath, "body { color: red; }")

	// Add more content to the test.css asset
	s = chtml.NewDryRunScope(map[string]any{"name": "test.css", "_": "p { font-size: 16px; }"})
	_, err = comp.Render(s)
	require.NoError(t, err)

	newAssetPath := assets.AssetPath("test.css")
	require.Equal(t, "/css/test.8434f14128bc97e5.css", newAssetPath) // Hash should change
	require.NotEqual(t, assetPath, newAssetPath, "Asset path should change when content is added")
	assertAssetContent(t, assets, newAssetPath, "body { color: red; }\np { font-size: 16px; }")

	// There should be no asset anymore with previous hash
	assertAssetNotFound(t, assets, assetPath)

	// The non-hashed name should work
	assertAssetContent(t, assets, "/css/test.css", "body { color: red; }\np { font-size: 16px; }")

	// When rendering the same content again (the first piece), there will be no changes to the asset
	// because the content is cumulative.
	s = chtml.NewDryRunScope(map[string]any{"name": "test.css", "_": "body { color: red; }"})
	_, err = comp.Render(s)
	require.NoError(t, err)

	assetPathAfterRepeat := assets.AssetPath("test.css")
	require.Equal(t, newAssetPath, assetPathAfterRepeat, "Asset path should not change when repeating existing content")
	assertAssetContent(t, assets, assetPathAfterRepeat, "body { color: red; }\np { font-size: 16px; }")

	// Rendering with a regular Scope should return nil, nil and we expect no changes to the asset
	renderScope := chtml.NewBaseScope(map[string]any{"name": "test.css", "_": "body { color: red; }"})
	rr, err := comp.Render(renderScope)
	require.NoError(t, err)
	require.Nil(t, rr)

	assetPathAfterRender := assets.AssetPath("test.css")
	require.Equal(t, newAssetPath, assetPathAfterRender, "Asset path should not change after rendering with a non-dry-run scope")
}

func TestAssetComponent_Render(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	assets := NewAssetRegistry("", logger)
	assets.RegisterCollector("css", NewStylesheetAssetCollector())
	assets.RegisterCollector("js", NewJavascriptAssetCollector())

	err := assets.AddAsset("test.css", []byte("body { color: red; }"))
	require.NoError(t, err)
	err = assets.AddAsset("test.js", []byte("console.log('Hello, world!');"))
	require.NoError(t, err)

	comp := NewAssetComponentFactory(assets)()

	t.Run("DryRun", func(t *testing.T) {
		ds := chtml.NewDryRunScope(map[string]any{"name": "test.css", "type": "css"})
		n, err := comp.Render(ds)
		require.NoError(t, err)
		require.Equal(t, &html.Node{}, n) // DryRun mode should return empty html node
	})

	t.Run("missing css file", func(t *testing.T) {
		s := chtml.NewBaseScope(map[string]any{"name": "missing.css", "type": "css"})
		n, err := comp.Render(s)
		require.NoError(t, err)
		require.Nil(t, n)
	})

	t.Run("missing js file", func(t *testing.T) {
		s := chtml.NewBaseScope(map[string]any{"name": "missing.js", "type": "js"})
		n, err := comp.Render(s)
		require.NoError(t, err)
		require.Nil(t, n)
	})

	t.Run("existing css file", func(t *testing.T) {
		s := chtml.NewBaseScope(map[string]any{"name": "test.css", "type": "css"})
		rr, err := comp.Render(s)
		require.NoError(t, err)
		assertHtmlEqual(t, rr.(*html.Node), `<link rel="stylesheet" href="/css/test.4a302240c13eaeb2.css"/>`)
	})

	t.Run("existing js file", func(t *testing.T) {
		s := chtml.NewBaseScope(map[string]any{"name": "test.js", "type": "js"})
		rr, err := comp.Render(s)
		require.NoError(t, err)
		assertHtmlEqual(t, rr.(*html.Node), `<script src="/js/test.c02ef03acd9bcafb.js"></script>`)
	})
}

// --- Helpers ---

// assertAssetContent checks that the asset at the given path exists and has the expected content.
func assertAssetContent(t *testing.T, assets AssetCollector, path, expectedContent string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()

	handled, err := assets.ServeAsset(rr, req)
	if err != nil {
		t.Fatalf("ServeAsset error for %q: %v", path, err)
	}
	if !handled {
		t.Fatalf("Asset at path %q was not handled (not found)", path)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for asset %q, got %d", path, rr.Code)
	}
	if rr.Body.String() != expectedContent {
		t.Fatalf("Asset content mismatch for %q:\nGot:  %q\nWant: %q", path, rr.Body.String(), expectedContent)
	}
}

// assertAssetNotFound checks that the asset at the given path does not exist (ServeAsset returns handled == false).
func assertAssetNotFound(t *testing.T, assets AssetCollector, path string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()

	handled, err := assets.ServeAsset(rr, req)
	if err != nil {
		t.Fatalf("ServeAsset error for %q: %v", path, err)
	}
	if handled {
		t.Fatalf("Expected ServeAsset to return handled=false for missing asset %q, but got handled=true (code=%d, body=%q)", path, rr.Code, rr.Body.String())
	}
}

func assertHtmlEqual(t *testing.T, n *html.Node, expectedHtml string) {
	t.Helper()

	buf := &bytes.Buffer{}
	err := html.Render(buf, n)
	require.NoError(t, err)
	require.Equal(t, expectedHtml, buf.String())
}
