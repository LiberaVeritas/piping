package postgres

import (
	"context"
	"go/ast"
	"go/constant"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestAllSQLQueriesPrepare(t *testing.T) {
	queries := collectSQL(t, ".")
	if len(queries) < 10 {
		t.Fatalf("only found %d queries — the AST walk is probably broken", len(queries))
	}
	t.Logf("found %d SQL statements", len(queries))

	pool := testPool(t)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	for _, q := range queries {
		t.Run(q.pos, func(t *testing.T) {
			_, err := conn.Conn().Prepare(ctx, "check_"+strings.ReplaceAll(q.pos, ":", "_"), q.sql)
			if err != nil {
				t.Errorf("%s\n%s\n%v", q.pos, strings.TrimSpace(q.sql), err)
			}
		})
	}
}

type sqlQuery struct {
	pos string
	sql string
}

var sqlVerbs = []string{"SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "TRUNCATE"}

func looksLikeSQL(s string) bool {
	up := strings.ToUpper(strings.TrimSpace(s))
	for _, v := range sqlVerbs {
		if strings.HasPrefix(up, v) {
			return true
		}
	}
	return false
}

func collectSQL(t *testing.T, dir string) []sqlQuery {
	t.Helper()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:  dir,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		t.Fatalf("loading package: %v", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatalf("package load had errors")
	}

	pkg := pkgs[0]

	var out []sqlQuery
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "Query", "QueryRow", "Exec", "Prepare":
			default:
				return true
			}

			for _, arg := range call.Args {
				tv, ok := pkg.TypesInfo.Types[arg]
				if ok && tv.Value != nil && tv.Value.Kind() == constant.String {
					s := constant.StringVal(tv.Value)

					if looksLikeSQL(s) {
						out = append(out, sqlQuery{
							pos: pkg.Fset.Position(call.Pos()).String(),
							sql: s,
						})
						break
					}
				}
			}
			return true
		})
	}
	return out
}
