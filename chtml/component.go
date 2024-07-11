package chtml

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"strings"

	"github.com/beevik/etree"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var (
	// ErrComponentNotFound is returned by Importer implementations when a component is not found.
	ErrComponentNotFound = errors.New("component not found")

	// ErrImportNotAllowed is returned when an Importer is not set for the component.
	ErrImportNotAllowed = errors.New("imports are not allowed")
)

type Component interface {
	// Render transforms the input data from the scope into another data object, typically
	// an HTML document (*html.Node) or anything else that can be sent over the wire or
	// passed to another Component as an input.
	Render(Scope) (any, error)
}

// ComponentFunc allows ordinary functions to act as Component instances.
type ComponentFunc func(Scope) (any, error)

// Render implements the Component interface, delegating the call to the ComponentFunc itself.
func (f ComponentFunc) Render(s Scope) (any, error) {
	return f(s)
}

// Disposable is an optional interface that can be implemented by components that need to
// release resources when they are no longer needed.
type Disposable interface {
	Dispose()
}

// Importer acts as a factory for components. It is invoked when a <c:NAME> element is encountered.
type Importer interface {
	Import(name string) (Component, error)
}

// ImporterFunc allows ordinary functions to act as Importer instances.
type ImporterFunc func(name string) (Component, error)

// Import implements the Importer interface, delegating the call to the ImporterFunc itself.
func (f ImporterFunc) Import(name string) (Component, error) {
	return f(name)
}

// chtmlParser parses a CHTML document and prepares it for rendering.
type chtmlParser struct {
	// fsys is the file system where the component is located.
	fsys fs.FS

	// filename is the name of the file where the component is located. Could be empty if the
	// component is not loaded from a file.
	fname string

	// doc is the root node of the parsed CHTML document
	doc *etree.Document

	// inpSchema are variables that the component expects as an input.
	// The map is populated with default values during parsing stage.
	inpSchema map[string]any

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

var _ Importer = (*chtmlParser)(nil)

// chtmlComponent is an instance of a CHTML component, ready to be rendered.
type chtmlComponent struct {
	// parser is a reference to the parser that created this component
	parser *chtmlParser

	// extraVars to be added to the isolated scope when rendering
	extraVars map[string]any

	// el is the root element of the component
	el *etree.Element

	// renderResult stores the data object returned by the component's Render method.
	renderResult any

	// vm is being used to evaluate expressions in rendering stage
	vm vm.VM

	// hidden stores nodes that have been marked as hidden and should not be rendered.
	hidden map[etree.Token]struct{}

	// ignoreLoop stores nodes that should ignore the c:for directive.
	ignoreLoop map[etree.Token]struct{}

	// children stores disposable resources created for nodes like imports and loops.
	// An XML node can have multiple children in case of c:for loops.
	children map[etree.Token][]Component

	// errs accumulates errors during rendering
	errs []error
}

var _ Component = (*chtmlComponent)(nil)
var _ Disposable = (*chtmlComponent)(nil)

// nodeMeta stores extra information about HTML nodes, such as prepared expressions for
// conditionals, loops, etc.
type nodeMeta struct {
	cond             *vm.Program
	loop             *vm.Program
	text             *vm.Program
	loopVar, loopIdx string
	imprt            func() Component
	nextCond         *etree.Element

	// attrs stores parsed values of HTML node attributes or <c:arg> elements.
	// Following value types are supported:
	// - string: static value
	// - *vm.Program: expression
	// - []etree.Token: raw XML nodes
	// All other types will be formatted as strings.
	attrs map[string]any

	// attrsKeys stores the keys of the attributes in the order they were parsed
	attrsKeys []string
}

// spawnComp tries to return already created scope for the given node or creates a new one.
// The n argument is used to distinguish between multiple components for the same node in c:for loops.
func (c *chtmlComponent) spawnComp(src *etree.Element, n int, loop bool, extraVars map[string]any) Component {
	// try to return an existing component:
	if n < len(c.children[src]) {
		return c.children[src][n]
	}

	var comp Component

	// make new instance of the component:
	nm := c.parser.meta[src]
	if nm == nil || nm.imprt == nil {
		// create a component from the source node
		el := &etree.Element{
			Child: []etree.Token{src},
		}
		comp = c.parser.component(el)
		comp.(*chtmlComponent).extraVars = extraVars
		if loop {
			comp.(*chtmlComponent).ignoreLoop[src] = struct{}{}
		}
	}
	if nm != nil && nm.imprt != nil {
		// create a component from the import
		comp = nm.imprt()
	}

	// register the new component in the children map:
	if comps, ok := c.children[src]; ok {
		// extend the slice if necessary
		if len(comps) <= n {
			extension := make([]Component, n-len(comps)+1)
			c.children[src] = append(comps, extension...)
		}
		c.children[src][n] = comp
	} else {
		c.children[src] = make([]Component, n+1)
		c.children[src][n] = comp
	}

	return comp
}

func (c *chtmlComponent) Dispose() {
	for src := range c.children {
		c.disposeComponents(src, 0)
	}
}

// disposeComponents closes all child components starting from the given index.
// This is used to close components in c:for loops and c:if.
func (c *chtmlComponent) disposeComponents(src etree.Token, n int) {
	if comps, ok := c.children[src]; ok {
		for i := n; i < len(comps); i++ {
			if d, ok := comps[i].(Disposable); ok {
				d.Dispose()
			}
		}
		c.children[src] = comps[:n]
	}
	if n == 0 {
		delete(c.children, src)
	}
}

func (c *chtmlComponent) error(t etree.Token, err error) {
	c.errs = append(c.errs, newComponentError(c.parser.fname, t, err))
}

// Parse parses the CHTML component from the given reader into a suitable representation for
// rendering.
func Parse(r io.Reader, imp Importer) (Importer, error) {
	cp := &chtmlParser{
		importer: imp,
	}

	cp.parse(r)

	return cp, cp.err
}

// ParseFile parses the CHTML component from the given file. Unlike Parse, it may also watch
// for changes in the file and trigger a re-parse when necessary.
func ParseFile(fsys fs.FS, fname string, imp Importer) (Importer, error) {
	fname = strings.TrimPrefix(fname, "/")

	f, err := fsys.Open(fname)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if !strings.HasPrefix(fname, ".") {
				return ParseFile(fsys, "."+fname, imp)
			}
			return nil, ErrComponentNotFound
		}
		return nil, fmt.Errorf("open component %s: %w", fname, err)
	}
	defer func() { _ = f.Close() }()

	cp := &chtmlParser{
		importer: imp,
		fsys:     fsys,
		fname:    fname,
	}

	cp.parse(f)

	return cp, cp.err
}

