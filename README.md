# goaaiplugins

Plugins for [Goa](https://goa.design) designs that use [goa-ai](https://github.com/goadesign/goa-ai).
They fill gaps in what goa-ai generates until goa-ai covers them itself.

| Package | What it adds |
|---|---|
| [`mcpinstructions`](#mcpinstructions) | The `instructions` that an MCP server returns |

## mcpinstructions

An MCP server may return `instructions` when a client connects: guidance on
what the server is for and when to use its tools. Clients such as Claude Code
add them to the system prompt of the model, so the model sees them before it
loads any tool and reads its description. goa-ai v0.90.0 has no way to declare
them; this plugin adds one.

### Usage

Call `mcpinstructions.Instructions` inside the design function of `MCP`:

```go
package design

import (
	. "goa.design/goa-ai/dsl"
	. "goa.design/goa/v3/dsl"

	"github.com/ikawaha/goaaiplugins/mcpinstructions"
)

var _ = Service("notes", func() {
	MCP("notes", "1.0.0", func() {
		mcpinstructions.Instructions(`The notes hold the user's work logs and past decisions.
Search them with search_notes before answering a question that may depend on them.`)
	})
	JSONRPC(func() { POST("/mcp") })

	// methods and their tools ...
})
```

Then run `goa gen` as usual. Importing the package registers the plugin.

The text should tell what the server holds and when to use it, and not repeat
the tool descriptions. It is written into the generated code as is, so it does
not change at run time.

### What it generates

- An optional `instructions` field in the `server/discover` result of the MCP
  service that goa-ai adds, and in its JSON-RPC response body.
- The declared text in the generated `ServerDiscover` of the MCP adapter.

The `initialize` result of the older protocol (2025-11-25) embeds the same
body, so clients that use either protocol receive the instructions.

### Compatibility

Written for goa-ai v0.90.0. The plugin relies on two details of goa-ai: the
name of the MCP service it adds (`mcp_<service>`), and the result literal
`return &DiscoverResult{` in the template of its `mcp-adapter-core` section.
If either changes, `goa gen` stops with an error instead of generating a server
without the instructions. It also stops if goa-ai already generates an
`instructions` field: then declare them with goa-ai and remove this plugin.

`Instructions` may appear once per MCP server, inside `MCP` only, with a text
that is not empty.

## License

[MIT](LICENSE)
