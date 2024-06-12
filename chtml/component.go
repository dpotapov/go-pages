package chtml

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"reflect"
	"strings"

	"github.com/beevik/etree"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type Component interface {
	Render(s Scope) (*RenderResult, error)

	// InputSchema returns an example of the data object that the component expects as input.
	// InputSchema() map[string]any

	// ResultSchema returns an example of the data object that the component will return when
	// rendered. It is used for type checking and providing default values.
	ResultSchema() any
}

type RenderResult struct {
	Header     http.Header
	HTML       *html.Node
	StatusCode int
	Data       any
}

// ErrComponentNotFound is returned by Importer implementations when a component is not found.
var ErrComponentNotFound = errors.New("component not found")

// ErrImportNotAllowed is returned when an Importer is not set for the component.
var ErrImportNotAllowed = errors.New("imports are not allowed")

// Importer defines an interface for importing components, providing a method to
// retrieve a component provider by name. The component provider is a function that
// instantiates components, typically called during CHTML template parsing. For efficiency,
// the Import method can be invoked once at app initialization, assuming all components
// are known, and caching is handled to prevent redundant loads.
type Importer interface {
	Import(name string) (Component, error)
}

// ImporterFunc allows ordinary functions to act as Importer instances.
type ImporterFunc func(name string) (Component, error)

// Import implements the Importer interface, delegating the call to the ImporterFunc itself.
func (f ImporterFunc) Import(name string) (Component, error) {
	return f(name)
}

// nodeMeta stores extra information about HTML nodes, such as prepared expressions for
// conditionals, loops, etc.
type nodeMeta struct {
	cond             *vm.Program
	loop             *vm.Program
	text             *vm.Program
	loopVar, loopIdx string
	imprt            Component
	imprtVar         string // scope's variable name where the component's returned data will be stored
	nextCond         *etree.Element

	// attrs stores HTML node attributes with parsed values. Following value types are supported:
	// - string: static value
	// - *vm.Program: expression
	// - []etree.Token: raw XML nodes
	// All other types will be formatted as strings.
	attrs map[string]any

	// attrsKeys stores the keys of the attributes in the order they were parsed
	attrsKeys []string
}

// evalScope wraps the Scope with extra information for the rendering stage.
type evalScope struct {
	Scope

	// header stores the HTTP headers to be returned by the component.
	header http.Header

	// statusCode records the status code to be returned by the component.
	statusCode int

	// hidden stores nodes that have been marked as hidden and should not be rendered.
	hidden map[etree.Token]struct{}

	// expandedLoops stores nodes that have been expanded in a loop, to prevent infinite recursion.
	expandedLoops map[etree.Token]struct{}

	// scopes stores child scopes for nodes like imports and loops.
	scopes map[etree.Token][]*evalScope

	// closed is a channel that is closed when the scope is not going to be rendered.
	closed chan struct{}
}

func newEvalScope(s Scope) *evalScope {
	return &evalScope{
		Scope:         s,
		hidden:        make(map[etree.Token]struct{}),
		expandedLoops: make(map[etree.Token]struct{}),
		scopes:        make(map[etree.Token][]*evalScope),
		closed:        make(chan struct{}),
	}
}

func (es *evalScope) Spawn(vars map[string]any) Scope {
	return es.spawn(vars, nil, 0)
}

// spawn tries to return already created scope for the given node or creates a new one.
// The n argument is used to distinguish between multiple scopes for the same node in c:for loops.
func (es *evalScope) spawn(vars map[string]any, src etree.Token, n int) *evalScope {
	if scopes, ok := es.scopes[src]; ok {
		if n < len(scopes) {
			s := scopes[n]

			// update the scope with new variables
			scopeVars := s.Vars()
			for k, v := range vars {
				scopeVars[k] = v
			}

			return s
		}
	}
	// create a new scope
	s := &evalScope{
		Scope:         es.Scope.Spawn(vars),
		hidden:        es.hidden,
		expandedLoops: es.expandedLoops,
		scopes:        es.scopes,
		closed:        make(chan struct{}),
	}
	// register the new scope
	if scopes, ok := es.scopes[src]; ok {
		// extend the slice if necessary
		if len(scopes) <= n {
			extension := make([]*evalScope, n-len(scopes)+1)
			es.scopes[src] = append(scopes, extension...)
		}
		es.scopes[src][n] = s
	} else {
		es.scopes = make(map[etree.Token][]*evalScope)
		es.scopes[src] = make([]*evalScope, n+1)
		es.scopes[src][n] = s
	}
	return s
}

