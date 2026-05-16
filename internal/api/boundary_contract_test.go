package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

func TestAdminWriteHandlersUseLocalRequestDTOs(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		name := info.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("解析 api 包失败: %v", err)
	}
	pkg := pkgs["api"]
	if pkg == nil {
		t.Fatal("未找到 api 包")
	}

	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !isAdminWriteHandler(fn.Name.Name) {
				continue
			}
			decoded := decodedRequestIdentNames(fn.Body)
			for _, name := range decoded {
				if isForbiddenAdminWriteDTOName(name) {
					pos := fset.Position(fn.Pos())
					t.Fatalf("%s:%d: %s 解码请求体到 %s；admin write handler 必须使用本地 request DTO，不能直接使用 Record/持久化结构", pos.Filename, pos.Line, fn.Name.Name, name)
				}
			}
		}
	}
}

func isAdminWriteHandler(name string) bool {
	if !strings.HasPrefix(name, "handleAdmin") {
		return false
	}
	return strings.Contains(name, "Create") ||
		strings.Contains(name, "Update") ||
		strings.Contains(name, "Upsert") ||
		strings.Contains(name, "Apply") ||
		strings.Contains(name, "Import") ||
		strings.Contains(name, "Generate") ||
		strings.Contains(name, "Preview") ||
		strings.Contains(name, "Recompute") ||
		strings.Contains(name, "Run") ||
		strings.Contains(name, "Cancel") ||
		strings.Contains(name, "Approve") ||
		strings.Contains(name, "Reject") ||
		strings.Contains(name, "Rollback") ||
		strings.Contains(name, "Evaluate")
}

func decodedRequestIdentNames(body *ast.BlockStmt) []string {
	if body == nil {
		return nil
	}
	var names []string
	scope := map[string]string{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.DeclStmt:
			collectValueSpecTypes(x, scope)
		case *ast.AssignStmt:
			collectShortVarTypes(x, scope)
		case *ast.CallExpr:
			if !isJSONDecodeCall(x) || len(x.Args) == 0 {
				return true
			}
			if name, ok := decodedArgName(x.Args[0], scope); ok {
				names = append(names, name)
			}
		}
		return true
	})
	return names
}

func collectValueSpecTypes(stmt *ast.DeclStmt, scope map[string]string) {
	gen, ok := stmt.Decl.(*ast.GenDecl)
	if !ok {
		return
	}
	for _, spec := range gen.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		typeName := exprTypeName(value.Type)
		for i, name := range value.Names {
			if typeName != "" {
				scope[name.Name] = typeName
				continue
			}
			if i < len(value.Values) {
				if inferred := exprTypeName(value.Values[i]); inferred != "" {
					scope[name.Name] = inferred
				}
			}
		}
	}
}

func collectShortVarTypes(stmt *ast.AssignStmt, scope map[string]string) {
	if stmt.Tok != token.DEFINE {
		return
	}
	for i, lhs := range stmt.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok || i >= len(stmt.Rhs) {
			continue
		}
		if typeName := exprTypeName(stmt.Rhs[i]); typeName != "" {
			scope[ident.Name] = typeName
		}
	}
}

func isJSONDecodeCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Decode"
}

func decodedArgName(arg ast.Expr, scope map[string]string) (string, bool) {
	unary, ok := arg.(*ast.UnaryExpr)
	if !ok || unary.Op != token.AND {
		return "", false
	}
	ident, ok := unary.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	name, ok := scope[ident.Name]
	return name, ok
}

func exprTypeName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		if pkg, ok := x.X.(*ast.Ident); ok {
			return pkg.Name + "." + x.Sel.Name
		}
		return x.Sel.Name
	case *ast.StarExpr:
		return exprTypeName(x.X)
	case *ast.CompositeLit:
		return exprTypeName(x.Type)
	default:
		return ""
	}
}

func isForbiddenAdminWriteDTOName(name string) bool {
	shortName := name
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		shortName = name[idx+1:]
	}
	return strings.HasSuffix(shortName, "Record") ||
		strings.HasPrefix(name, "store.")
}
