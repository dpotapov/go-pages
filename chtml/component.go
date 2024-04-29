package chtml

import (
	"context"
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

type Component interface {
	Execute(ctx context.Context, s Scope) error
}

// ErrComponentNotFound is returned by Importer implementations when a component is not found.
var ErrComponentNotFound = errors.New("component not found")

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
	attrs            []nodeAttr
	nextCond         *etree.Element
}

// nodeAttr stores prepared expression for HTML node attribute. If prog property is not nil, it
// means that the attribute value is an expression. Otherwise, it's a static string value.
type nodeAttr struct {
	key, val string
	prog     *vm.Program
}

// parseContext stores data helping to parse a component.
type parseContext struct {
	args map[string]any
}

func (pc *parseContext) clone() *parseContext {
	args := make(map[string]any, len(pc.args))
	for k, v := range pc.args {
		args[k] = v
	}
	return &parseContext{
		args: args,
	}
}

// evalScope wraps the Scope with extra information for the rendering stage.
type evalScope struct {
	Scope

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

	// args is populated with default values for arguments during parsing stage
	args map[string]any

	// doc is the root node of the parsed CHTML document
	doc *etree.Document

	// vm is being used to evaluate expressions in parsing stage
	vm vm.VM

	// meta stores extra information about HTML nodes, such as prepared expressions
	// for conditionals, loops, etc.
	meta map[etree.Token]*nodeMeta

	// importer resolves component imports.
	importer Importer
}

// Parse parses the CHTML component from the given reader into a suitable representation for
// rendering.
func Parse(r io.Reader, imp Importer) (Component, error) {
	return parse(r, imp)
}

func parse(r io.Reader, imp Importer) (*chtmlComponent, error) {
	// Since the CHTML file may not have a single root per XML spec, create an artificial one:
	root := etree.NewElement("c:root")
	root.CreateAttr("xmlns:c", "chtml")

	c := &chtmlComponent{
		args: map[string]any{
			"_": nil, // always provide a default argument
		},
		doc:      etree.NewDocumentWithRoot(root),
		meta:     make(map[etree.Token]*nodeMeta),
		importer: imp,
	}

	// Parse the document into an etree:
	tmpDoc := etree.NewDocument()

	// Disable strict mode for XML decoder, see https://pkg.go.dev/encoding/xml#Decoder
	tmpDoc.ReadSettings.Permissive = true
	tmpDoc.ReadSettings.AutoClose = xml.HTMLAutoClose // auto-close common HTML elements

	_, err := tmpDoc.ReadFrom(r)
	if err != nil {
		return nil, err
	}

	pc := &parseContext{args: c.args}

	// iterate over child nodes in the root element
	for _, child := range append([]etree.Token{}, tmpDoc.Child...) {
		switch n := child.(type) {
		case *etree.Element:
			// parse & remove c:arg elements on the root level
			if n.FullTag() == "c:arg" {
				if err := c.parseArgs(n, pc.args); err != nil {
					return nil, fmt.Errorf("parse args: %w", err)
				}
				tmpDoc.RemoveChildAt(n.Index())
			} else {
				// parse remaining elements recursively for imports, conditionals, loops, etc.
				if err := c.parseElement(n, pc); err != nil {
					return nil, err
				}
			}

		case *etree.CharData:
			// do not render whitespace text nodes at the root level
			if !n.IsWhitespace() {
				if err := c.parseText(n, pc); err != nil {
					return nil, err // TODO: provide trace
				}
			} else {
				tmpDoc.RemoveChildAt(n.Index())
			}
		default:
			// remove any non-element and non-text nodes (comments, directives, etc.)
			tmpDoc.RemoveChildAt(n.Index())
		}
	}

	for i := len(tmpDoc.Child); i > 0; i-- {
		root.AddChild(tmpDoc.Child[0])
	}

	return c, nil
}

// ParseFile parses the CHTML component from the given file. Unlike Parse, it may also watch
// for changes in the file and trigger a re-parse when necessary.
func ParseFile(fsys fs.FS, fname string, imp Importer) (Component, error) {
	f, err := fsys.Open(fname)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrComponentNotFound
		}
		return nil, fmt.Errorf("open component %s: %w", fname, err)
	}
	defer func() { _ = f.Close() }()

	c, err := parse(f, imp)
	if err != nil {
		return nil, err
	}

	c.fsys = fsys
	c.fname = fname

	return c, nil
}

