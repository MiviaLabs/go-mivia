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
	result, err := ParseGoFileSemantic(relativePath, source)
	return result.Symbols, err
}

func ParseGoFileSemantic(relativePath string, source []byte) (ExtractorResult, error) {
	if !utf8.Valid(source) {
		return ExtractorResult{}, fmt.Errorf("invalid utf-8 content")
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, relativePath, source, parser.ParseComments)
	if err != nil {
		return ExtractorResult{}, err
	}

	symbols := []Symbol{{
		Kind:        SymbolKindPackage,
		Name:        file.Name.Name,
		PackageName: file.Name.Name,
		StartLine:   lineFor(fileSet, file.Name.Pos()),
		EndLine:     lineFor(fileSet, file.Name.End()),
	}}
	applyGoSpan(fileSet, file.Name.Pos(), file.Name.End(), &symbols[0])

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
			StartByte:   offsetFor(fileSet, importSpec.Pos()),
			EndByte:     offsetFor(fileSet, importSpec.End()),
			StartColumn: columnFor(fileSet, importSpec.Pos()),
			EndColumn:   columnFor(fileSet, importSpec.End()),
		})
	}

	var references []Reference
	var calls []Call
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
					StartByte:   offsetFor(fileSet, typeSpec.Pos()),
					EndByte:     offsetFor(fileSet, typeSpec.End()),
					StartColumn: columnFor(fileSet, typeSpec.Pos()),
					EndColumn:   columnFor(fileSet, typeSpec.End()),
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
				StartByte:   offsetFor(fileSet, typed.Pos()),
				EndByte:     offsetFor(fileSet, typed.End()),
				StartColumn: columnFor(fileSet, typed.Pos()),
				EndColumn:   columnFor(fileSet, typed.End()),
			})
			funcRefs, funcCalls := extractGoFunctionOccurrences(fileSet, file.Name.Name, typed)
			references = append(references, funcRefs...)
			calls = append(calls, funcCalls...)
		}
	}

	return ExtractorResult{Symbols: symbols, References: dedupeReferences(references), Calls: dedupeCalls(calls)}, nil
}

func lineFor(fileSet *token.FileSet, position token.Pos) int {
	if !position.IsValid() {
		return 0
	}
	return fileSet.Position(position).Line
}

func columnFor(fileSet *token.FileSet, position token.Pos) int {
	if !position.IsValid() {
		return 0
	}
	return fileSet.Position(position).Column
}

func offsetFor(fileSet *token.FileSet, position token.Pos) int {
	if !position.IsValid() {
		return 0
	}
	return fileSet.Position(position).Offset
}

func applyGoSpan(fileSet *token.FileSet, start token.Pos, end token.Pos, symbol *Symbol) {
	symbol.StartByte = offsetFor(fileSet, start)
	symbol.EndByte = offsetFor(fileSet, end)
	symbol.StartColumn = columnFor(fileSet, start)
	symbol.EndColumn = columnFor(fileSet, end)
}

func exprString(fileSet *token.FileSet, expr ast.Expr) string {
	var buffer bytes.Buffer
	if err := printer.Fprint(&buffer, fileSet, expr); err != nil {
		return ""
	}
	return buffer.String()
}

func extractGoFunctionOccurrences(fileSet *token.FileSet, packageName string, fn *ast.FuncDecl) ([]Reference, []Call) {
	if fn == nil || fn.Body == nil {
		return nil, nil
	}
	callerName := fn.Name.Name
	var references []Reference
	var calls []Call
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.CallExpr:
			name, receiver := goCallTarget(fileSet, typed.Fun)
			if name != "" {
				calls = append(calls, Call{
					CallerName:       callerName,
					CalleeName:       name,
					Receiver:         receiver,
					StartLine:        lineFor(fileSet, typed.Pos()),
					EndLine:          lineFor(fileSet, typed.End()),
					StartByte:        offsetFor(fileSet, typed.Pos()),
					EndByte:          offsetFor(fileSet, typed.End()),
					StartColumn:      columnFor(fileSet, typed.Pos()),
					EndColumn:        columnFor(fileSet, typed.End()),
					ResolutionStatus: "unresolved",
					Confidence:       "candidate",
				})
			}
		case *ast.SelectorExpr:
			if typed.Sel != nil {
				references = append(references, Reference{
					Kind:                "selector",
					Name:                typed.Sel.Name,
					TargetName:          typed.Sel.Name,
					PackageName:         packageName,
					Receiver:            exprString(fileSet, typed.X),
					EnclosingSymbolName: callerName,
					StartLine:           lineFor(fileSet, typed.Pos()),
					EndLine:             lineFor(fileSet, typed.End()),
					StartByte:           offsetFor(fileSet, typed.Pos()),
					EndByte:             offsetFor(fileSet, typed.End()),
					StartColumn:         columnFor(fileSet, typed.Pos()),
					EndColumn:           columnFor(fileSet, typed.End()),
					ResolutionStatus:    "unresolved",
					Confidence:          "candidate",
				})
			}
			return false
		case *ast.Ident:
			if typed.Name == "_" || typed.Obj != nil && typed.Obj.Kind != ast.Fun {
				return true
			}
			references = append(references, Reference{
				Kind:                "identifier",
				Name:                typed.Name,
				TargetName:          typed.Name,
				PackageName:         packageName,
				EnclosingSymbolName: callerName,
				StartLine:           lineFor(fileSet, typed.Pos()),
				EndLine:             lineFor(fileSet, typed.End()),
				StartByte:           offsetFor(fileSet, typed.Pos()),
				EndByte:             offsetFor(fileSet, typed.End()),
				StartColumn:         columnFor(fileSet, typed.Pos()),
				EndColumn:           columnFor(fileSet, typed.End()),
				ResolutionStatus:    "unresolved",
				Confidence:          "candidate",
			})
		}
		return true
	})
	return references, calls
}

func goCallTarget(fileSet *token.FileSet, expr ast.Expr) (string, string) {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name, ""
	case *ast.SelectorExpr:
		if typed.Sel == nil {
			return "", ""
		}
		return typed.Sel.Name, exprString(fileSet, typed.X)
	default:
		return exprString(fileSet, expr), ""
	}
}
