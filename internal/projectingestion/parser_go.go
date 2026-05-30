package projectingestion

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strconv"
	"unicode/utf8"
)

func ParseGoFile(relativePath string, source []byte) ([]Symbol, error) {
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("invalid utf-8 content")
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, relativePath, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	symbols := []Symbol{{
		Kind:        SymbolKindPackage,
		Name:        file.Name.Name,
		PackageName: file.Name.Name,
		StartLine:   lineFor(fileSet, file.Name.Pos()),
		EndLine:     lineFor(fileSet, file.Name.End()),
	}}

	for _, importSpec := range file.Imports {
		importPath, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			importPath = ""
		}
		name := importPath
		if importSpec.Name != nil {
			name = importSpec.Name.Name
		}
		symbols = append(symbols, Symbol{
			Kind:        SymbolKindImport,
			Name:        name,
			PackageName: file.Name.Name,
			ImportPath:  importPath,
			StartLine:   lineFor(fileSet, importSpec.Pos()),
			EndLine:     lineFor(fileSet, importSpec.End()),
		})
	}

	for _, decl := range file.Decls {
		switch typed := decl.(type) {
		case *ast.GenDecl:
			if typed.Tok != token.TYPE {
				continue
			}
			for _, spec := range typed.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				symbols = append(symbols, Symbol{
					Kind:        SymbolKindType,
					Name:        typeSpec.Name.Name,
					PackageName: file.Name.Name,
					StartLine:   lineFor(fileSet, typeSpec.Pos()),
					EndLine:     lineFor(fileSet, typeSpec.End()),
				})
			}
		case *ast.FuncDecl:
			kind := SymbolKindFunction
			receiver := ""
			if typed.Recv != nil && len(typed.Recv.List) > 0 {
				kind = SymbolKindMethod
				receiver = exprString(fileSet, typed.Recv.List[0].Type)
			}
			symbols = append(symbols, Symbol{
				Kind:        kind,
				Name:        typed.Name.Name,
				PackageName: file.Name.Name,
				Receiver:    receiver,
				StartLine:   lineFor(fileSet, typed.Pos()),
				EndLine:     lineFor(fileSet, typed.End()),
			})
		}
	}

	return symbols, nil
}

func lineFor(fileSet *token.FileSet, position token.Pos) int {
	if !position.IsValid() {
		return 0
	}
	return fileSet.Position(position).Line
}

func exprString(fileSet *token.FileSet, expr ast.Expr) string {
	var buffer bytes.Buffer
	if err := printer.Fprint(&buffer, fileSet, expr); err != nil {
		return ""
	}
	return buffer.String()
}