// parseArgs parses the <c:arg> element recursively and builds a map of values for the
// component's arguments.
func (c *chtmlComponent) parseArgs(el *etree.Element, args map[string]any) error {
	name := el.SelectAttrValue("name", "")

	if name == "" {
		return errors.New("missing name attribute in <c:arg> element")
	}

	args[name] = nil

	for _, child := range el.Child {
		switch el := child.(type) {
		case *etree.Element:
			if el.FullTag() == "c:arg" {
				if args[name] == nil {
					args[name] = map[string]any{}
				}
				if _, ok := args[name].(map[string]any); !ok {
					return fmt.Errorf("argument %q is already defined", name)
				}
				nestedArgs := args[name].(map[string]any)
				if err := c.parseArgs(el, nestedArgs); err != nil {
					return err // TODO: provide trace
				}
				args[name] = nestedArgs
			} else {
				if args[name] == nil {
					args[name] = &html.Node{
						Type: html.DocumentNode,
					}
				}
				if _, ok := args[name].(*html.Node); !ok {
					return fmt.Errorf("argument %q is already defined", name)
				}
				return fmt.Errorf("html args are not implemented")
			}
		case *etree.CharData:
			if el.IsWhitespace() {
				continue
			}
			// either text or html
			prog, err := Interpol(el.Data, args)
			if err != nil {
				return fmt.Errorf("parse arg expression: %w", err) // TODO: provide trace
			}
			if prog != nil {
				res, err := c.vm.Run(prog, env(args))
				if err != nil {
					return fmt.Errorf("run arg expression: %w", err) // TODO: provide trace
				}
				args[name] = res
			} else {
				args[name] = el.Data
			}
		}
	}

	return nil
}

// parseElement recursively parses the given node and its children, storing extra information about
// the node in the component's meta map.
//
// Parsing rules:
// - whitespace text nodes are skipped
// - <c:NAME> is a component import
// - node attributes are parsed with parseAttrs()
// - text nodes are parsed as expressions if they contain ${...} syntax
func (c *chtmlComponent) parseElement(el *etree.Element, pc *parseContext) error {
	if el.Space == "c" { // TODO: add extra check for namespace URI
		if c.importer == nil {
			return errors.New("imports are not allowed")
		}
		compName := el.Tag
		comp, err := c.importer.Import(compName)
		if err != nil {
			return fmt.Errorf("import %s: %w", compName, err)
		}
		c.meta[el] = &nodeMeta{imprt: comp}
	}
	if err := c.parseAttrs(el, pc); err != nil {
		return err
	}
	nm := c.meta[el]
	if nm != nil && nm.loop != nil {
		pc = pc.clone()
		pc.args[nm.loopVar] = new(any)
		if nm.loopIdx != "" {
			pc.args[nm.loopIdx] = 0
		}
	}

	for _, child := range el.Child {
		switch n := child.(type) {
		case *etree.Element:
			if err := c.parseElement(n, pc); err != nil {
				return err // TODO: provide trace
			}
		case *etree.CharData:
			if err := c.parseText(n, pc); err != nil {
				return err // TODO: provide trace
			}
		default:
			// remove any non-element and non-text nodes
			el.RemoveChildAt(n.Index())
		}
	}
	return nil
}

