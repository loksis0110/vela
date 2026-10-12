package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// verbs lists the fmt verbs of a message ("%s", "%d", "%[2]d"...).
var verbRe = regexp.MustCompile(`%(\[\d+\])?[-+# 0]*\d*(\.\d+)?[a-zA-Z%]`)

func verbs(s string) []string {
	var out []string
	for _, v := range verbRe.FindAllString(s, -1) {
		if v == "%%" {
			continue
		}
		v = regexp.MustCompile(`^%\[\d+\]`).ReplaceAllString(v, "%")
		out = append(out, v[len(v)-1:])
	}
	sort.Strings(out)
	return out
}

// Every message passed to tr() has an English translation with the same
// placeholders, and the dictionary has no leftovers.
func TestBackendMessagesTranslated(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	used := map[string]string{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "tr" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				t.Errorf("%s: tr() needs a string literal", fset.Position(call.Pos()))
				return true
			}
			s, _ := strconv.Unquote(lit.Value)
			used[s] = fset.Position(call.Pos()).String()
			return true
		})
	}
	for ru, pos := range used {
		en, ok := english[ru]
		if !ok {
			t.Errorf("%s: no English for %q", pos, ru)
			continue
		}
		if strings.Join(verbs(ru), "") != strings.Join(verbs(en), "") {
			t.Errorf("%s: placeholders differ: %q / %q", pos, ru, en)
		}
	}
	for ru := range english {
		if _, ok := used[ru]; !ok {
			t.Errorf("unused translation %q", ru)
		}
	}
}

// The window: both dictionaries have the same keys, placeholders and plural
// forms, and every key used by the page exists.
func TestWindowTranslated(t *testing.T) {
	src, err := os.ReadFile("ui/i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	ruStart, enStart := strings.Index(text, "\n  ru: {"), strings.Index(text, "\n  en: {")
	end := strings.Index(text, "\n};")
	if ruStart < 0 || enStart < 0 || end < 0 {
		t.Fatal("dictionaries not found")
	}
	entry := regexp.MustCompile(`(?m)^\s+"([^"]+)":\s*(\[[^\]]*\]|"(?:[^"\\]|\\.)*")`)
	parse := func(block string) map[string]string {
		m := map[string]string{}
		for _, e := range entry.FindAllStringSubmatch(block, -1) {
			if _, dup := m[e[1]]; dup {
				t.Errorf("duplicate key %q", e[1])
			}
			m[e[1]] = e[2]
		}
		return m
	}
	ru, en := parse(text[ruStart:enStart]), parse(text[enStart:end])
	ph := regexp.MustCompile(`\{\w+\}`)
	set := func(s string) string {
		p := ph.FindAllString(s, -1)
		sort.Strings(p)
		return strings.Join(slicesCompact(p), "")
	}
	for k, rv := range ru {
		ev, ok := en[k]
		if !ok {
			t.Errorf("no English for %q", k)
			continue
		}
		if set(rv) != set(ev) {
			t.Errorf("%q: placeholders differ: %s / %s", k, rv, ev)
		}
		if strings.HasPrefix(rv, "[") != strings.HasPrefix(ev, "[") {
			t.Errorf("%q: plural forms in one language only", k)
		} else if strings.HasPrefix(rv, "[") {
			if n := strings.Count(rv, `",`) + 1; n != 3 {
				t.Errorf("%q: Russian needs 3 plural forms, has %d", k, n)
			}
			if n := strings.Count(ev, `",`) + 1; n != 2 {
				t.Errorf("%q: English needs 2 plural forms, has %d", k, n)
			}
		}
	}
	for k := range en {
		if _, ok := ru[k]; !ok {
			t.Errorf("no Russian for %q", k)
		}
	}
	html, _ := os.ReadFile("ui/index.html")
	js, _ := os.ReadFile("ui/app.js")
	var keys []string
	for _, m := range regexp.MustCompile(`data-i18n(?:-[a-z]+)?="([^"]+)"`).FindAllStringSubmatch(string(html), -1) {
		keys = append(keys, m[1])
	}
	for _, m := range regexp.MustCompile(`\bt\("([a-zA-Z0-9.]+)"`).FindAllStringSubmatch(string(js), -1) {
		if !strings.HasSuffix(m[1], ".") { // "routing." + r: listed below
			keys = append(keys, m[1])
		}
	}
	// keys built in code
	for _, r := range []string{"smart", "global", "blocked"} {
		keys = append(keys, "routing."+r+".hint")
	}
	for _, a := range []string{"expired", "1d", "3d", "traffic"} {
		keys = append(keys, "alert."+a)
	}
	for _, m := range regexp.MustCompile(`"(state\.[a-z]+)"`).FindAllStringSubmatch(string(js), -1) {
		keys = append(keys, m[1])
	}
	for _, k := range keys {
		if _, ok := ru[k]; !ok {
			t.Errorf("key %q is used but not defined", k)
		}
	}
}

func slicesCompact(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
