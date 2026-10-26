package main

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/agentlab/agentlab/internal/clicontract"
)

// Check the public catalog against actual registered flags and consumed operand
// indexes. A newly implemented command must not become unusable or invisible
// because its discovery artifact was not regenerated.
func TestCatalogMatchesRegisteredParsers(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range source.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := ""
			maxOperand := 0
			flags := []string{}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "newFlagSet" && len(call.Args) > 0 {
					if literal, ok := call.Args[0].(*ast.BasicLit); ok {
						name, _ = strconv.Unquote(literal.Value)
					}
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				object, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}
				if object.Name == "flag" && selector.Sel.Name == "NewFlagSet" && len(call.Args) > 0 {
					if literal, ok := call.Args[0].(*ast.BasicLit); ok {
						name, _ = strconv.Unquote(literal.Value)
					}
				}
				if object.Name != "fs" {
					return true
				}
				if selector.Sel.Name == "Arg" && len(call.Args) == 1 {
					if literal, ok := call.Args[0].(*ast.BasicLit); ok {
						index, _ := strconv.Atoi(literal.Value)
						if index+1 > maxOperand {
							maxOperand = index + 1
						}
					}
				}
				if strings.HasSuffix(selector.Sel.Name, "Var") || selector.Sel.Name == "Var" {
					if len(call.Args) > 1 {
						if literal, ok := call.Args[1].(*ast.BasicLit); ok {
							flag, _ := strconv.Unquote(literal.Value)
							flags = append(flags, flag)
						}
					}
				}
				return true
			})
			if name == "" || name == "agentlab" {
				continue
			}
			definition, ok := clicontract.Commands[name]
			if !ok {
				t.Errorf("%s: no catalog entry for %q", path, name)
				continue
			}
			if definition.Operands >= 0 && maxOperand > definition.Operands {
				t.Errorf("%s consumes %d operands but catalog allows only %d", name, maxOperand, definition.Operands)
			}
			for _, flag := range flags {
				if _, ok := definition.Flags[flag]; !ok {
					t.Errorf("%s: registered --%s is missing from catalog", name, flag)
				}
			}
		}
	}
}

func TestCatalogIncludesBoundLocalSecretFlags(t *testing.T) {
	fs := flag.NewFlagSet("secrets local", flag.ContinueOnError)
	bindSecretsLocalFlags(fs)
	for _, name := range []string{"secrets show", "secrets validate", "secrets add-ssh-key", "secrets remove-ssh-key", "secrets set-tailscale", "secrets clear-tailscale"} {
		definition := clicontract.Commands[name]
		fs.VisitAll(func(f *flag.Flag) {
			if _, ok := definition.Flags[f.Name]; !ok {
				t.Errorf("%s missing bound --%s", name, f.Name)
			}
		})
	}
}