func (c *chtmlComponent) parseText(n *etree.CharData, pc *parseContext) error {
	p, err := Interpol(n.Data, pc.args)
	if err != nil {
		return err
	}
	if p != nil {
		c.meta[n] = &nodeMeta{text: p}
	}
	return nil
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
func (c *chtmlComponent) parseAttrs(el *etree.Element, pc *parseContext) error {
	specialAttrs := map[string]string{}
	hasAttr := func(key string) bool {
		_, ok := specialAttrs[key]
		return ok
	}

	if len(el.Attr) > 0 {
		if c.meta[el] == nil {
			c.meta[el] = &nodeMeta{}
		}
		if c.meta[el].attrs == nil {
			c.meta[el].attrs = make([]nodeAttr, 0, len(el.Attr))
		}
	}

	for _, attr := range el.Attr {
		switch attr.FullKey() {
		case "c:if", "c:else-if", "c:else", "c:for":
			if _, ok := specialAttrs[attr.FullKey()]; ok {
				return fmt.Errorf("cannot use %s twice on the same element", attr.FullKey())
			}
			specialAttrs[attr.FullKey()] = attr.Value
		default:
			na := nodeAttr{key: attr.FullKey()}
			p, err := Interpol(attr.Value, pc.args)
			if err != nil {
				return err
			}
			if p != nil {
				na.prog = p
			} else {
				na.val = attr.Value
			}
			c.meta[el].attrs = append(c.meta[el].attrs, na)
		}
	}
	if hasAttr("c:if") && hasAttr("c:else-if") {
		return errors.New("cannot use c:if with c:else-if on the same element")
	}
	if hasAttr("c:if") && hasAttr("c:else") {
		return errors.New("cannot use c:if with c:else on the same element")
	}
	if hasAttr("c:else-if") && hasAttr("c:else") {
		return errors.New("cannot use c:else-if with c:else on the same element")
	}
	if specialAttrs["c:else"] != "" && specialAttrs["c:else"] != "else" {
		return errors.New("unexpected value for c:else")
	}

	if _, ok := specialAttrs["c:if"]; ok {
		prog, err := expr.Compile(specialAttrs["c:if"], expr.Optimize(true), expr.AsBool())
		if err != nil {
			return err // TODO: provide trace
		}
		c.meta[el].cond = prog
	} else if _, ok := specialAttrs["c:else-if"]; ok {
		prog, err := expr.Compile(specialAttrs["c:else-if"], expr.Optimize(true), expr.AsBool())
		if err != nil {
			return err // TODO: provide trace
		}
		c.meta[el].cond = prog
		if prevCond := c.findPrevCond(el); prevCond != nil {
			c.meta[prevCond].nextCond = el
		} else {
			return errors.New("c:else-if must be used after c:if")
		}
	} else if _, ok := specialAttrs["c:else"]; ok {
		prog, err := expr.Compile("true")
		if err != nil {
			return err // TODO: provide trace
		}
		c.meta[el].cond = prog
		if prevCond := c.findPrevCond(el); prevCond != nil {
			c.meta[prevCond].nextCond = el
		} else {
			return errors.New("c:else must be used after c:if or c:else-if")
		}
	}
	if _, ok := specialAttrs["c:for"]; ok {
		v, k, ex, err := parseLoopExpr(specialAttrs["c:for"])
		if err != nil {
			return err
		}
		prog, err := expr.Compile(ex, expr.Optimize(true))
		if err != nil {
			return err // TODO: provide trace
		}
		c.meta[el].loop = prog
		c.meta[el].loopVar = v
		c.meta[el].loopIdx = k
	}
	return nil
}

// Execute builds a new HTML document by evaluating the expressions in the component's tree and
// stores the result in the scope's "_" variable.
func (c *chtmlComponent) Execute(ctx context.Context, s Scope) error {
	newDoc := &html.Node{
		Type: html.DocumentNode,
	}

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
			vars[k] = v
		}
	}

	for _, child := range c.doc.Root().Child {
		if err := c.eval(ctx, newDoc, child, es); err != nil {
			return err
		}
	}

	s.Vars()["$html"] = newDoc

	return nil
}

// evalIf evaluates the conditional expression (c:if, c:else-if, c:else) for the given node and
// marks it as hidden if the condition is false.
func (c *chtmlComponent) evalIf(ctx context.Context, dst *html.Node, src *etree.Element, es *evalScope) error {
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
func (c *chtmlComponent) evalAttrs(dst *html.Node, src *etree.Element, es *evalScope) error {
	nm := c.meta[src]
	if nm == nil || nm.attrs == nil {
		return nil
	}
	attrs := make([]html.Attribute, len(nm.attrs))
	for i, attr := range nm.attrs {
		if attr.prog != nil {
			res, err := c.vm.Run(attr.prog, env(es.Vars()))
			if err != nil {
				return err // TODO: provide trace
			}
			s, ok := res.(string)
			if res != nil && !ok {
				return errors.New("attribute expression must return string")
			}
			attrs[i] = html.Attribute{Key: attr.key, Val: s}
		} else {
			attrs[i] = html.Attribute{Key: attr.key, Val: attr.val}
		}
	}
	dst.Attr = nil
	if len(attrs) > 0 {
		dst.Attr = attrs
	}
	return nil
}

// evalFor evaluates the loop expression (c:for) for the given node and appends the result to the
// destination node.
func (c *chtmlComponent) evalFor(ctx context.Context, dst *html.Node, src *etree.Element, es *evalScope) error {
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
		if err := c.eval(ctx, dst, src, subScope); err != nil {
			return err
		}
	}

	es.closeChild(src, v.Len()) // close remaining scopes

	es.hidden[src] = struct{}{}
	return nil
}

