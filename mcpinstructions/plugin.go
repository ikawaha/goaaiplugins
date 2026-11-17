package mcpinstructions

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	mcpcodegen "goa.design/goa-ai/codegen/mcp"
	"goa.design/goa/v3/codegen"
	"goa.design/goa/v3/codegen/generator"
	"goa.design/goa/v3/eval"
	"goa.design/goa/v3/expr"
)

// plugin adds the declared instructions to the code that goa-ai generates for
// one run of goa gen.
type plugin struct {
	// texts maps the name of each authored service to the instructions of its
	// MCP server.
	texts map[string]string
}

const (
	// discoverMethod is the protocol method whose result carries the
	// instructions.
	discoverMethod = "server/discover"
	// adapterSection is the section of goa-ai that renders ServerDiscover.
	adapterSection = "mcp-adapter-core"
	// discoverReturn starts the result literal in the template of
	// adapterSection.
	discoverReturn = "    return &DiscoverResult{\n"
)

func init() {
	// The MCP plugin of goa-ai is registered first, so this plugin sees the
	// MCP services it adds and the files it generates.
	generator.RegisterPlugin("mcp-instructions", "gen", newPlugin)
}

// newPlugin returns the plugin for one run.
func newPlugin() generator.Plugin {
	p := &plugin{texts: make(map[string]string)}
	return generator.Plugin{Prepare: p.prepare, Generate: p.generate}
}

// discoverObjects returns the objects that carry the server/discover result of
// the MCP service that goa-ai added for svc: the result type, and the JSON-RPC
// response bodies that Goa copied from it when goa-ai evaluated the service.
// goa-ai v0.90.0 names that service mcp_<service> and shares the result type
// between the MCP services of a design.
func discoverObjects(designs []*expr.RootExpr, svc *expr.ServiceExpr) ([]*expr.Object, error) {
	for _, design := range designs {
		if !slices.Contains(design.Services, svc) {
			continue
		}
		name := "mcp_" + svc.Name
		service := design.Service(name)
		if service == nil {
			return nil, fmt.Errorf("goa-ai added no MCP service %q for service %q", name, svc.Name)
		}
		method := service.Method(discoverMethod)
		if method == nil {
			return nil, fmt.Errorf("MCP service %q has no %s method", name, discoverMethod)
		}
		result := expr.AsObject(method.Result.Type)
		if result == nil {
			return nil, fmt.Errorf("the %s result of MCP service %q is not an object", discoverMethod, name)
		}
		transport := design.API.JSONRPC.Service(name)
		if transport == nil {
			return nil, fmt.Errorf("MCP service %q has no JSON-RPC transport", name)
		}
		endpoint := transport.Endpoint(discoverMethod)
		if endpoint == nil {
			return nil, fmt.Errorf("MCP service %q has no JSON-RPC endpoint %s", name, discoverMethod)
		}
		objects := []*expr.Object{result}
		for _, resp := range endpoint.Responses {
			body := expr.AsObject(resp.Body.Type)
			if body == nil {
				return nil, fmt.Errorf("the %s response body of MCP service %q is not an object", discoverMethod, name)
			}
			objects = append(objects, body)
		}
		return objects, nil
	}
	return nil, fmt.Errorf("service %q is not in the design", svc.Name)
}

// addInstructions declares text before the result literal of ServerDiscover in
// source, the template of adapterSection, and sets the field from it. The text
// is a Go string literal whose "{{" is written by an action, so that the
// template does not parse it.
func addInstructions(source, text string) (string, error) {
	if n := strings.Count(source, discoverReturn); n != 1 {
		return "", fmt.Errorf("the template has %d result literals of ServerDiscover, want 1", n)
	}
	literal := strings.ReplaceAll(strconv.Quote(text), "{{", `{{ "{{" }}`)
	return strings.Replace(source, discoverReturn, "    instructions := "+literal+"\n"+discoverReturn+"        Instructions: &instructions,\n", 1), nil
}

// prepare adds the instructions field to the server/discover result, and to its
// response bodies, of the MCP services that goa-ai added for servers with
// instructions.
func (p *plugin) prepare(_ string, roots []eval.Root) error {
	var root *RootExpr
	var designs []*expr.RootExpr
	for _, r := range roots {
		switch r := r.(type) {
		case *RootExpr:
			root = r
		case *expr.RootExpr:
			designs = append(designs, r)
		}
	}
	if root == nil {
		return fmt.Errorf("the root of the MCP instructions plugin is not registered")
	}
	added := make(map[*expr.Object]bool)
	for _, e := range root.sorted() {
		objects, err := discoverObjects(designs, e.MCP.Service)
		if err != nil {
			return err
		}
		for _, fields := range objects {
			switch {
			case added[fields]:
			case fields.Attribute("instructions") != nil:
				return fmt.Errorf("the %s result of MCP server %q already has instructions: declare them with goa-ai and remove mcpinstructions", discoverMethod, e.MCP.Name)
			default:
				*fields = append(*fields, &expr.NamedAttributeExpr{
					Name: "instructions",
					Attribute: &expr.AttributeExpr{
						Type:        expr.String,
						Description: "Guidance on using this server, which clients may add to the system prompt of the model",
					},
				})
				added[fields] = true
			}
		}
		p.texts[e.MCP.Service.Name] = e.Text
	}
	return nil
}

// generate writes the instructions into ServerDiscover of the MCP adapter of
// each server that declares them.
func (p *plugin) generate(_ *generator.Plan, files []*codegen.File) ([]*codegen.File, error) {
	written := make(map[string]bool, len(p.texts))
	for _, f := range files {
		for _, s := range f.SectionTemplates {
			if s.Name != adapterSection {
				continue
			}
			data, ok := s.Data.(*mcpcodegen.AdapterData)
			if !ok {
				return nil, fmt.Errorf("section %s of %s has data of type %T, not goa-ai adapter data", adapterSection, f.Path, s.Data)
			}
			text, ok := p.texts[data.ServiceName]
			if !ok {
				continue
			}
			source, err := addInstructions(s.Source, text)
			if err != nil {
				return nil, fmt.Errorf("MCP adapter of service %q: %w", data.ServiceName, err)
			}
			s.Source = source
			written[data.ServiceName] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(p.texts)) {
		if !written[name] {
			return nil, fmt.Errorf("goa-ai generated no MCP adapter for service %q", name)
		}
	}
	return files, nil
}
