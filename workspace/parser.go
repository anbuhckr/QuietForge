package workspace

import (
	"context"
	"path/filepath"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

func GetLanguage(ext string) *sitter.Language {
	switch ext {
	case ".py":
		return python.GetLanguage()
	case ".js", ".jsx":
		return javascript.GetLanguage()
	case ".ts":
		return typescript.GetLanguage()
	case ".tsx":
		return tsx.GetLanguage()
	case ".go":
		return golang.GetLanguage()
	case ".rs":
		return rust.GetLanguage()
	case ".java":
		return java.GetLanguage()
	case ".c", ".h":
		return c.GetLanguage()
	case ".cpp", ".hpp":
		return cpp.GetLanguage()
	}
	return nil
}

func ParseAST(path string, data []byte) (*sitter.Tree, error) {
	lang := GetLanguage(filepath.Ext(path))
	if lang == nil {
		return nil, nil // Unsupported
	}
	parser := sitter.NewParser()
	parser.SetLanguage(lang)
	return parser.ParseCtx(context.Background(), nil, data)
}
