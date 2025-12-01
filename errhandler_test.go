package pages

import (
	"errors"
	"io"
	"io/fs"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/dpotapov/go-pages/chtml"
	"github.com/stretchr/testify/require"
)

func TestErrorHandlerComponent_Render(t *testing.T) {
	tests := []struct {
		name           string
		importErr      error
		comp           chtml.Component
		compRenderErr  error
		fallback       *mockComponent
		fallbackName   string
		fsys           fs.FS
		wantErr        bool
		wantFallback   bool
		wantErrorsLen  int
		wantErrorTypes []string
	}{
		{
			name:          "successful render - no errors",
			importErr:     nil,
			comp:          &mockComponent{renderResult: "success"},
			compRenderErr: nil,
			fallback:      nil,
			fallbackName:  "",
			fsys:          nil,
			wantErr:       false,
			wantFallback:  false,
		},
		{
			name:           "import error - renders fallback",
			importErr:      errors.New("import failed"),
			comp:           nil,
			compRenderErr:  nil,
			fallback:       &mockComponent{renderResult: "fallback", captureScope: true},
			fallbackName:   "error-handler",
			fsys:           nil,
			wantErr:        false,
			wantFallback:   true,
			wantErrorsLen:  1,
			wantErrorTypes: []string{"generic"},
		},
		{
			name:           "component render error - renders fallback",
			importErr:      nil,
			comp:           &mockComponent{renderErr: errors.New("render failed")},
			compRenderErr:  nil,
			fallback:       &mockComponent{renderResult: "fallback", captureScope: true},
			fallbackName:   "error-handler",
			fsys:           nil,
			wantErr:        false,
			wantFallback:   true,
			wantErrorsLen:  1,
			wantErrorTypes: []string{"generic"},
		},
		{
			name:           "component error - renders fallback with enriched error",
			importErr:      nil,
			comp:           &mockComponent{renderErr: createComponentError(errors.New("component error"))},
			compRenderErr:  nil,
			fallback:       &mockComponent{renderResult: "fallback", captureScope: true},
			fallbackName:   "error-handler",
			fsys:           nil,
			wantErr:        false,
			wantFallback:   true,
			wantErrorsLen:  1,
			wantErrorTypes: []string{"generic"},
		},
		{
			name:           "http call error - renders fallback with http error type",
			importErr:      nil,
			comp:           &mockComponent{renderErr: createWrappedHttpCallError()},
			compRenderErr:  nil,
			fallback:       &mockComponent{renderResult: "fallback", captureScope: true},
			fallbackName:   "error-handler",
			fsys:           nil,
			wantErr:        false,
			wantFallback:   true,
			wantErrorsLen:  1,
			wantErrorTypes: []string{"http_call"},
		},
		{
			name:           "decode error - renders fallback with decode error type",
			importErr:      nil,
			comp:           &mockComponent{renderErr: createComponentError(&chtml.DecodeError{Key: "field", Err: errors.New("decode failed")})},
			compRenderErr:  nil,
			fallback:       &mockComponent{renderResult: "fallback", captureScope: true},
			fallbackName:   "error-handler",
			fsys:           nil,
			wantErr:        false,
			wantFallback:   true,
			wantErrorsLen:  1,
			wantErrorTypes: []string{"decode"},
		},
		{
			name:           "multiple component errors",
			importErr:      nil,
			comp:           &mockComponent{renderErr: errors.Join(createComponentError(errors.New("error1")), createComponentError(errors.New("error2")))},
			compRenderErr:  nil,
			fallback:       &mockComponent{renderResult: "fallback", captureScope: true},
			fallbackName:   "error-handler",
			fsys:           nil,
			wantErr:        false,
			wantFallback:   true,
			wantErrorsLen:  2,
			wantErrorTypes: []string{"generic", "generic"},
		},
		{
			name:          "no fallback - returns error",
			importErr:     nil,
			comp:          &mockComponent{renderErr: createComponentError(errors.New("component error"))},
			compRenderErr: nil,
			fallback:      nil,
			fallbackName:  "",
			fsys:          nil,
			wantErr:       true,
			wantFallback:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imp := &mockImporter{
				components: make(map[string]chtml.Component),
				errors:     make(map[string]error),
			}
			if tt.importErr != nil {
				imp.errors["test-component"] = tt.importErr
			} else if tt.comp != nil {
				imp.components["test-component"] = tt.comp
			}
			if tt.fallback != nil {
				imp.components[tt.fallbackName] = tt.fallback
			}

			fsys := tt.fsys
			if fsys == nil {
				fsys = &mockFS{}
			}

			eh := NewErrorHandlerComponent("test-component", imp, tt.fallbackName, fsys)

			scope := chtml.NewBaseScope(nil)
			result, err := eh.Render(scope)

			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				if tt.wantFallback {
					require.Equal(t, "fallback", result)
					// Check that errors were passed to fallback scope
					if tt.fallback != nil {
						if tt.fallback.capturedScope != nil {
							vars := tt.fallback.capturedScope.Vars()
							errorsVar, ok := vars["errors"]
							require.True(t, ok, "errors should be in fallback scope")
							errorsList, ok := errorsVar.([]*ErrorView)
							require.True(t, ok, "errors should be a list of *ErrorView")
							require.Len(t, errorsList, tt.wantErrorsLen, "errors list length mismatch")
							if len(tt.wantErrorTypes) > 0 {
								for i, errType := range tt.wantErrorTypes {
									require.Equal(t, errType, errorsList[i].Type, "error type mismatch at index %d", i)
								}
							}
						}
					}
				} else {
					require.Equal(t, "success", result)
				}
			}
		})
	}
}