func (cp *chtmlParser) Import(name string) (Component, error) {
	// TODO: resolve sub-component by name
	return cp.component(&cp.doc.Element), nil
}

func (cp *chtmlParser) component(el *etree.Element) *chtmlComponent {
	return &chtmlComponent{
		parser:       cp,
		el:           el,
		renderResult: nil,
		hidden:       make(map[etree.Token]struct{}),
		ignoreLoop:   make(map[etree.Token]struct{}),
		children:     make(map[etree.Token][]Component),
		errs:         nil,
	}
}

// parse walks the document tree and populates the component's meta map with extra
// information about the nodes.
// On a root level of a document, it looks for <c:arg> elements that define the component's input
// arguments.
func (cp *chtmlParser) parse(r io.Reader) {
	cp.doc = etree.NewDocument()

	// Disable strict mode for XML decoder, see https://pkg.go.dev/encoding/xml#Decoder
	cp.doc.ReadSettings.Permissive = true
	cp.doc.ReadSettings.AutoClose = xml.HTMLAutoClose // auto-close common HTML elements

	// init meta with the root node
	cp.meta = map[etree.Token]*nodeMeta{
		&cp.doc.Element: {},
	}

	// create a new input schema with the implicit argument "_"
	cp.inpSchema = map[string]any{"_": new(any)}

	cp.shadowed = make(map[string][]any)

	if _, err := cp.doc.ReadFrom(r); err != nil {
		cp.error(&cp.doc.Element, fmt.Errorf("read XML document: %w", err))
		return
	}

	cp.cleanWhitespace(&cp.doc.Element)

	for _, child := range cp.doc.Element.Child {
		if n, ok := child.(*etree.Element); ok && n.FullTag() == "c:arg" {
			cp.parseArg(n)
		} else {
			cp.parseToken(child)
		}
	}
}

