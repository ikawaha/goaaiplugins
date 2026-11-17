package mcpinstructions

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	. "goa.design/goa-ai/dsl"
	agentexpr "goa.design/goa-ai/expr/agent"
	mcpexpr "goa.design/goa-ai/expr/mcp"
	"goa.design/goa/v3/codegen/generator"
	. "goa.design/goa/v3/dsl"
	"goa.design/goa/v3/eval"
	"goa.design/goa/v3/expr"
)

func TestInstructions(t *testing.T) {
	for _, tc := range []struct {
		name string
		dsl  func()
		want string // the text declared for the server notes
		err  string // part of the error, or empty for none
	}{
		{
			name: "declared",
			dsl:  func() { service("notes", func() { Instructions("Search the notes first.") }) },
			want: "Search the notes first.",
		},
		{
			name: "empty",
			dsl:  func() { service("notes", func() { Instructions(" \n") }) },
			err:  `instructions of MCP server "notes" are empty`,
		},
		{
			name: "twice",
			dsl: func() {
				service("notes", func() {
					Instructions("a")
					Instructions("b")
				})
			},
			err: `instructions of MCP server "notes" are declared more than once`,
		},
		{
			name: "outside MCP",
			dsl: func() {
				Service("notes", func() {
					Instructions("a")
				})
			},
			err: "invalid use of",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runDSL(t, tc.dsl)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Errorf("error = %v, want one with %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, e := range Root.sorted() {
				got = append(got, e.MCP.Service.Name+": "+e.Text)
			}
			if want := []string{"notes: " + tc.want}; !slices.Equal(got, want) {
				t.Errorf("instructions = %q, want %q", got, want)
			}
		})
	}
}

// TestGenerate runs goa gen with the MCP plugin of goa-ai. The server notes
// declares instructions with a quote, a new line and the delimiter of a
// template action; the server plain declares none, while the two share the
// result type of server/discover.
func TestGenerate(t *testing.T) {
	const text = "Search the notes first.\nRead them with \"get_note\"; {{ is not an action."
	if err := runDSL(t, func() {
		service("notes", func() { Instructions(text) })
		service("plain")
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "off")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := generator.Generate(dir, "gen", false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path    string
		pattern string
		want    bool
	}{
		{"gen/mcp_notes/adapter_server.go", `instructions := ` + regexp.QuoteMeta(strconv.Quote(text)) + "\n", true},
		{"gen/mcp_notes/adapter_server.go", `Instructions:\s+&instructions,`, true},
		{"gen/mcp_plain/adapter_server.go", `instructions`, false},
		{"gen/mcp_notes/service.go", `Instructions \*string`, true},
		{"gen/mcp_plain/service.go", `Instructions \*string`, true},
		{"gen/jsonrpc/mcp_notes/server/types.go", `json:"instructions,omitempty"`, true},
	} {
		b, err := os.ReadFile(filepath.Join(dir, tc.path))
		if err != nil {
			t.Fatal(err)
		}
		if got := regexp.MustCompile(tc.pattern).Match(b); got != tc.want {
			t.Errorf("%s matches %q: %v, want %v", tc.path, tc.pattern, got, tc.want)
		}
	}
}

func TestAddInstructions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		text   string
		want   string
		err    bool
	}{
		{
			name:   "one literal",
			source: "a\n" + discoverReturn + "b\n",
			text:   `say "hi"`,
			want:   "a\n    instructions := \"say \\\"hi\\\"\"\n" + discoverReturn + "        Instructions: &instructions,\nb\n",
		},
		{
			name:   "action delimiter",
			source: discoverReturn,
			text:   "{{x}}",
			want:   `    instructions := "{{ "{{" }}x}}"` + "\n" + discoverReturn + "        Instructions: &instructions,\n",
		},
		{name: "no literal", source: "a\n", text: "x", err: true},
		{name: "two literals", source: discoverReturn + discoverReturn, text: "x", err: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := addInstructions(tc.source, tc.text)
			if tc.err {
				if err == nil {
					t.Errorf("no error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// runDSL evaluates dsl with fresh roots of Goa, goa-ai and this package. The
// agent plugin of goa-ai, which goa gen also runs, needs the agent root.
func runDSL(t *testing.T, dsl func()) error {
	t.Helper()
	eval.Reset()
	expr.Root = new(expr.RootExpr)
	expr.GeneratedResultTypes = new(expr.ResultTypesRoot)
	agentexpr.Root = new(agentexpr.RootExpr)
	mcpexpr.Root = mcpexpr.NewRoot()
	Root = NewRoot()
	for _, r := range []eval.Root{expr.Root, expr.GeneratedResultTypes, agentexpr.Root, mcpexpr.Root, Root} {
		if err := eval.Register(r); err != nil {
			t.Fatal(err)
		}
	}
	expr.Root.API = expr.NewAPIExpr("test", func() {})
	expr.Root.API.Servers = []*expr.ServerExpr{expr.Root.API.DefaultServer()}
	if !eval.Execute(dsl, nil) {
		return errors.New(eval.Context.Error())
	}
	return eval.RunDSL()
}

// service declares a service with an MCP server of the same name, which has
// one tool, and runs mcp in the design function of MCP.
func service(name string, mcp ...func()) {
	Service(name, func() {
		MCP(name, "1.0.0", mcp...)
		JSONRPC(func() { POST("/" + name) })
		Method("read", func() {
			Result(String)
			Tool("read", "Read one value")
			JSONRPC(func() {})
		})
	})
}
