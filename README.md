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
- [ ] Single-file components (combine HTML, CSS, and JS in a single file).
- [ ] Not tied to Go ecosystem, allowing components to be reused in non-Go projects.
- [ ] Plays nicely with [HTMX](https://htmx.org/) and [AlpineJS](https://alpinejs.dev/).
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

- `<c:attr name="ATTR_NAME">...</c:attr>` - is a builtin component that adds an attribute
  named `ATTR_NAME` to the parent element.

- `c:if`, `c:else-if`, `c:else` attribute for conditional rendering.

- `c:for` attribute for iterating over a slice or a map.

All `c:` elements and attributes are removed from the final HTML output.

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