// close closes the current scope and all child scopes.
func (es *evalScope) close() {
	if es.closed == nil {
		return
	}

	for i := range es.scopes {
		for _, s := range es.scopes[i] {
			s.close()
		}
		es.scopes[i] = nil
	}
	es.scopes = nil
	close(es.closed)
	es.closed = nil
}

// closeChild closes all child scopes starting from the given index.
// This is used to close scopes in c:for loops.
func (es *evalScope) closeChild(src etree.Token, n int) {
	if scopes, ok := es.scopes[src]; ok {
		for i := n; i < len(scopes); i++ {
			scopes[i].close()
			scopes[i] = nil
		}
	}
}

// chtmlComponent is an instance of a CHTML component, ready to be rendered.
type chtmlComponent struct {
	// fsys is the file system where the component is located.
	fsys fs.FS

	// filename is the name of the file where the component is located. Could be empty if the
	// component is not loaded from a file.
	fname string

	// doc is the root node of the parsed CHTML document
	doc *etree.Document

	// vm is being used to evaluate expressions in parsing stage
	vm vm.VM

	// args is a map of variables that are passed to the component during rendering.
	// The map is populated with default values during parsing stage.
	args map[string]any

	// shadowed stores the original values of variables that were shadowed during parsing.
	shadowed map[string][]any

	// meta stores extra information about HTML nodes, such as prepared expressions
	// for conditionals, loops, etc. This is being populated during parsing stage.
	meta map[etree.Token]*nodeMeta

	// err stores the first error that occurred during parsing.
	err error

	// importer resolves component imports.
	importer Importer
}

// Parse parses the CHTML component from the given reader into a suitable representation for
// rendering.
func Parse(r io.Reader, imp Importer) (Component, error) {
	c := &chtmlComponent{
		importer: imp,
	}

	c.parse(r)

	return c, c.err
}

// ParseFile parses the CHTML component from the given file. Unlike Parse, it may also watch
// for changes in the file and trigger a re-parse when necessary.
func ParseFile(fsys fs.FS, fname string, imp Importer) (Component, error) {
	if strings.HasPrefix(fname, "/") {
		fname = fname[1:]
	}
	f, err := fsys.Open(fname)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrComponentNotFound
		}
		return nil, fmt.Errorf("open component %s: %w", fname, err)
	}
	defer func() { _ = f.Close() }()

	c := &chtmlComponent{
		importer: imp,
		fsys:     fsys,
		fname:    fname,
	}

	c.parse(f)

	return c, c.err
}

// parse walks the document tree and populates the component's meta map with extra
// information about the nodes.
// On a root level of a document, it looks for <c:arg> elements that define the component's input
// arguments.
func (c *chtmlComponent) parse(r io.Reader) {
	c.doc = etree.NewDocument()

	// Disable strict mode for XML decoder, see https://pkg.go.dev/encoding/xml#Decoder
	c.doc.ReadSettings.Permissive = true
	c.doc.ReadSettings.AutoClose = xml.HTMLAutoClose // auto-close common HTML elements

	c.meta = make(map[etree.Token]*nodeMeta)

	// create implicit argument
	c.meta[&c.doc.Element] = &nodeMeta{attrs: map[string]any{"_": nil}}
	c.args = c.meta[&c.doc.Element].attrs

	c.shadowed = make(map[string][]any)

	if _, err := c.doc.ReadFrom(r); err != nil {
		c.error(fmt.Errorf("read XML document: %w", err))
		return
	}

	c.cleanWhitespace(&c.doc.Element)

	for _, child := range c.doc.Element.Child {
		if n, ok := child.(*etree.Element); ok && n.FullTag() == "c:arg" {
			c.parseArg(n)
		} else {
			c.parseToken(child)
		}
	}
}

// cleanWhitespace removes any whitespace text nodes in children of the given node.
func (c *chtmlComponent) cleanWhitespace(el *etree.Element) {
	for i := 0; i < len(el.Child); i++ {
		if n, ok := el.Child[i].(*etree.CharData); ok && n.IsWhitespace() {
			el.RemoveChildAt(i)
			i--
		}
	}
}