// evalImport renders the imported component (<c:NAME>) and appends the result to the destination.
func (c *chtmlComponent) evalImport(ctx context.Context, dst *html.Node, src *etree.Element, es *evalScope) error {
	nm := c.meta[src]

	if nm == nil || nm.imprt == nil {
		return nil
	}

	// build list of arguments for the child component
	args := make(map[string]any)
	for _, attr := range nm.attrs {
		if attr.prog != nil {
			res, err := c.vm.Run(attr.prog, env(es.Vars()))
			if err != nil {
				return err // TODO: provide trace
			}
			args[attr.key] = res
		} else {
			args[attr.key] = attr.val
		}
	}

	nonameArg := "_"

	// add extra arguments based on child <c:arg> elements
	// It is assumed that the dst tree has already been evaluated, thus the values there are
	// ready to become arguments for the imported component.
	for child := dst.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == "c:arg" {
			if err := c.parseArgs(src, args); err != nil {
				return err // TODO: provide trace
			}
		} else {
			// skip TextNodes with whitespace
			if child.Type == html.TextNode && strings.TrimSpace(child.Data) == "" {
				continue
			}

			var sb strings.Builder
			if err := html.Render(&sb, child); err != nil {
				return err
			}
			if _, ok := args[nonameArg]; !ok {
				args[nonameArg] = &html.Node{
					Type: html.DocumentNode,
				}
			}
			if doc, ok := args[nonameArg].(*html.Node); !ok {
				return fmt.Errorf("import: argument %q is already defined", nonameArg)
			} else {
				doc.AppendChild(&html.Node{
					Type: html.RawNode,
					Data: sb.String(),
				})
			}
		}
	}

	// retrieve the scope for the imported component or create a new one
	cs := es.spawn(args, src, 0)

	// make the scope isolated (keep only vars matching the component's arguments)
	vars := cs.Vars()
	for k, v := range args {
		if _, ok := vars[k]; !ok {
			vars[k] = v
		}
	}

	if err := nm.imprt.Execute(ctx, cs); err != nil {
		return err
	}

	v := cs.Vars()["$html"]
	if doc, ok := v.(*html.Node); ok {
		*dst = *doc // replace <c:NAME> with the imported document
	} else {
		// if the imported component does not return HTML, replace it with an empty node
		dst.Type = html.RawNode
		dst.Data = ""
	}

	return nil
}

// evalText evaluates the interpolated expression for the given text node and stores the result in
// the destination node.
// If the text node is not an expression, no action is taken.
func (c *chtmlComponent) evalText(dst *html.Node, src *etree.CharData, es *evalScope) error {
	nm := c.meta[src]
	if nm == nil || nm.text == nil {
		return nil
	}
	res, err := c.vm.Run(nm.text, env(es.Vars()))
	if err != nil {
		return err
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

// evalElement evaluates all expressions in conditionals, loops, child nodes, imports for the source node
// tree and clones relevant nodes to the destination tree.
//
// The evaluation process is performed in the following order:
// 1. conditionals (c:if, c:else-if, c:else)
// 2. loops (c:for)
// 3. attributes
// 4. child nodes
// 5. imports (<c:NAME>)
func (c *chtmlComponent) evalElement(ctx context.Context, dst *html.Node, src *etree.Element, es *evalScope) error {
	if _, ok := es.hidden[src]; ok {
		return nil
	}
	if err := c.evalIf(ctx, dst, src, es); err != nil {
		return err
	}
	if _, ok := es.hidden[src]; ok {
		return nil
	}

	if err := c.evalFor(ctx, dst, src, es); err != nil {
		return err
	}
	if _, ok := es.hidden[src]; ok {
		return nil
	}

	clone := &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Lookup([]byte(src.FullTag())),
		Data:     src.FullTag(),
	}
	if err := c.evalAttrs(clone, src, es); err != nil {
		return err
	}

	for _, child := range src.Child {
		if err := c.eval(ctx, clone, child, es); err != nil {
			return err
		}
	}

	if err := c.evalImport(ctx, clone, src, es); err != nil {
		return err
	}

	dst.AppendChild(clone)
	return nil
}

func (c *chtmlComponent) eval(ctx context.Context, dst *html.Node, src etree.Token, es *evalScope) error {
	switch n := src.(type) {
	case *etree.Element:
		return c.evalElement(ctx, dst, n, es)
	case *etree.CharData:
		return c.evalText(dst, n, es)
	}
	return nil
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
