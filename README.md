# go-pages

`go-pages` is a server-side HTML component rendering engine designed to bring the ease and
flexibility of Web component development to Go projects.

It consists of a template engine for rendering HTML components (`.chtml` files) and a file-based
router for serving components based on URL paths.

**Goals**

- [x] Template engine supports composable and reusable components.
- [x] Template language takes cues from VueJS and AlpineJS, incorporating `for/if` directives
  within HTML tags.
- [x] No JavaScript required.
- [x] No code generation required, though a Go code generator can be developed for improved performance.
- [x] Implements file-based routing akin to [NuxtJS](https://v2.nuxt.com/).
- [x] Single-file components (combine HTML, CSS, and JS in a single file).
- [ ] Not tied to Go ecosystem, allowing components to be reused in non-Go projects.
- [x] Plays nicely with [HTMX](https://htmx.org/) and [AlpineJS](https://alpinejs.dev/).
- [x] Template fragments for partial rendering of components.
- [ ] Automatic browser refresh during development.
- [x] Small API surface (both on the template language and the Go API) for quick starts.
- [x] Ability to embed assets into a single binary.

**Why not `html/template`, `jet`, `pongo2`, or others?**

Templates are harder to compose and reuse across projects compared to more modular components
approach.

**Why not [templ](https://github.com/a-h/templ) or [gomponents](https://github.com/maragudk/gomponents)?**

These options lock you into the Go ecosystem, limiting component reuse in non-Go projects.

## Installation

```bash
go get -u github.com/dpotapov/go-pages
```

## Example Usage

1. Create a directory for your pages and components. For example, `./pages`.
2. Create a file in the `./pages` directory. For example, `./pages/index.chtml` with the following
   content:

   ```html
   <h1>Hello World</h1>
   ```

3. Create a Go program to serve the pages.

    ```go
    package main

    import (
        "net/http"
        "os"

        "github.com/dpotapov/go-pages"
    )

    func main() {
        ph := &pages.Handler{
            FileSystem: os.DirFS("./pages"),
        }

        http.ListenAndServe(":8080", ph)
    }
    ```

4. Run the program and navigate to `http://localhost:8080`. You should see the text "Hello World".
5. Create another file in the `./pages` directory. For example, `./pages/about.chtml` with the
   following content:

   ```html
   <h1>About page</h1>
   ```

6. Navigate to `http://localhost:8080/about`. You should see the text "About page". No need to
   restart the server.

Check out the [example](./example) directory for a more complete example.

## CHTML Tags and Attributes

Components are defined in `.chtml` files in HTML5-like syntax.

On-top of a standard HTML, `go-pages` adds the following elements and attributes prefixed
with `c:` namespace:

- `<c:NAME>...</c:NAME>` imports a component by name.
  The body of the element is passed to the component as an argument and can be interpolated
  with `${_}` syntax.
  Any attributes on the element are passed to the component as arguments as well.
  Typically, the component is a `.chtml` file, but it can also be a virtual component defined in Go code.

- `<c:attr name="ATTR_NAME">VALUE</c:attr>` — builtin that applies the attribute `ATTR_NAME`
  with value `VALUE` to the parent element/component. It:
  - must be nested inside an element or component (top-level usage is ignored)
  - supports string interpolation in `VALUE`
  - has no side effects beyond setting the attribute (no env/scope mutation)
  Examples:
  ```html
  <a><c:attr name="href">${link}</c:attr>Open</a>
  <!-- Renders: <a href="/path">Open</a> -->

  <c:MyButton>
    <c:attr name="variant">primary</c:attr>
    Click me
  </c:MyButton>
  ```

- `c:if`, `c:else-if`, `c:else` attribute for conditional rendering.

- `c:for` attribute for iterating over a slice or a map.

All `c:` elements and attributes are removed from the final HTML output.

### Special `<c>` element

`<c>...</c>` is a control-only container that does not render its own tag. It can:

- passthrough children as-is:
  ```html
  <c><p>Hello</p></c>
  <!-- Renders: <p>Hello</p> -->
  ```
- bind rendered content to a variable and suppress output via `var`:
  ```html
  <c var="paragraph"><p>Hello</p></c>
  <div>${paragraph}</div>
  <!-- Renders: <div><p>Hello</p></div> -->
  ```
- bind with automatic type casting:
  ```html
  <c var="myvar number">123</c>
  <c var="myobj { name: string, age: number }">${ {name: "John", age: 30} }</c>
  <p>${myobj.name} is ${myobj.age} years old</p>
  <!-- Content is automatically converted to the specified type -->
  ```
- loop with `for`:
  ```html
  <c for="i in items"><li>${i}</li></c>
  ```
- conditionally render with `if` / `else-if` / `else`:
  ```html
  <c if="cond">A</c><c else-if="other">B</c><c else>C</c>
  ```

**Type Annotations (Shapes)**

`go-pages` supports type annotations using a shape syntax. Shapes can be used for:
1. **Variable declarations** with type casting (`<c var="name SHAPE">`)
2. **Inline expressions** with the `cast()` function (`cast(value, SHAPE)`)
3. **Conditionals** with type matching (`c:if="EXPR is SHAPE"`)

*Supported shapes:*

| Shape             | Description                    | Example                                 |
|-------------------|--------------------------------|-----------------------------------------|
| `string`          | String value                   | `"hello"`                               |
| `number`          | Integer or float               | `42`, `3.14`                            |
| `bool`            | Boolean                        | `true`, `false`                         |
| `html`            | HTML node                      | `<p>text</p>`                           |
| `any`             | Any type                       | —                                       |
| `[T]`             | Array of type T                | `[string]`, `[{name: string}]`          |
| `{field: T, ...}` | Object with typed fields       | `{name: string, age: number}`           |
| `{_: T}`          | Object with uniform value type | `{_: string}` (any keys, string values) |

*Type Casting in Variables*

The `var` attribute supports type casting with the syntax `var="name SHAPE"`:

```html
<c var="count number">42</c>
<c var="user {name: string, age: number}">${ {name: "John", age: 30} }</c>
<p>${user.name} is ${user.age} years old</p>
```

Uniform objects (`{_: T}`) are useful when keys are dynamic but values share a type:

```html
<c var="labels {_: string}">${ {a: "Alpha", b: "Beta"} }</c>
<p>${labels.a} / ${labels.b}</p>
```

If the value cannot be converted to the specified shape, a runtime error is thrown.

*The `cast()` Function*

Use `cast(value, SHAPE)` in expressions to validate and cast values inline:

```html
<!-- Cast in attribute -->
<c:my-component items="${ cast(data, [string]) }" />

<!-- Cast in loop -->
<c for="item in cast(items, [{name: string}])">
  <p>${ item.name }</p>
</c>

<!-- Cast with nested shape -->
<p>${ cast(response, {user: {name: string}}).user.name }</p>
```

The function validates that the value matches the shape and returns it unchanged. If validation fails, a runtime error is thrown.

*Type Matching in Conditionals*

Conditionals support type matching with the syntax `EXPR is SHAPE`:

```html
<!-- Basic type matching -->
<p c:if="value is string">${ value }</p>
<p c:else-if="value is number">Number: ${ value }</p>
<p c:else>Unknown type</p>

<!-- Object shape matching -->
<div c:if="response is {success: bool, data: string}">
  Success: ${ response.data }
</div>
```

Use `as IDENT` to bind the matched value to a new variable:

```html
<c if="result is {user: {name: string}} as r">
  <p>User: ${ r.user.name }</p>
</c>
<c else>
  <p>No user data</p>
</c>
```

The `as IDENT` part is optional:
- If omitted and EXPR is a simple identifier (e.g., `data`), that name is reused
- If omitted and EXPR is complex (e.g., `obj.field`), no variable is bound
- If provided, the matched value is bound to the specified name

The condition evaluates to `true` only if the value is not `nil` and structurally matches the shape.

Constraints and notes:
- Do not mix `for` with `if`/`else-if`/`else` on the same `<c>`.
- Do not use `c:*` directives on `<c>`; use plain attributes `if`, `else-if`, `else`, `for`.
- `<c>` with no children produces no output.

**Kebab-case conversion**

The `go-pages` library does not enforce a style for naming components and arguments, you may
choose between CamelCase/camelCase or kebab-style, single word or multiple words. Just don't use
underscores as they feel wrong in HTML and URL paths.

You may want to use kebab-case for components, that represent pages (and become part of
the URL that visible to the user). When referencing a component or an argument in an expression,
all dashes will be replaced with underscores.

Example:

```html
<!--
  kebab-style - preferred for URLs and native to HTML.
  The go-pages engine will automatically replace dashes to underscore_case to make easier to use
  in expressions. E.g. some-arg-1 becomes ${some_arg_1}.
 -->

<c:my-component some-arg-1="...">
   ...
</c:my-component>

<!--
  CamelCase for component names and camelCase for attributes.
  This style is easier to use in expressions. E.g. someArg1 can be referenced as ${someArg1}.
 -->
<c:MyComponent someArg1="...">
   ...
</c:MyComponent>
```

**Expressions**

Currently, `go-pages` uses the `https://github.com/expr-lang/expr` library for evaluating
expressions. Refer https://expr-lang.org/ for the syntax.

**String Interpolation**

String interpolation is supported using the `${ ... }` syntax. For example:

```html
<a href="${link}">Hello, ${name}!</a>
```

All string attributes and text nodes are interpolated.

## Template Fragments

Template fragments allow rendering just a portion of a component's HTML. This is particularly useful for HTMX-based applications where you want to update only specific parts of a page.

To define a fragment in your template, simply add an `id` attribute to the HTML element that you want to use as a fragment:

```html
<div id="user-profile">
  <h2>${user.name}</h2>
  <p>${user.bio}</p>
</div>

<div id="user-stats">
  <h3>Stats</h3>
  <ul>
    <li>Posts: ${user.posts_count}</li>
    <li>Followers: ${user.followers_count}</li>
  </ul>
</div>
```

### Fragment Selection

To render only a specific fragment, you can use the `FragmentSelector` option in the `Handler`:

```go
h := &pages.Handler{
    FileSystem: os.DirFS("./pages"),
    FragmentSelector: func(r *http.Request) string {
        return r.URL.Query().Get("fragment")
    },
}
```

This example renders only the fragment specified in the `fragment` query parameter. For example, requesting `/user?fragment=user-stats` would render only the `user-stats` div.

### HTMX Integration

For HTMX applications, you can use the built-in `HTMXFragmentSelector` function to automatically handle the `HX-Target` header:

```go
h := &pages.Handler{
    FileSystem: os.DirFS("./pages"),
    FragmentSelector: pages.HTMXFragmentSelector,
}
```

With this setup, HTMX requests using `hx-target` will automatically render only the targeted fragment:

```html
<button hx-get="/user" hx-target="#user-stats">Refresh Stats</button>
```

When this button is clicked, only the `user-stats` fragment will be rendered and returned to the browser, which HTMX will then use to update just that part of the page.

## File-based Routing

`go-pages` handler implements a file-based routing system. The path from the request URL is used to
find the corresponding `.chtml` file.

For example, if the request URL is `/about`, the handler will look for the `about.chtml` file in the
directory. The handler will also look for an `index.chtml` file if the request URL ends with `/`.

If the request URL points to a static file (e.g. `/css/style.css`), the handler will serve the
file if it exists in the file system.

**Dynamic routes**

Directories or component files can be prefixed with an underscore to indicate a dynamic route.
Double underscore `__` in the component filename is used to indicate a catch-all route.

Examples:

```
/_user
    /index.chtml    -> matches URL: /joe/
    /profile.chtml  -> matches URL: /joe/profile
/posts
    /index.chtml    -> matches URL: /posts/
    /_slug.chtml    -> matches URL: /posts/hello-world
/__path.chtml       -> matches URL: /anything/else
```

The router will pass the dynamic part of the URL as an argument to the component. For example, the
`/posts/_slug.chtml` component will receive the `slug` argument with the value `hello-world`.

Catch-all component in the root directory of the file system can be used to implement a 404 page.

Each top-level page component receives a `pages.RequestArg` object as an `request` argument.
Here is an example of the data it contains:

```yaml
# HTTP method, string: GET, POST, etc.
method: "GET"

# Full URL
url: "http://localhost:8080/posts/hello-world?foo=bar&foo=baz"

# URL scheme
scheme: "http"

# Hostname part of the URL
host: "localhost"

# Port part of the URL
port: "8080"

# Path part of the URL
path: "/posts/hello-world"

# URL Query parameters, represented as a map of string slices.
query:
  foo: ["bar", "baz"]

# Remote address of the client, string: "IPv4:PORT" or "IPv6:PORT"
remote_addr: "127.0.0.1:12345"

# HTTP headers, represented as a map of string slices.
headers:
  Content-Type: ["application/json"]
  Cookie: ["session_id=1234567890", "user_id=123"]

# Body is available only when the content type is either application/json or
# application/x-www-form-urlencoded and the body size is less than xxx MB.
# TODO: define the size limit.
body:
  foo: "bar"
  bar: "baz"

# raw_body is only available when the content type is not application/json or
# application/x-www-form-urlencoded, or body size exceeds xxx MB limit.
# It is meant to be passed to custom components with Go renderers.
raw_body: nil
```