// parseToken recursively parses the given node and its children, storing extra information about
// the node in the component's meta map.
//
// Parsing rules:
// - whitespace text nodes are skipped
// - text nodes are parsed as expressions if they contain ${...} syntax
// - <c:NAME> is a component import
// - other HTML tags are parsed for attributes and child nodes
func (c *chtmlComponent) parseToken(t etree.Token) {
	switch n := t.(type) {
	case *etree.Element:
		if n.Space == "c" {
			c.parseImport(n)
		} else {
			c.parseHTML(n)
		}
	case *etree.CharData:
		if !n.IsWhitespace() {
			c.parseText(n)
		}
	}
}

// parseArg parses the <c:arg> element and stores its children in the parent's node metadata.
func (c *chtmlComponent) parseArg(el *etree.Element) {
	name := el.SelectAttrValue("name", "")

	if name == "" {
		c.error(fmt.Errorf("missing name attribute in %s", el.FullTag()))
		return
	}

	if len(el.Attr) > 1 {
		c.error(fmt.Errorf("unexpected attributes in %s", el.FullTag()))
		return
	}

	parent := el.Parent()
	nm := c.meta[parent]
	if nm == nil {
		nm = &nodeMeta{}
		c.meta[parent] = nm
	}
	if nm.attrs == nil {
		nm.attrs = make(map[string]any)
	}

	if _, ok := nm.attrs[name]; ok {
		c.error(fmt.Errorf("duplicate argument %q in %s", name, parent.FullTag()))
		return
	}

	if len(el.Child) == 0 {
		nm.attrs[name] = new(any)
	} else {
		nm.attrs[name] = el.Child // TODO: remove whitespace text nodes?
	}

	nm.attrsKeys = append(nm.attrsKeys, name)

	for _, t := range el.Child {
		c.parseToken(t)
	}
}

// parseImport parses the <c:NAME> element
func (c *chtmlComponent) parseImport(el *etree.Element) {
	compName := el.Tag

	if compName == "arg" {
		c.error(fmt.Errorf("c:arg element is not allowed in this context"))
		return
	}

	if c.importer == nil {
		c.error(ErrImportNotAllowed)
		return
	}

	comp, err := c.importer.Import(compName)
	if err != nil {
		c.error(fmt.Errorf("import %s: %w", compName, err))
		return
	}

	imprtVar := el.SelectAttrValue("c:var", "")
	if imprtVar != "" {
		if _, ok := c.args[imprtVar]; ok {
			c.error(fmt.Errorf("variable %q is already defined", imprtVar))
			return
		}

		c.args[imprtVar] = comp.ResultSchema()

		c.push(imprtVar, comp.ResultSchema())
		defer c.pop(imprtVar)
	}

	nm := &nodeMeta{
		imprt:    comp,
		imprtVar: imprtVar,
	}
	c.meta[el] = nm

	c.parseAttrs(el)

	var defaultArg []etree.Token

	c.cleanWhitespace(el)

	for _, child := range el.Child {
		if n, ok := child.(*etree.Element); ok && n.FullTag() == "c:arg" {
			c.parseArg(n)
		} else {
			c.parseToken(child)
			defaultArg = append(defaultArg, child)
		}
	}

	if len(defaultArg) > 0 {
		if nm.attrs == nil {
			nm.attrs = make(map[string]any)
		}
		nm.attrs["_"] = defaultArg
		nm.attrsKeys = append(nm.attrsKeys, "_")
	}

	// TODO: check inputs for the component (attrs and <c:arg> values)
}

func (c *chtmlComponent) parseHTML(el *etree.Element) {
	c.parseAttrs(el)

	for _, child := range el.Child {
		c.parseToken(child)
	}

	if c.meta[el] != nil {
		if c.meta[el].loopVar != "" {
			c.pop(c.meta[el].loopVar)
		}
		if c.meta[el].loopIdx != "" {
			c.pop(c.meta[el].loopIdx)
		}
	}
}

func (c *chtmlComponent) parseText(n *etree.CharData) {
	p, err := Interpol(n.Data, c.args)
	if err != nil {
		c.error(err)
		return
	}
	if p != nil {
		c.meta[n] = &nodeMeta{text: p}
	}
}

