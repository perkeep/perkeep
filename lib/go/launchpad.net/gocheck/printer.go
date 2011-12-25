package gocheck

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"go/printer"
	"os"
)

func indent(s, with string) (r string) {
	eol := true
	for i := 0; i != len(s); i++ {
		c := s[i]
		switch {
		case eol && c == '\n' || c == '\r':
		case c == '\n' || c == '\r':
			eol = true
		case eol:
			eol = false
			s = s[:i] + with + s[i:]
			i += len(with)
		}
	}
	return s
}

func printLine(filename string, line int) (string, os.Error) {
	fset := token.NewFileSet()
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	fnode, err := parser.ParseFile(fset, filename, file, 0)
	if err != nil {
		return "", err
	}
	config := &printer.Config{Mode: printer.UseSpaces, Tabwidth: 4}
	lp := &linePrinter{fset: fset, line: line, config: config}
	ast.Walk(lp, fnode)
	return lp.output.String(), nil
}

type linePrinter struct {
	config *printer.Config
	fset   *token.FileSet
	line   int
	output bytes.Buffer
	stmt   ast.Stmt
}

func (lp *linePrinter) emit() bool {
	if lp.stmt != nil {
		lp.trim(lp.stmt)
		lp.config.Fprint(&lp.output, lp.fset, lp.stmt)
		lp.stmt = nil
		return true
	}
	return false
}

func (lp *linePrinter) Visit(n ast.Node) (w ast.Visitor) {
	if n == nil {
		if lp.output.Len() == 0 {
			lp.emit()
		}
		return nil
	}
	first := lp.fset.Position(n.Pos()).Line
	last := lp.fset.Position(n.End()).Line
	if first <= lp.line && last >= lp.line {
		// Print the innermost statement containing the line.
		if stmt, ok := n.(ast.Stmt); ok {
			if _, ok := n.(*ast.BlockStmt); !ok {
				lp.stmt = stmt
			}
		}
		if first == lp.line && lp.emit() {
			return nil
		}
		return lp
	}
	return nil
}

func (lp *linePrinter) trim(n ast.Node) bool {
	stmt, ok := n.(ast.Stmt)
	if !ok {
		return true
	}
	line := lp.fset.Position(n.Pos()).Line
	if line != lp.line {
		return false
	}
	switch stmt := stmt.(type) {
	case *ast.IfStmt:
		stmt.Body = lp.trimBlock(stmt.Body)
	case *ast.SwitchStmt:
		stmt.Body = lp.trimBlock(stmt.Body)
	case *ast.TypeSwitchStmt:
		stmt.Body = lp.trimBlock(stmt.Body)
	case *ast.CaseClause:
		stmt.Body = lp.trimList(stmt.Body)
	case *ast.CommClause:
		stmt.Body = lp.trimList(stmt.Body)
	case *ast.BlockStmt:
		stmt.List = lp.trimList(stmt.List)
	}
	return true
}

func (lp *linePrinter) trimBlock(stmt *ast.BlockStmt) *ast.BlockStmt {
	if !lp.trim(stmt) {
		return lp.emptyBlock(stmt)
	}
	stmt.Rbrace = stmt.Lbrace
	return stmt
}

func (lp *linePrinter) trimList(stmts []ast.Stmt) []ast.Stmt {
	for i := 0; i != len(stmts); i++ {
		if !lp.trim(stmts[i]) {
			stmts[i] = lp.emptyStmt(stmts[i])
			break
		}
	}
	return stmts
}

func (lp *linePrinter) emptyStmt(n ast.Node) *ast.ExprStmt {
	return &ast.ExprStmt{&ast.Ellipsis{n.Pos(), nil}}
}

func (lp *linePrinter) emptyBlock(n ast.Node) *ast.BlockStmt {
	p := n.Pos()
	return &ast.BlockStmt{p, []ast.Stmt{lp.emptyStmt(n)}, p}
}