func TestErrorHandlerComponent_HttpCallErrorEnrichment(t *testing.T) {
	ce := createWrappedHttpCallError()
	fallback := &mockComponent{renderResult: "fallback", captureScope: true}
	imp := &mockImporter{
		components: map[string]chtml.Component{
			"test-component": &mockComponent{renderErr: ce},
			"error-handler":  fallback,
		},
	}
	fsys := &mockFS{}

	eh := NewErrorHandlerComponent("test-component", imp, "error-handler", fsys)

	scope := chtml.NewBaseScope(nil)
	_, err := eh.Render(scope)
	require.NoError(t, err)

	require.NotNil(t, fallback.capturedScope)
	vars := fallback.capturedScope.Vars()
	errorsVar := vars["errors"]
	errorsList := errorsVar.([]*ErrorView)
	require.Len(t, errorsList, 1)

	errorObj := errorsList[0]
	require.Equal(t, "http_call", errorObj.Type)
	require.NotEmpty(t, errorObj.RootCause)
	require.NotNil(t, errorObj.Err)

	// Check that we can extract HttpCallError from the original error
	var httpErr *HttpCallError
	require.True(t, errors.As(errorObj.Err, &httpErr))
	require.NotNil(t, httpErr.Args)
	require.NotNil(t, httpErr.Response)
	require.Equal(t, "GET", httpErr.Args.Method)
	require.Equal(t, "/api/test", httpErr.Args.URL)
	require.Equal(t, 404, httpErr.Response.Code)
}

func TestErrorHandlerComponent_SourceCodeContext(t *testing.T) {
	ce := createComponentError(errors.New("test error"))
	ce.File = "test.chtml"
	ce.Line = 10
	ce.Column = 5
	ce.Length = 10

	fallback := &mockComponent{renderResult: "fallback", captureScope: true}
	imp := &mockImporter{
		components: map[string]chtml.Component{
			"test-component": &mockComponent{renderErr: ce},
			"error-handler":  fallback,
		},
	}
	fsys := &mockFS{
		files: map[string]string{
			"test.chtml": "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10 error here\nline 11\nline 12\nline 13",
		},
	}

	eh := NewErrorHandlerComponent("test-component", imp, "error-handler", fsys)

	scope := chtml.NewBaseScope(nil)
	_, err := eh.Render(scope)
	require.NoError(t, err)

	require.NotNil(t, fallback.capturedScope)
	vars := fallback.capturedScope.Vars()
	errorsVar := vars["errors"]
	errorsList := errorsVar.([]*ErrorView)
	require.Len(t, errorsList, 1)

	errorObj := errorsList[0]
	require.Greater(t, len(errorObj.Stack), 0, "Stack should have at least one frame")
	firstFrame := errorObj.Stack[0]
	require.Equal(t, 10, firstFrame.Line)
	require.Equal(t, 5, firstFrame.Column)
	require.Greater(t, len(firstFrame.SourceLines), 0)
}