// cleanWhitespace removes any whitespace text nodes in children of the given node.
func (cp *chtmlParser) cleanWhitespace(el *etree.Element) {
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
func (cp *chtmlParser) parseToken(t etree.Token) {
	switch n := t.(type) {
	case *etree.Element:
		if n.Space == "c" {
			cp.parseImport(n)
		} else {
			cp.parseHTML(n)
		}
	case *etree.CharData:
		if !n.IsWhitespace() {
			cp.parseText(n)
		}
	}
}

// parseArg parses the <c:arg> element and stores its children in the parent's node metadata.
func (cp *chtmlParser) parseArg(el *etree.Element) {
	name := el.SelectAttrValue("name", "")

	if name == "" {
		cp.error(el, fmt.Errorf("missing name attribute in %s", el.FullTag()))
		return
	}

	if len(el.Attr) > 1 {
		cp.error(el, fmt.Errorf("unexpected attributes in %s", el.FullTag()))
		return
	}

	parent := el.Parent()
	nm := cp.meta[parent]
	if nm == nil {
		nm = &nodeMeta{}
		cp.meta[parent] = nm
	}
	if nm.attrs == nil {
		nm.attrs = make(map[string]any)
	}

	if _, ok := nm.attrs[name]; ok {
		cp.error(el, fmt.Errorf("duplicate argument %q in %s", name, parent.FullTag()))
		return
	}

	// populate c.meta with parsed expressions:
	for _, t := range el.Child {
		cp.parseToken(t)
	}

	nm.attrs[name] = cp.parseArgChildren(name, el)
	nm.attrsKeys = append(nm.attrsKeys, name)
}

func (cp *chtmlParser) parseArgChildren(name string, el *etree.Element) any {
	// treat the argument as a component
	comp := cp.component(el)
	defer comp.Dispose()

	// get a sample of component's output to build the input schema
	rr, err := comp.Render(&BaseScope{vars: cp.inpSchema})
	if err != nil {
		cp.error(el, fmt.Errorf("eval import: %w", err))
		return nil
	}

	// TODO: walk recursively through the component's output to build the input schema
	// convert any element of *html.Node type to &html.Node{}
	if rr == nil {
		cp.inpSchema[name] = new(any)
	} else if _, ok := rr.(*html.Node); ok {
		cp.inpSchema[name] = &html.Node{}
	} else {
		cp.inpSchema[name] = rr
	}
	return comp
}

// parseImport parses the <c:NAME> element
func (cp *chtmlParser) parseImport(el *etree.Element) {
	compName := el.Tag

	if compName == "arg" {
		cp.error(el, fmt.Errorf("c:arg element is not allowed in this context"))
		return
	}

	if cp.importer == nil {
		cp.error(el, ErrImportNotAllowed)
		return
	}

	nm := &nodeMeta{
		imprt: func() Component {
			comp, err := cp.importer.Import(compName)
			if err != nil {
				cp.error(el, fmt.Errorf("import %s: %w", compName, err))
				return nil
			}
			return comp
		},
	}
	cp.meta[el] = nm

	cp.parseAttrs(el)

	var defaultArg []etree.Token

	cp.cleanWhitespace(el)

	for _, child := range el.Child {
		if n, ok := child.(*etree.Element); ok && n.FullTag() == "c:arg" {
			cp.parseArg(n)
		} else {
			cp.parseToken(child)
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

	// import and dispose the component to check for input schema errors
	comp, err := cp.importer.Import(compName)
	if err != nil {
		cp.error(el, fmt.Errorf("import %s: %w", compName, err))
		return
	}
	if d, ok := comp.(Disposable); ok {
		defer d.Dispose()
	}
	s := NewScope(cp.inpSchema)
	if _, err := comp.Render(s); err != nil {
		cp.error(el, fmt.Errorf("render %s: %w", compName, err))
		return
	}
}

func (cp *chtmlParser) parseHTML(el *etree.Element) {
	cp.parseAttrs(el)

	for _, child := range el.Child {
		cp.parseToken(child)
	}

	if cp.meta[el] != nil {
		if cp.meta[el].loopVar != "" {
			cp.pop(cp.meta[el].loopVar)
		}
		if cp.meta[el].loopIdx != "" {
			cp.pop(cp.meta[el].loopIdx)
		}
	}
}

func (cp *chtmlParser) parseText(n *etree.CharData) {
	p, err := Interpol(n.Data, cp.inpSchema)
	if err != nil {
		cp.error(n, err)
		return
	}
	if p != nil {
		cp.meta[n] = &nodeMeta{text: p}
	}
}

func (cp *chtmlParser) findPrevCond(n *etree.Element) *etree.Element {
	parent := n.Parent()

	for i := n.Index() - 1; i >= 0; i-- {
		sibling, ok := parent.Child[i].(*etree.Element)
		if !ok {
			continue
		}
		if nm, ok := cp.meta[sibling]; ok && nm.cond != nil {
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
func (cp *chtmlParser) parseAttrs(el *etree.Element) {
	specialAttrs := map[string]string{}
	hasAttr := func(key string) bool {
		_, ok := specialAttrs[key]
		return ok
	}

	nm := cp.meta[el]

	if len(el.Attr) > 0 {
		if nm == nil {
			nm = &nodeMeta{}
			cp.meta[el] = nm
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
				cp.error(el, fmt.Errorf("cannot use %s twice on the same element", fk))
				return
			}
			specialAttrs[fk] = attr.Value
		default:
			p, err := Interpol(attr.Value, cp.inpSchema)
			if err != nil {
				cp.error(el, fmt.Errorf("parse attribute %s: %w", fk, err))
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
		cp.error(el, errors.New("cannot use c:if with c:else-if on the same element"))
	}
	if hasAttr("c:if") && hasAttr("c:else") {
		cp.error(el, errors.New("cannot use c:if with c:else on the same element"))
	}
	if hasAttr("c:else-if") && hasAttr("c:else") {
		cp.error(el, errors.New("cannot use c:else-if with c:else on the same element"))
	}
	if specialAttrs["c:else"] != "" && specialAttrs["c:else"] != "else" {
		cp.error(el, errors.New("unexpected value for c:else"))
	}

	if _, ok := specialAttrs["c:if"]; ok {
		prog, err := expr.Compile(specialAttrs["c:if"], expr.Optimize(true), expr.AsBool())
		if err != nil {
			cp.error(el, fmt.Errorf("parse c:if: %w", err))
			return
		}
		nm.cond = prog
	} else if _, ok := specialAttrs["c:else-if"]; ok {
		prog, err := expr.Compile(specialAttrs["c:else-if"], expr.Optimize(true), expr.AsBool())
		if err != nil {
			cp.error(el, fmt.Errorf("parse c:else-if: %w", err))
			return
		}
		nm.cond = prog
		if prevCond := cp.findPrevCond(el); prevCond != nil {
			cp.meta[prevCond].nextCond = el
		} else {
			cp.error(el, errors.New("c:else-if must be used after c:if"))
			return
		}
	} else if _, ok := specialAttrs["c:else"]; ok {
		prog, err := expr.Compile("true")
		if err != nil {
			cp.error(el, fmt.Errorf("parse c:else: %w", err))
			return
		}
		nm.cond = prog
		if prevCond := cp.findPrevCond(el); prevCond != nil {
			cp.meta[prevCond].nextCond = el
		} else {
			cp.error(el, errors.New("c:else must be used after c:if or c:else-if"))
			return
		}
	}
	if _, ok := specialAttrs["c:for"]; ok {
		v, k, ex, err := parseLoopExpr(specialAttrs["c:for"])
		if err != nil {
			cp.error(el, fmt.Errorf("parse c:for: %w", err))
			return
		}
		prog, err := expr.Compile(ex, expr.Optimize(true))
		if err != nil {
			cp.error(el, fmt.Errorf("parse c:for: %w", err))
			return
		}
		nm.loop = prog
		nm.loopVar = v
		nm.loopIdx = k

		cp.push(v, new(any))
		if k != "" {
			cp.push(k, 0)
		}
	}
}

// error captures the first error that occurred during parsing.
func (cp *chtmlParser) error(t etree.Token, err error) {
	if cp.err == nil {
		cp.err = newComponentError(cp.fname, t, err)
	}
}

// push adds a variable (argument) to the parsing context. If the current scope already has
// a variable with the same name, it is shadowed and restored when the variable is popped.
func (cp *chtmlParser) push(name string, v any) {
	if t, ok := cp.inpSchema[name]; ok {
		cp.shadowed[name] = append(cp.shadowed[name], t)
	}
	cp.inpSchema[name] = v
}

// pop removes a variable from the parsing context, restoring the previous value if it was shadowed.
func (cp *chtmlParser) pop(name string) any {
	v := cp.inpSchema[name]
	if len(cp.shadowed[name]) > 0 {
		p := cp.shadowed[name][len(cp.shadowed[name])-1]
		cp.shadowed[name] = cp.shadowed[name][:len(cp.shadowed[name])-1]
		cp.inpSchema[name] = p
	} else {
		delete(cp.inpSchema, name)
	}
	return v
}

// Render builds a new HTML document by evaluating the expressions in the component's tree and
// returns the result as *html.Node.
func (c *chtmlComponent) Render(s Scope) (any, error) {
	vars := map[string]any{}
	ss := s.Spawn(vars)

	// copy s.Vars() into vars
	for k, v := range c.parser.inpSchema {
		vars[k] = v
	}
	for k, v := range c.extraVars {
		vars[k] = v
	}

	if err := UnmarshalScope(s, &vars); err != nil {
		return nil, err
	}

	nm := c.parser.meta[c.el]
	if nm != nil {
		// make sure the all vars are present in the nm.attrs
		/* for k := range s.Vars() {
			if _, ok := nm.attrs[k]; !ok && k != "_" {
				// TODO: implement strict mode when passing arguments to components
				// c.error(c.el, fmt.Errorf("unrecognized argument %s", k))
			}
			// TODO: verify the type of the variable
		} */

		// apply default component's args to the scope:
		for k, v := range nm.attrs {
			if _, ok := s.Vars()[k]; !ok {
				switch vv := v.(type) {
				case *vm.Program:
					res, err := c.vm.Run(vv, env(vars))
					if err != nil {
						c.error(c.el, fmt.Errorf("eval default arg %s: %w", k, err))
						continue
					}
					vars[k] = res
				case Component:
					rr, err := vv.Render(ss)
					if err != nil {
						c.error(c.el, fmt.Errorf("eval default arg %s: %w", k, err))
						continue
					}
					vars[k] = rr
				default:
					vars[k] = v
				}
			}
		}
	}

	rr := c.eval(c.el.Child, ss)
	clear(c.hidden) // next time we render the component, we re-evaluate the conditions

	err := errors.Join(c.errs...)
	c.errs = nil

	return rr, err
}

// evalIf evaluates the conditional expression (c:if, c:else-if, c:else) for the given node and
// marks it as hidden if the condition is false.
// Returns true if the node should be rendered, false otherwise.
func (c *chtmlComponent) evalIf(src *etree.Element, s Scope) bool {
	if _, ok := c.hidden[src]; ok {
		return false
	}

	nm, ok := c.parser.meta[src]
	if !ok {
		return true
	}

	render := true

	if nm.cond != nil {
		res, err := c.vm.Run(nm.cond, env(s.Vars()))
		if _, ok := res.(bool); err == nil && !ok {
			err = errors.New("expression must return boolean")
		}
		if err != nil {
			c.error(src, fmt.Errorf("eval c:if: %w", err))
			render = false
		} else {
			render = res.(bool)
		}
	}
	if render {
		// mark next conditional as not rendered
		for next := nm.nextCond; next != nil; next = c.parser.meta[next].nextCond {
			c.hidden[next] = struct{}{}
		}
	} else {
		c.hidden[src] = struct{}{}
		c.disposeComponents(src, 0)
	}
	return render
}

// evalAttrs loops over the attributes of the source node and evaluates the expressions for them.
// If the attribute has no associated expression, it is copied as is.
// If the given element is an import, skip the evaluation and return immediately.
func (c *chtmlComponent) evalAttrs(dst *html.Node, src *etree.Element, s Scope) error {
	nm := c.parser.meta[src]
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
			res, err := c.vm.Run(attr, env(s.Vars()))
			if err != nil {
				return err // TODO: provide trace
			}
			sv = fmt.Sprint(res)
		case []etree.Token:
			continue // skip raw XML nodes
		case *html.Node:
			continue // skip HTML nodes
		default:
			if attr == nil {
				sv = ""
			} else {
				sv = fmt.Sprint(v)
			}
		}
		if sv == "<nil>" {
			sv = ""
		}
		attrs = append(attrs, html.Attribute{Key: k, Val: sv})
	}
	dst.Attr = nil
	if len(attrs) > 0 {
		dst.Attr = attrs
	}
	return nil
}

func (c *chtmlComponent) evalTag(src *etree.Element, s Scope) any {
	clone := &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Lookup([]byte(src.FullTag())),
		Data:     src.FullTag(),
	}

	// eval attributes into values for the cloned node
	if err := c.evalAttrs(clone, src, s); err != nil {
		c.error(src, fmt.Errorf("eval attributes: %w", err))
		return nil
	}

	rr := c.eval(src.Child, s)

	if c := AnyToHtml(rr); c != nil {
		clone.AppendChild(c)
	}

	return clone
}

// evalFor evaluates the loop expression (c:for) for the given node and appends the result to the
// destination node.
func (c *chtmlComponent) evalFor(src *etree.Element, s Scope) any {
	nm := c.parser.meta[src]

	res, err := c.vm.Run(nm.loop, env(s.Vars()))
	if err != nil {
		c.error(src, fmt.Errorf("eval c:for: %w", err))
		return nil
	}
	v := reflect.ValueOf(res)
	if v.Kind() != reflect.Slice {
		c.error(src, fmt.Errorf("c:for expression must return slice"))
		return nil
	}

	defer func() {
		c.disposeComponents(src, v.Len()) // close remaining children
	}()

	var acc any

	for i := 0; i < v.Len(); i++ {
		el := v.Index(i)
		vv := make(map[string]any)
		for k, v := range s.Vars() {
			vv[k] = v
		}
		vv[nm.loopVar] = el.Interface()
		if nm.loopIdx != "" {
			vv[nm.loopIdx] = i
		}

		extraVars := map[string]any{
			nm.loopVar: el.Interface(),
		}
		if nm.loopIdx != "" {
			extraVars[nm.loopIdx] = i
		}

		comp := c.spawnComp(src, i, true, extraVars)

		rr, err := comp.Render(s.Spawn(vv))
		if err != nil {
			c.error(src, fmt.Errorf("eval c:for: %w", err))
			return nil
		}
		acc = AnyPlusAny(acc, rr)
	}

	return acc
}

// evalImport renders the imported component (<c:NAME>) and appends the result to the destination.
func (c *chtmlComponent) evalImport(src *etree.Element, s Scope) any {
	nm := c.parser.meta[src]

	if nm == nil || nm.imprt == nil { // TODO: check if this is necessary
		return nil
	}

	args := make(map[string]any)

	for k, v := range nm.attrs {
		// eval expressions in the scope
		switch val := v.(type) {
		case *vm.Program:
			res, err := c.vm.Run(val, env(s.Vars()))
			if err != nil {
				c.error(src, fmt.Errorf("eval attr %q: %w", k, err))
				return nil
			}
			args[k] = res
		case []etree.Token:
			args[k] = c.eval(val, s)
		case Component:
			rr, err := val.Render(s.Spawn(s.Vars()))
			if err != nil {
				c.error(src, fmt.Errorf("eval attr %q: %w", k, err))
				return nil
			}
			args[k] = rr
		default:
			args[k] = v
		}

		// if the variable is an HTML node with a single text child, use it as the value
		if n, ok := args[k].(*html.Node); ok {
			if n.FirstChild != nil && n.FirstChild == n.LastChild && n.FirstChild.Type == html.TextNode {
				args[k] = n.FirstChild.Data
			}
		}
	}

	comp := c.spawnComp(src, 0, false, nil)

	rr, err := comp.Render(s.Spawn(args))
	if err != nil {
		c.error(src, fmt.Errorf("eval import: %w", err))
		return nil
	}
	return rr
}

// evalText evaluates the interpolated expression for the given text node and stores the result in
// the destination node.
// If the text node is not an expression, the value is copied as is.
func (c *chtmlComponent) evalText(t *etree.CharData, s Scope) any {
	nm := c.parser.meta[t]
	if nm == nil || nm.text == nil {
		return t.Data
	}
	res, err := c.vm.Run(nm.text, env(s.Vars()))
	if err != nil {
		c.error(t, fmt.Errorf("eval text: %w", err))
		return nil
	}
	return res
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
func (c *chtmlComponent) evalElement(src *etree.Element, s Scope) any {
	if c.evalIf(src, s) {
		nm := c.parser.meta[src]
		_, ignoreLoop := c.ignoreLoop[src]

		switch {
		case src.FullTag() == "c:arg": // TODO: find a better way to not render <c:arg> elements
			return nil
		case nm != nil && nm.loop != nil && !ignoreLoop:
			return c.evalFor(src, s)
		case nm != nil && nm.imprt != nil:
			return c.evalImport(src, s)
		default:
			return c.evalTag(src, s)
		}
	}
	return nil
}

func (c *chtmlComponent) eval(toks []etree.Token, s Scope) any {
	var acc, rr any
	for _, t := range toks {
		switch n := t.(type) {
		case *etree.Element:
			rr = c.evalElement(n, s)
		case *etree.CharData:
			rr = c.evalText(n, s)
		default:
			continue
		}
		acc = AnyPlusAny(acc, rr)
	}

	return acc
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