func (c *chtmlComponent) findPrevCond(n *etree.Element) *etree.Element {
	parent := n.Parent()

	for i := n.Index() - 1; i >= 0; i-- {
		sibling, ok := parent.Child[i].(*etree.Element)
		if !ok {
			continue
		}
		if nm, ok := c.meta[sibling]; ok && nm.cond != nil {
			return sibling
		}
	}
	return nil
}

// parseAttrs parses the attributes of the given node and stores the results in the component's meta
// map.
// Parsing rules:
// - c:if and c:else-if attributes are compiled as expressions
// - c:else attribute is parsed as a conditional expression with "true" value
// - c:for attribute is parsed as a loop expression
// - other attributes are interpolated if they contain ${...} syntax
func (c *chtmlComponent) parseAttrs(el *etree.Element) {
	specialAttrs := map[string]string{}
	hasAttr := func(key string) bool {
		_, ok := specialAttrs[key]
		return ok
	}

	nm := c.meta[el]

	if len(el.Attr) > 0 {
		if nm == nil {
			nm = &nodeMeta{}
			c.meta[el] = nm
		}
		if nm.attrs == nil {
			nm.attrs = make(map[string]any)
			nm.attrsKeys = make([]string, 0, len(el.Attr))
		}
	}

	for _, attr := range el.Attr {
		fk := attr.FullKey()

		switch fk {
		case "c:if", "c:else-if", "c:else", "c:for":
			if _, ok := specialAttrs[fk]; ok {
				c.error(fmt.Errorf("cannot use %s twice on the same element", fk))
				return
			}
			specialAttrs[fk] = attr.Value
		default:
			p, err := Interpol(attr.Value, c.args)
			if err != nil {
				c.error(fmt.Errorf("parse attribute %s: %w", fk, err))
				return
			}
			if p != nil {
				nm.attrs[fk] = p
			} else {
				nm.attrs[fk] = attr.Value
			}
			nm.attrsKeys = append(nm.attrsKeys, fk)
		}
	}
	if hasAttr("c:if") && hasAttr("c:else-if") {
		c.error(errors.New("cannot use c:if with c:else-if on the same element"))
	}
	if hasAttr("c:if") && hasAttr("c:else") {
		c.error(errors.New("cannot use c:if with c:else on the same element"))
	}
	if hasAttr("c:else-if") && hasAttr("c:else") {
		c.error(errors.New("cannot use c:else-if with c:else on the same element"))
	}
	if specialAttrs["c:else"] != "" && specialAttrs["c:else"] != "else" {
		c.error(errors.New("unexpected value for c:else"))
	}

	if _, ok := specialAttrs["c:if"]; ok {
		prog, err := expr.Compile(specialAttrs["c:if"], expr.Optimize(true), expr.AsBool())
		if err != nil {
			c.error(fmt.Errorf("parse c:if: %w", err))
			return
		}
		nm.cond = prog
	} else if _, ok := specialAttrs["c:else-if"]; ok {
		prog, err := expr.Compile(specialAttrs["c:else-if"], expr.Optimize(true), expr.AsBool())
		if err != nil {
			c.error(fmt.Errorf("parse c:else-if: %w", err))
			return
		}
		nm.cond = prog
		if prevCond := c.findPrevCond(el); prevCond != nil {
			c.meta[prevCond].nextCond = el
		} else {
			c.error(errors.New("c:else-if must be used after c:if"))
			return
		}
	} else if _, ok := specialAttrs["c:else"]; ok {
		prog, err := expr.Compile("true")
		if err != nil {
			c.error(fmt.Errorf("parse c:else: %w", err))
			return
		}
		nm.cond = prog
		if prevCond := c.findPrevCond(el); prevCond != nil {
			c.meta[prevCond].nextCond = el
		} else {
			c.error(errors.New("c:else must be used after c:if or c:else-if"))
			return
		}
	}
	if _, ok := specialAttrs["c:for"]; ok {
		v, k, ex, err := parseLoopExpr(specialAttrs["c:for"])
		if err != nil {
			c.error(fmt.Errorf("parse c:for: %w", err))
			return
		}
		prog, err := expr.Compile(ex, expr.Optimize(true))
		if err != nil {
			c.error(fmt.Errorf("parse c:for: %w", err))
			return
		}
		nm.loop = prog
		nm.loopVar = v
		nm.loopIdx = k

		c.push(v, new(any))
		if k != "" {
			c.push(k, 0)
		}
	}
}

