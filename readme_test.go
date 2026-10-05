package koldera

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const modulePath = "github.com/koldera/koldera-go"

// readmeBlocks returns the contents of the ```go fences of a Markdown file with the line each starts on.
func readmeBlocks(markdown string) (blocks []string, lines []int) {
	var current []string
	in := false
	start := 0
	for i, line := range strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n") {
		switch {
		case !in && strings.TrimSpace(line) == "```go":
			in, start, current = true, i+2, nil
		case in && strings.TrimSpace(line) == "```":
			in = false
			blocks = append(blocks, strings.Join(current, "\n"))
			lines = append(lines, start)
		case in:
			current = append(current, line)
		}
	}
	return blocks, lines
}

type selfImporter struct {
	std  types.Importer
	self *types.Package
}

func (s selfImporter) Import(path string) (*types.Package, error) {
	if path == modulePath {
		return s.self, nil
	}
	return s.std.Import(path)
}

// checkSnippets type-checks every snippet as a function body that sees the names the README prose introduces
// (client, ctx, jobID) and the imports the examples use.
func checkSnippets(t *testing.T, blocks []string, lines []int) []string {
	t.Helper()
	fset := token.NewFileSet()
	var files []*ast.File
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	std := importer.Default()
	self, err := (&types.Config{Importer: std}).Check(modulePath, fset, files, nil)
	if err != nil {
		t.Fatalf("the package does not type-check: %v", err)
	}

	var source strings.Builder
	source.WriteString("package snippets\n\nimport (\n\t\"context\"\n\t\"encoding/json\"\n\t\"errors\"\n\t\"fmt\"\n\t\"log\"\n\t\"os\"\n\t\"time\"\n\n\t\"" + modulePath + "\"\n)\n\n")
	source.WriteString("var _ = json.Marshal\nvar _ = errors.New\nvar _ = fmt.Sprint\nvar _ = log.Fatal\nvar _ = os.Open\nvar _ = time.Now\nvar _ context.Context\nvar _ koldera.Client\n\n")
	offsets := make([]int, len(blocks))
	text := source.String()
	row := strings.Count(text, "\n")
	for i, block := range blocks {
		head := fmt.Sprintf("func snippet%d(client *koldera.Client, ctx context.Context, jobID string) {\n\tvar err error\n\t_, _, _, _ = client, ctx, jobID, err\n\t{\n", i)
		text += head
		row += strings.Count(head, "\n")
		offsets[i] = row
		text += block + "\n\t}\n}\n\n"
		row += strings.Count(block, "\n") + 1 + 3
	}
	parsed, err := parser.ParseFile(fset, "readme_snippets.go", text, 0)
	if err != nil {
		t.Fatalf("a README snippet does not parse: %v", err)
	}
	var problems []string
	conf := types.Config{Importer: selfImporter{std: std, self: self}, Error: func(e error) {
		if terr, ok := e.(types.Error); ok {
			line := fset.Position(terr.Pos).Line
			for i := len(offsets) - 1; i >= 0; i-- {
				if line > offsets[i] {
					problems = append(problems, fmt.Sprintf("README.md block at line %d: %s", lines[i]+line-offsets[i]-1, terr.Msg))
					return
				}
			}
		}
		problems = append(problems, e.Error())
	}}
	_, _ = conf.Check("snippets", fset, []*ast.File{parsed}, nil)
	return problems
}

func TestReadmeSnippetsTypeCheck(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks, lines := readmeBlocks(string(data))
	if len(blocks) == 0 {
		t.Fatal("README.md has no go blocks")
	}
	for _, problem := range checkSnippets(t, blocks, lines) {
		t.Error(problem)
	}
}

func TestSnippetCheckRejectsNamesThatDoNotExist(t *testing.T) {
	problems := checkSnippets(t, []string{"_ = koldera.NoSuchOption(1)\nfmt.Println(client.NoSuchField)"}, []int{1})
	if len(problems) != 2 {
		t.Fatalf("want two problems, got %v", problems)
	}
}
