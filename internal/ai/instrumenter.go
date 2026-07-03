package ai

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
)

// ============================================================================
// AST Source Code Instrumenter
// ============================================================================
//
// Injects debug trace statements into user source code for dynamic analysis.
// Used by the AI 诊断助手 when the submission verdict is WA or RE —
// instruments the code with Printf calls, runs it against test cases, and
// feeds the traced output to the LLM for causal analysis.
//
// Verdict-aware dispatch:
//
//	CE → static analysis only (no instrumentation needed)
//	TLE → reference-solution validation + static complexity analysis
//	WA/RE → Instrument(code) → run against failed test case → trace output

// Instrumenter defines the interface for source code instrumentation.
type Instrumenter interface {
	// Instrument injects debug trace statements into source code.
	// For Go code, inserts fmt.Printf calls after AssignStmt nodes and
	// fmt.Println calls at the start of ForStmt bodies.
	// Non-Go languages return (code, nil) — no instrumentation (unsupported).
	Instrument(code string, lang string) (instrumentedCode string, err error)
}

// GoInstrumenter implements Instrumenter for Go source using go/parser,
// go/ast, and go/printer. Injects debug trace statements after assignment
// statements and at loop entry points.
type GoInstrumenter struct{}

// NewGoInstrumenter creates a new GoInstrumenter.
func NewGoInstrumenter() *GoInstrumenter {
	return &GoInstrumenter{}
}

// insertion records a statement to insert after, within a given block.
type insertion struct {
	stmt  ast.Stmt
	ident string
}

// Instrument parses Go source, injects debug trace statements, and
// returns the instrumented code. Returns an error if parsing fails.
func (g *GoInstrumenter) Instrument(code string, lang string) (string, error) {
	if strings.ToLower(lang) != "go" {
		return code, nil
	}

	defer func() {
		if r := recover(); r != nil {
			panic(fmt.Errorf("go instrumenter panic: %v", r))
		}
	}()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", code, parser.AllErrors)
	if err != nil {
		return "", fmt.Errorf("go instrumenter: parse error: %w", err)
	}

	targets := make(map[*ast.BlockStmt][]insertion)
	loopBodies := make(map[*ast.BlockStmt]bool)

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		collectTargets(fset, fn.Body, targets, loopBodies)
	}

	// Insert loop-enter statements at the start of loop bodies.
	for block := range loopBodies {
		if len(block.List) == 0 {
			continue
		}
		line := fset.Position(block.Pos()).Line
		debugStmt := parseExprStmt(fmt.Sprintf(`fmt.Println("DEBUG_LOOP_ENTER [line:%d]")`, line))
		if debugStmt == nil {
			continue
		}
		block.List = append([]ast.Stmt{debugStmt}, block.List...)
	}

	// Insert after-assignment statements (reverse order per block).
	for block, inserts := range targets {
		if len(block.List) == 0 {
			continue
		}
		type idxStmt struct {
			idx  int
			stmt ast.Stmt
		}
		var items []idxStmt
		for _, ins := range inserts {
			for i, s := range block.List {
				if s == ins.stmt {
					line := fset.Position(s.Pos()).Line
					ds := buildAssignDebugStmt(line, ins.ident)
					if ds != nil {
						items = append(items, idxStmt{i, ds})
					}
					break
				}
			}
		}
		// Sort descending by index.
		for i := 0; i < len(items); i++ {
			for j := i + 1; j < len(items); j++ {
				if items[j].idx > items[i].idx {
					items[i], items[j] = items[j], items[i]
				}
			}
		}
		for _, it := range items {
			newList := make([]ast.Stmt, len(block.List)+1)
			copy(newList, block.List[:it.idx+1])
			newList[it.idx+1] = it.stmt
			copy(newList[it.idx+2:], block.List[it.idx+1:])
			block.List = newList
		}
	}

	ensureFmtImport(f)

	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, f); err != nil {
		return "", fmt.Errorf("go instrumenter: print error: %w", err)
	}
	return buf.String(), nil
}

// collectTargets recursively walks a block collecting assignment and loop targets.
func collectTargets(
	fset *token.FileSet,
	block *ast.BlockStmt,
	targets map[*ast.BlockStmt][]insertion,
	loopBodies map[*ast.BlockStmt]bool,
) {
	for _, stmt := range block.List {
		switch s := stmt.(type) {
		case *ast.AssignStmt:
			if s.Tok == token.ASSIGN || s.Tok == token.DEFINE {
				for _, lhs := range s.Lhs {
					if _, ok := lhs.(*ast.Ident); ok {
						targets[block] = append(targets[block], insertion{stmt: stmt, ident: identName(lhs)})
						break
					}
				}
			}
		case *ast.ForStmt:
			if s.Body != nil {
				loopBodies[s.Body] = true
				collectTargets(fset, s.Body, targets, loopBodies)
			}
		case *ast.RangeStmt:
			if s.Body != nil {
				loopBodies[s.Body] = true
				collectTargets(fset, s.Body, targets, loopBodies)
			}
		case *ast.IfStmt:
			if s.Body != nil {
				collectTargets(fset, s.Body, targets, loopBodies)
			}
			if s.Else != nil {
				if elseBlock, ok := s.Else.(*ast.BlockStmt); ok {
					collectTargets(fset, elseBlock, targets, loopBodies)
				}
			}
		case *ast.SwitchStmt:
			if s.Body != nil {
				for _, cc := range s.Body.List {
					if caseClause, ok := cc.(*ast.CaseClause); ok {
						synth := &ast.BlockStmt{List: caseClause.Body}
						collectTargets(fset, synth, targets, loopBodies)
						caseClause.Body = synth.List
					}
				}
			}
		}
	}
}

func identName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return identName(e.X) + "." + e.Sel.Name
	default:
		return "_"
	}
}

// parseExprStmt parses an expression statement from source text.
// Uses parser.ParseExpr and wraps the result in an ExprStmt.
func parseExprStmt(src string) ast.Stmt {
	expr, err := parser.ParseExpr(src)
	if err != nil {
		return nil
	}
	return &ast.ExprStmt{X: expr}
}

// buildAssignDebugStmt creates: fmt.Printf("DEBUG_VAR_TRACE [line:N] var = %+v\n", var)
// The generated source uses %s and %+v format verbs in the Printf call,
// so the outer fmt.Sprintf needs %%s → %s and %%+v → %+v.
func buildAssignDebugStmt(line int, varName string) ast.Stmt {
	src := fmt.Sprintf(`fmt.Printf("DEBUG_VAR_TRACE [line:%d] %%s = %%+v\n", %q, %s)`,
		line, varName, varName)
	return parseExprStmt(src)
}

// ensureFmtImport adds the "fmt" import if not present.
func ensureFmtImport(f *ast.File) {
	for _, imp := range f.Imports {
		if imp.Path.Value == `"fmt"` {
			return
		}
	}

	newImport := &ast.ImportSpec{
		Path: &ast.BasicLit{
			Kind:  token.STRING,
			Value: `"fmt"`,
		},
	}

	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if ok && genDecl.Tok == token.IMPORT {
			genDecl.Specs = append(genDecl.Specs, newImport)
			return
		}
	}

	f.Decls = append([]ast.Decl{
		&ast.GenDecl{
			Tok:   token.IMPORT,
			Specs: []ast.Spec{newImport},
		},
	}, f.Decls...)
}
