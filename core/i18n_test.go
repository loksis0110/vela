package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

var formatVerb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

// TestTranslations checks that every Russian message of the core goes
// through T/errorf, has an English translation with the same format verbs,
// and that the dictionary has no stale entries.
func TestTranslations(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	used := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "i18n.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		translated := map[*ast.BasicLit]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && (id.Name == "T" || id.Name == "errorf") {
				if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					translated[lit] = true
				}
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil || !hasCyrillic(s) {
				return true
			}
			pos := fset.Position(lit.Pos())
			if !translated[lit] {
				t.Errorf("%s: Russian text not passed to T/errorf: %q", pos, s)
				return true
			}
			used[s] = true
			en, ok := english[s]
			if !ok {
				t.Errorf("%s: no English translation for %q", pos, s)
				return true
			}
			if hasCyrillic(en) {
				t.Errorf("translation of %q is not English: %q", s, en)
			}
			if a, b := formatVerb.FindAllString(s, -1), formatVerb.FindAllString(en, -1); strings.Join(a, " ") != strings.Join(b, " ") {
				t.Errorf("format verbs differ: %q %v vs %q %v", s, a, en, b)
			}
			return true
		})
	}
	for ru := range english {
		if !used[ru] {
			t.Errorf("unused translation: %q", ru)
		}
	}
}