func TestErrorHandlerComponent_HttpErrorTypeDetection(t *testing.T) {
	httpErr := &HttpCallError{
		Response: HttpCallResponse{
			Code:  404,
			Error: "not found",
		},
		Args: HttpCallArgs{
			Method: "GET",
			URL:    "/api/test",
		},
	}
	ce := createComponentError(httpErr)

	fallback := &mockComponent{renderResult: "fallback", captureScope: true}
	imp := &mockImporter{
		components: map[string]chtml.Component{
			"test-component": &mockComponent{renderErr: ce},
			"error-handler":  fallback,
		},
	}
	fsys := &mockFS{}

	eh := NewErrorHandlerComponent("test-component", imp, "error-handler", fsys)

	scope := chtml.NewBaseScope(nil)
	_, err := eh.Render(scope)
	require.NoError(t, err)

	require.NotNil(t, fallback.capturedScope)
	vars := fallback.capturedScope.Vars()
	errorsVar := vars["errors"]
	errorsList := errorsVar.([]*ErrorView)
	require.Len(t, errorsList, 1)

	errorObj := errorsList[0]
	require.Equal(t, "http_call", errorObj.Type)
	require.NotNil(t, errorObj.Err)

	// Can extract HttpCallError from original error
	var extractedHttpErr *HttpCallError
	require.True(t, errors.As(errorObj.Err, &extractedHttpErr))
	require.Equal(t, 404, extractedHttpErr.Response.Code)
}

// Helper types and functions

type mockComponent struct {
	renderResult  any
	renderErr     error
	capturedScope chtml.Scope
	captureScope  bool
}

func (m *mockComponent) Render(s chtml.Scope) (any, error) {
	if m.captureScope {
		m.capturedScope = s
	}
	return m.renderResult, m.renderErr
}

func (m *mockComponent) InputShape() *chtml.Shape  { return nil }
func (m *mockComponent) OutputShape() *chtml.Shape { return chtml.Any }

type mockImporter struct {
	components map[string]chtml.Component
	errors     map[string]error
}

func (m *mockImporter) Import(name string) (chtml.Component, error) {
	if err, ok := m.errors[name]; ok {
		return nil, err
	}
	if comp, ok := m.components[name]; ok {
		return comp, nil
	}
	return nil, errors.New("component not found: " + name)
}

type mockFS struct {
	files map[string]string
}

func (m *mockFS) Open(name string) (fs.File, error) {
	if m.files == nil {
		return nil, fs.ErrNotExist
	}
	content, ok := m.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return &mockFile{content: content}, nil
}

type mockFile struct {
	content string
	offset  int64
}

func (m *mockFile) Stat() (fs.FileInfo, error) {
	return &mockFileInfo{size: int64(len(m.content))}, nil
}

func (m *mockFile) Read(b []byte) (int, error) {
	if m.offset >= int64(len(m.content)) {
		return 0, io.EOF
	}
	n := copy(b, m.content[m.offset:])
	m.offset += int64(n)
	return n, nil
}

func (m *mockFile) Close() error {
	return nil
}

type mockFileInfo struct {
	size int64
}

func (m *mockFileInfo) Name() string       { return "mock" }
func (m *mockFileInfo) Size() int64        { return m.size }
func (m *mockFileInfo) Mode() fs.FileMode  { return 0644 }
func (m *mockFileInfo) ModTime() time.Time { return time.Time{} }
func (m *mockFileInfo) IsDir() bool        { return false }
func (m *mockFileInfo) Sys() interface{}   { return nil }

// createComponentError creates a ComponentError using reflection to set unexported fields
func createComponentError(err error) *chtml.ComponentError {
	ce := &chtml.ComponentError{}

	// Use reflection to set unexported fields
	rv := reflect.ValueOf(ce).Elem()

	// Set err field using reflect.NewAt to access unexported field
	errField := rv.FieldByName("err")
	if errField.IsValid() {
		// Create a new value at the field's address
		errFieldPtr := reflect.NewAt(errField.Type(), unsafe.Pointer(errField.UnsafeAddr())).Elem()
		errFieldPtr.Set(reflect.ValueOf(err))
	}

	// Set path field
	pathField := rv.FieldByName("path")
	if pathField.IsValid() {
		pathFieldPtr := reflect.NewAt(pathField.Type(), unsafe.Pointer(pathField.UnsafeAddr())).Elem()
		pathFieldPtr.SetString("test")
	}

	// Set exported fields directly
	ce.File = ""
	ce.Line = 0
	ce.Column = 0
	ce.Length = 0

	return ce
}

// createWrappedHttpCallError creates an HttpCallError wrapped in ComponentError
func createWrappedHttpCallError() *chtml.ComponentError {
	httpErr := &HttpCallError{
		Response: HttpCallResponse{
			Code:    404,
			Data:    nil,
			Error:   map[string]any{"detail": "Not found"},
			Success: false,
		},
		Args: HttpCallArgs{
			Method: "GET",
			URL:    "/api/test",
		},
	}
	return createComponentError(httpErr)
}
