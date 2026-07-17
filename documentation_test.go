package opus_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportedAPIDocumented(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{".", "oggopus"} {
		fset := token.NewFileSet()
		packages, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool {
			return filepath.Ext(info.Name()) == ".go" && !strings.HasSuffix(info.Name(), "_test.go")
		}, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", dir, err)
		}

		for _, pkg := range packages {
			for filename, file := range pkg.Files {
				for _, declaration := range file.Decls {
					switch declaration := declaration.(type) {
					case *ast.FuncDecl:
						if ast.IsExported(declaration.Name.Name) && declaration.Doc == nil {
							t.Errorf("%s:%d: %s has no doc comment", filename, fset.Position(declaration.Pos()).Line, declaration.Name.Name)
						}
					case *ast.GenDecl:
						checkExportedSpecsDocumented(t, fset, filename, declaration)
					}
				}
			}
		}
	}
}

func checkExportedSpecsDocumented(t *testing.T, fset *token.FileSet, filename string, declaration *ast.GenDecl) {
	t.Helper()

	for _, spec := range declaration.Specs {
		switch spec := spec.(type) {
		case *ast.TypeSpec:
			if !ast.IsExported(spec.Name.Name) {
				continue
			}
			if spec.Doc == nil && declaration.Doc == nil {
				t.Errorf("%s:%d: %s has no doc comment", filename, fset.Position(spec.Pos()).Line, spec.Name.Name)
			}
			structType, ok := spec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range structType.Fields.List {
				for _, name := range field.Names {
					if ast.IsExported(name.Name) && field.Doc == nil && field.Comment == nil {
						t.Errorf("%s:%d: %s.%s has no doc comment", filename, fset.Position(field.Pos()).Line, spec.Name.Name, name.Name)
					}
				}
			}
		case *ast.ValueSpec:
			for _, name := range spec.Names {
				if ast.IsExported(name.Name) && spec.Doc == nil && declaration.Doc == nil {
					t.Errorf("%s:%d: %s has no doc comment", filename, fset.Position(spec.Pos()).Line, name.Name)
				}
			}
		}
	}
}