// Render builds a new HTML document by evaluating the expressions in the component's tree and
// stores the result in the scope's "_" variable.
func (c *chtmlComponent) Render(s Scope) (*RenderResult, error) {
	// create an evalScope if the given scope is not an evalScope
	var es *evalScope
	if evalScope, ok := s.(*evalScope); ok {
		es = evalScope
	} else {
		es = newEvalScope(s)
		go func() { // TODO: remove the need for goroutine
			<-s.Closed()
			es.close()
		}()
	}

	// apply default arguments:
	vars := es.Vars()
	for k, v := range c.args {
		if _, ok := vars[k]; !ok {

			// if the argument is raw XML, evaluate into HTML:
			if tokens, ok := v.([]etree.Token); ok {
				n := &html.Node{Type: html.DocumentNode}
				for _, t := range tokens {
					if err := c.eval(n, t, es); err != nil {
						return nil, fmt.Errorf("eval arg %s: %w", k, err)
					}
				}
				v = n
			}

			vars[k] = v
		}
	}

	newDoc := &html.Node{
		Type: html.DocumentNode,
	}

	for _, child := range c.doc.Element.Child {
		if err := c.eval(newDoc, child, es); err != nil {
			return nil, err
		}
	}

	return &RenderResult{
		Header:     es.header,
		StatusCode: es.statusCode,
		HTML:       newDoc,
		Data:       nil, // the CHMTL component does not return any data
	}, nil
}

func (c *chtmlComponent) ResultSchema() any {
	return nil
}

// evalIf evaluates the conditional expression (c:if, c:else-if, c:else) for the given node and
// marks it as hidden if the condition is false.
func (c *chtmlComponent) evalIf(dst *html.Node, src *etree.Element, es *evalScope) error {
	render := true
	nm, ok := c.meta[src]
	if !ok {
		return nil
	}
	if nm.cond != nil {
		res, err := c.vm.Run(nm.cond, env(es.Vars()))
		if err != nil {
			return err
		}
		render, ok = res.(bool)
		if !ok {
			return errors.New("c:if expression must return boolean")
		}
	}
	if render {
		// mark next conditional as not rendered
		for next := nm.nextCond; next != nil; next = c.meta[next].nextCond {
			es.hidden[next] = struct{}{}
		}
	} else {
		es.hidden[src] = struct{}{}
		es.closeChild(src, 0) // TODO: close only child scopes or itself?
	}
	return nil
}

// evalAttrs loops over the attributes of the source node and evaluates the expressions for them.
// If the attribute has no associated expression, it is copied as is.
// If the given element is an import, skip the evaluation and return immediately.
func (c *chtmlComponent) evalAttrs(dst *html.Node, src *etree.Element, es *evalScope) error {
	nm := c.meta[src]
	if nm == nil || nm.attrs == nil {
		return nil
	}
	attrs := make([]html.Attribute, 0, len(nm.attrs))
	for k, v := range nm.attrs {
		var sv string

		switch attr := v.(type) {
		case string:
			sv = attr
		case *vm.Program:
			res, err := c.vm.Run(attr, env(es.Vars()))
			if err != nil {
				return err // TODO: provide trace
			}
			sv = fmt.Sprint(res)
		case []etree.Token:
			continue // skip raw XML nodes
		case *html.Node:
			continue // skip HTML nodes
		default:
			sv = fmt.Sprint(v)
		}
		attrs = append(attrs, html.Attribute{Key: k, Val: sv})
	}
	dst.Attr = nil
	if len(attrs) > 0 {
		dst.Attr = attrs
	}
	return nil
}

// evalFor evaluates the loop expression (c:for) for the given node and appends the result to the
// destination node.
func (c *chtmlComponent) evalFor(dst *html.Node, src *etree.Element, es *evalScope) error {
	nm := c.meta[src]
	if nm == nil || nm.loop == nil {
		return nil
	}
	if _, ok := es.expandedLoops[src]; ok {
		return nil
	}
	es.expandedLoops[src] = struct{}{}

	res, err := c.vm.Run(nm.loop, env(es.Vars()))
	if err != nil {
		return err // TODO: provide trace
	}
	v := reflect.ValueOf(res)
	if v.Kind() != reflect.Slice {
		return errors.New("c:for expression must return slice")
	}
	for i := 0; i < v.Len(); i++ {
		el := v.Index(i)
		subScope := es.spawn(map[string]any{
			nm.loopVar: el.Interface(),
			nm.loopIdx: i,
		}, src, i)
		if err := c.eval(dst, src, subScope); err != nil {
			return err
		}
	}

	es.closeChild(src, v.Len()) // close remaining scopes

	es.hidden[src] = struct{}{}
	return nil
}

// evalImport renders the imported component (<c:NAME>) and appends the result to the destination.
func (c *chtmlComponent) evalImport(dst *html.Node, src *etree.Element, es *evalScope) error {
	nm := c.meta[src]

	if nm == nil || nm.imprt == nil {
		return nil
	}

	vars := es.Vars()

	for k, v := range nm.attrs {
		// eval expressions in the scope
		switch val := v.(type) {
		case *vm.Program:
			res, err := c.vm.Run(val, env(vars))
			if err != nil {
				return err
			}
			vars[k] = res
		case []etree.Token:
			n := &html.Node{Type: html.DocumentNode}
			for _, t := range val {
				if err := c.eval(n, t, es); err != nil {
					return fmt.Errorf("eval attr %s: %w", k, err)
				}
			}
			vars[k] = n
		}

		// if the variable is an HTML node with a single text child, use it as the value
		if n, ok := vars[k].(*html.Node); ok {
			if n.FirstChild != nil && n.FirstChild == n.LastChild && n.FirstChild.Type == html.TextNode {
				vars[k] = n.FirstChild.Data
			}
		}
	}

	// retrieve the scope for the imported component or create a new one
	cs := es.spawn(nil, src, 0)

	// make the scope isolated (keep only vars matching the component's arguments).
	// using closure here just for grouping the code.
	func() {
		vars := cs.Vars()

		// remove all vars that do not belong to the component
		for k := range vars {
			if _, ok := nm.attrs[k]; !ok {
				delete(vars, k)
			}
		}
	}()

	rr, err := nm.imprt.Render(cs)
	if err != nil {
		return err
	}

	if rr.HTML != nil {
		dst.AppendChild(rr.HTML)
	}

	if rr.Data != nil && nm.imprtVar != "" {
		es.Vars()[nm.imprtVar] = rr.Data
	}

	// propagate the HTTP headers and status code

	if len(rr.Header) > 0 {
		if es.header == nil {
			es.header = make(http.Header)
		}
		for k, vv := range rr.Header {
			for _, v := range vv {
				es.header.Add(k, v)
			}
		}
	}

	if rr.StatusCode != 0 && es.statusCode == 0 {
		es.statusCode = rr.StatusCode
	}

	return nil
}

// evalText evaluates the interpolated expression for the given text node and stores the result in
// the destination node.
// If the text node is not an expression, the value is copied as is.
func (c *chtmlComponent) evalText(dst *html.Node, src *etree.CharData, es *evalScope) error {
	nm := c.meta[src]
	if nm == nil || nm.text == nil {
		dst.AppendChild(&html.Node{
			Type: html.TextNode,
			Data: src.Data,
		})
		return nil
	}
	res, err := c.vm.Run(nm.text, env(es.Vars()))
	if err != nil {
		return fmt.Errorf("eval text %s: %w", src.Parent().GetPath(), err)
	}
	switch v := res.(type) {
	case string:
		// if the result of the expression is a string, use it as the text node's value
		// and trim any leading/trailing whitespace
		dst.AppendChild(&html.Node{
			Type: html.TextNode,
			Data: strings.TrimSpace(v),
		})
	case *html.Node:
		// if the result of the expression is HTML, copy it recursively to the destination tree
		cloneTree(dst, v)
	default:
		// if the result of the expression is anything else, use it as the text node's value
		// and convert it to a string.
		if res != nil {
			dst.AppendChild(&html.Node{
				Type: html.TextNode,
				Data: fmt.Sprintf("%v", res),
			})
		}
	}
	return nil
}

// evalElement evaluates all expressions in conditionals, loops, child nodes, imports for the
// source node tree and clones relevant nodes to the destination tree.
//
// The evaluation process is performed in the following order:
// 1. conditionals (c:if, c:else-if, c:else)
// 2. loops (c:for)
// 3. import arguments (<c:arg>)
// 4. attributes
// 5. child nodes and child <c:arg> elements
// 6. imports (<c:NAME>)
func (c *chtmlComponent) evalElement(dst *html.Node, src *etree.Element, es *evalScope) error {
	if _, ok := es.hidden[src]; ok {
		return nil
	}
	if err := c.evalIf(dst, src, es); err != nil {
		return err
	}
	if _, ok := es.hidden[src]; ok {
		return nil
	}

	if err := c.evalFor(dst, src, es); err != nil {
		return err
	}
	if _, ok := es.hidden[src]; ok {
		return nil
	}

	if src.FullTag() == "c:arg" { // TODO: find a better way to not render <c:arg> elements
		return nil
	}

	nm := c.meta[src]
	if nm == nil || nm.imprt == nil {
		// if the element is not an import, clone it to the destination tree as HTML
		clone := &html.Node{
			Type:     html.ElementNode,
			DataAtom: atom.Lookup([]byte(src.FullTag())),
			Data:     src.FullTag(),
		}

		// eval attributes into values for the cloned node
		if err := c.evalAttrs(clone, src, es); err != nil {
			return err
		}

		for _, child := range src.Child {
			if err := c.eval(clone, child, es); err != nil {
				return err
			}
		}

		dst.AppendChild(clone)
	} else if nm.imprt != nil {
		if err := c.evalImport(dst, src, es); err != nil {
			return fmt.Errorf("eval import %s: %w", src.GetPath(), err)
		}
	}

	return nil
}

func (c *chtmlComponent) eval(dst *html.Node, src etree.Token, es *evalScope) error {
	switch n := src.(type) {
	case *etree.Element:
		return c.evalElement(dst, n, es)
	case *etree.CharData:
		return c.evalText(dst, n, es)
	}
	return nil
}

// error captures the first error that occurred during parsing.
func (c *chtmlComponent) error(err error) {
	if c.err == nil {
		c.err = err
	}
}

// push adds a variable (argument) to the parsing context. If the current scope already has
// a variable with the same name, it is shadowed and restored when the variable is popped.
func (c *chtmlComponent) push(name string, v any) {
	if t, ok := c.args[name]; ok {
		c.shadowed[name] = append(c.shadowed[name], t)
	}
	c.args[name] = v
}

// pop removes a variable from the parsing context, restoring the previous value if it was shadowed.
func (c *chtmlComponent) pop(name string) any {
	v := c.args[name]
	if len(c.shadowed[name]) > 0 {
		p := c.shadowed[name][len(c.shadowed[name])-1]
		c.shadowed[name] = c.shadowed[name][:len(c.shadowed[name])-1]
		c.args[name] = p
	} else {
		delete(c.args, name)
	}
	return v
}

func cloneNode(n *html.Node) *html.Node {
	clone := &html.Node{
		Type:     n.Type,
		DataAtom: n.DataAtom,
		Data:     n.Data,
	}
	if n.Attr != nil {
		clone.Attr = make([]html.Attribute, len(n.Attr))
		copy(clone.Attr, n.Attr)
	}
	return clone
}

func cloneTree(dst, src *html.Node) {
	for child := src.FirstChild; child != nil; child = child.NextSibling {
		clone := cloneNode(child)
		dst.AppendChild(clone)
		cloneTree(clone, child)
	}
}

func parseLoopExpr(s string) (v, k, expr string, err error) {
	l := &lexer{
		input: s,
		items: make([]item, 0),
	}

	for state := lexLoop; state != nil; {
		state = state(l)
	}

	idents := make([]string, 0, 3)

	for _, item := range l.items {
		if item.typ == itemError {
			return "", "", "", errors.New(item.val)
		}
		if item.typ == itemEOF {
			break
		}
		if item.typ == itemLoopIdent {
			idents = append(idents, item.val)
		}
		if item.typ == itemExpr {
			expr = item.val
		}
	}

	switch len(idents) {
	case 0:
		return "", "", "", errors.New("missing loop variable")
	case 1:
		return idents[0], "", expr, nil
	case 2:
		return idents[0], idents[1], expr, nil
	default:
		return "", "", "", errors.New("too many loop variables")
	}
}
