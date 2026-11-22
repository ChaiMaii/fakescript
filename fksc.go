package main

import (
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const fkscVersion = "3.0.0"

// ============================================================================
// 1. LEXER
// ============================================================================

type TokenKind int

const (
	TokWord TokenKind = iota
	TokNum
	TokString
	TokBool
	TokNewline
	TokEOF
)

type Token struct {
	Kind TokenKind
	Val  string
	Line int
}

type CompileError struct {
	Line int
	Msg  string
}

func (e *CompileError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
	}
	return e.Msg
}

func lex(src string) ([]Token, error) {
	var tokens []Token
	rawLines := strings.Split(src, "\n")

	for idx, raw := range rawLines {
		lineNum := idx + 1
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "note" || strings.HasPrefix(trimmed, "note ") || strings.HasPrefix(trimmed, "note\t") {
			continue
		}

		i := 0
		n := len(line)
		for i < n {
			// Skip whitespace
			for i < n && (line[i] == ' ' || line[i] == '\t') {
				i++
			}
			if i >= n {
				break
			}

			// Read a word
			start := i
			for i < n && line[i] != ' ' && line[i] != '\t' {
				i++
			}
			word := line[start:i]

			if word == "say" {
				// String literal: capture everything until standalone word "end" on this line
				contentStart := i
				if contentStart < n && (line[contentStart] == ' ' || line[contentStart] == '\t') {
					contentStart++
				}
				found := false
				scan := i
				for scan < n {
					for scan < n && (line[scan] == ' ' || line[scan] == '\t') {
						scan++
					}
					wStart := scan
					for scan < n && line[scan] != ' ' && line[scan] != '\t' {
						scan++
					}
					if line[wStart:scan] == "end" {
						contentEnd := wStart
						if contentEnd > contentStart && (line[contentEnd-1] == ' ' || line[contentEnd-1] == '\t') {
							contentEnd--
						}
						strVal := ""
						if contentEnd >= contentStart {
							strVal = line[contentStart:contentEnd]
						}
						tokens = append(tokens, Token{Kind: TokString, Val: strVal, Line: lineNum})
						i = scan
						found = true
						break
					}
				}
				if !found {
					return nil, &CompileError{Line: lineNum, Msg: "unterminated string literal (missing 'end' after 'say')"}
				}
				continue
			}

			// Strict zero-symbol validation outside strings and comments
			if !isZeroSymbolWord(word) {
				return nil, &CompileError{
					Line: lineNum,
					Msg:  fmt.Sprintf("symbol %q is not allowed in FakeScript — use plain English keywords only", word),
				}
			}

			if isAllDigits(word) {
				tokens = append(tokens, Token{Kind: TokNum, Val: word, Line: lineNum})
			} else if word == "true" || word == "yes" {
				tokens = append(tokens, Token{Kind: TokBool, Val: "true", Line: lineNum})
			} else if word == "false" || word == "no" {
				tokens = append(tokens, Token{Kind: TokBool, Val: "false", Line: lineNum})
			} else {
				tokens = append(tokens, Token{Kind: TokWord, Val: word, Line: lineNum})
			}
		}

		tokens = append(tokens, Token{Kind: TokNewline, Val: "\n", Line: lineNum})
	}

	tokens = append(tokens, Token{Kind: TokEOF, Line: len(rawLines)})
	return tokens, nil
}

func isZeroSymbolWord(w string) bool {
	for _, r := range w {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return len(w) > 0
}

func isAllDigits(w string) bool {
	for _, r := range w {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(w) > 0
}

// ============================================================================
// 2. TYPES & AST
// ============================================================================

type TypeKind int

const (
	TyUnknown TypeKind = iota
	TyVoid
	TyNum
	TyText
	TyFlag
	TyList
	TyAction
)

type Type struct {
	Kind TypeKind
	Elem *Type
}

var (
	TypeVoid   = Type{Kind: TyVoid}
	TypeNum    = Type{Kind: TyNum}
	TypeText   = Type{Kind: TyText}
	TypeFlag   = Type{Kind: TyFlag}
	TypeAction = Type{Kind: TyAction}
)

func ListOf(elem Type) Type {
	e := elem
	return Type{Kind: TyList, Elem: &e}
}

func (t Type) Equal(other Type) bool {
	if t.Kind != other.Kind {
		return false
	}
	if t.Kind == TyList {
		if t.Elem == nil || other.Elem == nil {
			return t.Elem == other.Elem
		}
		return t.Elem.Equal(*other.Elem)
	}
	return true
}

func (t Type) String() string {
	switch t.Kind {
	case TyVoid:
		return "void"
	case TyNum:
		return "num"
	case TyText:
		return "text"
	case TyFlag:
		return "flag"
	case TyAction:
		return "action"
	case TyList:
		if t.Elem != nil {
			return "list of " + t.Elem.String()
		}
		return "list"
	default:
		return "unknown"
	}
}

func (t Type) GoType() string {
	switch t.Kind {
	case TyNum:
		return "int64"
	case TyText:
		return "string"
	case TyFlag:
		return "bool"
	case TyAction:
		return "func()"
	case TyList:
		return "[]" + t.Elem.GoType()
	default:
		return ""
	}
}

type Program struct {
	Imports []*ImportStmt
	Funcs   []*FuncDecl
	Stmts   []Stmt
}

type Param struct {
	Name string
	Type Type
	Line int
}

type FuncDecl struct {
	Name       string
	Params     []Param
	ReturnType Type
	Body       []Stmt
	Line       int
}

type Stmt interface {
	stmtNode()
	stmtLine() int
}

type ImportStmt struct {
	Module string
	Alias  string
	Line   int
}

func (*ImportStmt) stmtNode()       {}
func (s *ImportStmt) stmtLine() int { return s.Line }

type LetStmt struct {
	Name         string
	ExplicitType *Type
	Expr         Expr
	IsGlobal     bool
	ResolvedType Type
	Used         bool
	Line         int
}

func (*LetStmt) stmtNode()       {}
func (s *LetStmt) stmtLine() int { return s.Line }

type SetStmt struct {
	Name string
	Expr Expr
	Line int
}

func (*SetStmt) stmtNode()       {}
func (s *SetStmt) stmtLine() int { return s.Line }

type SetIndexStmt struct {
	ListName string
	Index    Expr
	Value    Expr
	Line     int
}

func (*SetIndexStmt) stmtNode()       {}
func (s *SetIndexStmt) stmtLine() int { return s.Line }

type AppendStmt struct {
	ListName string
	Value    Expr
	Line     int
}

func (*AppendStmt) stmtNode()       {}
func (s *AppendStmt) stmtLine() int { return s.Line }

type AskStmt struct {
	Target   string
	Prompt   Expr
	WantNum  bool
	IsNew    bool
	IsGlobal bool
	Used     bool
	Line     int
}

func (*AskStmt) stmtNode()       {}
func (s *AskStmt) stmtLine() int { return s.Line }

type IfBranch struct {
	Cond Expr
	Body []Stmt
	Line int
}

type IfStmt struct {
	Branches []IfBranch
	ElseBody []Stmt
	Line     int
}

func (*IfStmt) stmtNode()       {}
func (s *IfStmt) stmtLine() int { return s.Line }

type WhileStmt struct {
	Cond Expr
	Body []Stmt
	Line int
}

func (*WhileStmt) stmtNode()       {}
func (s *WhileStmt) stmtLine() int { return s.Line }

type RangeStmt struct {
	VarName string
	Start   Expr
	End     Expr
	Body    []Stmt
	Used    bool
	Line    int
}

func (*RangeStmt) stmtNode()       {}
func (s *RangeStmt) stmtLine() int { return s.Line }

type ForEachStmt struct {
	VarName  string
	ListExpr Expr
	Body     []Stmt
	Used     bool
	Line     int
}

func (*ForEachStmt) stmtNode()       {}
func (s *ForEachStmt) stmtLine() int { return s.Line }

type ReturnStmt struct {
	Value Expr
	Line  int
}

func (*ReturnStmt) stmtNode()       {}
func (s *ReturnStmt) stmtLine() int { return s.Line }

type BreakStmt struct{ Line int }

func (*BreakStmt) stmtNode()       {}
func (s *BreakStmt) stmtLine() int { return s.Line }

type ContinueStmt struct{ Line int }

func (*ContinueStmt) stmtNode()       {}
func (s *ContinueStmt) stmtLine() int { return s.Line }

type ExprStmt struct {
	Expr Expr
	Line int
}

func (*ExprStmt) stmtNode()       {}
func (s *ExprStmt) stmtLine() int { return s.Line }

// Expressions

type Expr interface {
	exprNode()
	exprLine() int
}

type NumLit struct {
	Val  int64
	Line int
}

func (*NumLit) exprNode()       {}
func (e *NumLit) exprLine() int { return e.Line }

type TextLit struct {
	Val  string
	Line int
}

func (*TextLit) exprNode()       {}
func (e *TextLit) exprLine() int { return e.Line }

type FlagLit struct {
	Val  bool
	Line int
}

func (*FlagLit) exprNode()       {}
func (e *FlagLit) exprLine() int { return e.Line }

type IdentExpr struct {
	Name         string
	ResolvedType Type
	Line         int
}

func (*IdentExpr) exprNode()       {}
func (e *IdentExpr) exprLine() int { return e.Line }

type UnaryExpr struct {
	Op    string
	Right Expr
	Line  int
}

func (*UnaryExpr) exprNode()       {}
func (e *UnaryExpr) exprLine() int { return e.Line }

type BinaryExpr struct {
	Op    string
	Left  Expr
	Right Expr
	Line  int
}

func (*BinaryExpr) exprNode()       {}
func (e *BinaryExpr) exprLine() int { return e.Line }

type ListLitExpr struct {
	Elements []Expr
	ElemType Type
	Line     int
}

func (*ListLitExpr) exprNode()       {}
func (e *ListLitExpr) exprLine() int { return e.Line }

type IndexExpr struct {
	List  Expr
	Index Expr
	Line  int
}

func (*IndexExpr) exprNode()       {}
func (e *IndexExpr) exprLine() int { return e.Line }

type LenExpr struct {
	Target Expr
	Line   int
}

func (*LenExpr) exprNode()       {}
func (e *LenExpr) exprLine() int { return e.Line }

type CallExpr struct {
	Name string
	Args []Expr
	Line int
}

func (*CallExpr) exprNode()       {}
func (e *CallExpr) exprLine() int { return e.Line }

type ModCallExpr struct {
	ModAlias string
	ModName  string
	Method   string
	Args     []Expr
	Line     int
}

func (*ModCallExpr) exprNode()       {}
func (e *ModCallExpr) exprLine() int { return e.Line }

type ThunkExpr struct {
	Inner Expr
	Line  int
}

func (*ThunkExpr) exprNode()       {}
func (e *ThunkExpr) exprLine() int { return e.Line }

// ============================================================================
// 3. PARSER
// ============================================================================

type Parser struct {
	tokens []Token
	pos    int
	mods   map[string]string // alias -> canonical module name
}

func parse(tokens []Token) (*Program, error) {
	p := &Parser{
		tokens: tokens,
		mods:   map[string]string{},
	}
	prog := &Program{}
	for !p.at(TokEOF) {
		p.skipNewlines()
		if p.at(TokEOF) {
			break
		}
		if p.atWord("import") {
			imp, err := p.parseImport()
			if err != nil {
				return nil, err
			}
			p.mods[imp.Alias] = imp.Module
			prog.Imports = append(prog.Imports, imp)
			continue
		}
		if p.atWord("fn") {
			fn, err := p.parseFuncDecl()
			if err != nil {
				return nil, err
			}
			prog.Funcs = append(prog.Funcs, fn)
			continue
		}
		stmt, err := p.parseStmt(true)
		if err != nil {
			return nil, err
		}
		prog.Stmts = append(prog.Stmts, stmt)
	}
	return prog, nil
}

func (p *Parser) cur() Token {
	if p.pos >= len(p.tokens) {
		return Token{Kind: TokEOF}
	}
	return p.tokens[p.pos]
}

func (p *Parser) peek(offset int) Token {
	idx := p.pos + offset
	if idx >= len(p.tokens) {
		return Token{Kind: TokEOF}
	}
	return p.tokens[idx]
}

func (p *Parser) advance() Token {
	t := p.cur()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

func (p *Parser) at(kind TokenKind) bool {
	return p.cur().Kind == kind
}

func (p *Parser) atWord(w string) bool {
	return p.cur().Kind == TokWord && p.cur().Val == w
}

func (p *Parser) expectWord(w string) error {
	if !p.atWord(w) {
		return &CompileError{Line: p.cur().Line, Msg: fmt.Sprintf("expected %q, got %q", w, p.cur().Val)}
	}
	p.advance()
	return nil
}

func (p *Parser) expectIdent() (string, error) {
	if p.cur().Kind != TokWord {
		return "", &CompileError{Line: p.cur().Line, Msg: fmt.Sprintf("expected identifier, got %q", p.cur().Val)}
	}
	w := p.advance().Val
	return w, nil
}

func (p *Parser) expectNewlineOrEOF() error {
	if p.at(TokNewline) {
		p.advance()
		return nil
	}
	if p.at(TokEOF) {
		return nil
	}
	return &CompileError{Line: p.cur().Line, Msg: fmt.Sprintf("unexpected token %q at end of statement", p.cur().Val)}
}

func (p *Parser) skipNewlines() {
	for p.at(TokNewline) {
		p.advance()
	}
}

func (p *Parser) parseType() (Type, error) {
	tok := p.cur()
	if tok.Kind != TokWord {
		return Type{}, &CompileError{Line: tok.Line, Msg: fmt.Sprintf("expected type name, got %q", tok.Val)}
	}
	switch tok.Val {
	case "num":
		p.advance()
		return TypeNum, nil
	case "text":
		p.advance()
		return TypeText, nil
	case "flag":
		p.advance()
		return TypeFlag, nil
	case "list":
		p.advance()
		if err := p.expectWord("of"); err != nil {
			return Type{}, err
		}
		elem, err := p.parseType()
		if err != nil {
			return Type{}, err
		}
		return ListOf(elem), nil
	default:
		return Type{}, &CompileError{Line: tok.Line, Msg: fmt.Sprintf("unknown type %q (expected num, text, flag, or list of <type>)", tok.Val)}
	}
}

func (p *Parser) isTypeStart() bool {
	if p.cur().Kind != TokWord {
		return false
	}
	switch p.cur().Val {
	case "num", "text", "flag":
		return true
	case "list":
		return p.peek(1).Kind == TokWord && p.peek(1).Val == "of"
	}
	return false
}

func (p *Parser) parseImport() (*ImportStmt, error) {
	line := p.cur().Line
	p.advance() // import
	mod, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	alias := mod
	if p.atWord("as") {
		p.advance()
		alias, err = p.expectIdent()
		if err != nil {
			return nil, err
		}
	}
	if err := p.expectNewlineOrEOF(); err != nil {
		return nil, err
	}
	return &ImportStmt{Module: mod, Alias: alias, Line: line}, nil
}

func (p *Parser) parseFuncDecl() (*FuncDecl, error) {
	line := p.cur().Line
	p.advance() // fn
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}

	var params []Param
	if p.atWord("with") {
		p.advance()
		for {
			pLine := p.cur().Line
			var paramType Type
			var paramName string
			// Check if typed: "<type> <name>" or untyped (v2.0 compat): "<name>"
			if p.isTypeStart() {
				// Look ahead past type to see if an identifier follows
				saved := p.pos
				t, err := p.parseType()
				if err == nil && p.cur().Kind == TokWord && !p.atWord("and") && !p.atWord("do") && !p.atWord("returns") {
					paramType = t
					paramName = p.advance().Val
				} else {
					p.pos = saved
					paramType = TypeText
					paramName, err = p.expectIdent()
					if err != nil {
						return nil, err
					}
				}
			} else {
				paramType = TypeText // v2.0 backwards compatibility
				paramName, err = p.expectIdent()
				if err != nil {
					return nil, err
				}
			}
			params = append(params, Param{Name: paramName, Type: paramType, Line: pLine})
			if p.atWord("and") {
				p.advance()
				continue
			}
			break
		}
	}

	retType := TypeVoid
	if p.atWord("returns") {
		p.advance()
		rt, err := p.parseType()
		if err != nil {
			return nil, err
		}
		retType = rt
	}

	if err := p.expectWord("do"); err != nil {
		return nil, err
	}
	if err := p.expectNewlineOrEOF(); err != nil {
		return nil, err
	}

	body, err := p.parseBlock("end")
	if err != nil {
		return nil, err
	}
	if err := p.expectWord("end"); err != nil {
		return nil, err
	}
	if err := p.expectNewlineOrEOF(); err != nil {
		return nil, err
	}

	return &FuncDecl{
		Name:       name,
		Params:     params,
		ReturnType: retType,
		Body:       body,
		Line:       line,
	}, nil
}

func (p *Parser) parseBlock(stopWords ...string) ([]Stmt, error) {
	var stmts []Stmt
	for {
		p.skipNewlines()
		if p.at(TokEOF) {
			return nil, &CompileError{Line: p.cur().Line, Msg: fmt.Sprintf("unexpected end of file (expected %q)", strings.Join(stopWords, " or "))}
		}
		for _, sw := range stopWords {
			if p.atWord(sw) {
				return stmts, nil
			}
		}
		stmt, err := p.parseStmt(false)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, stmt)
	}
}

func (p *Parser) parseStmt(isGlobal bool) (Stmt, error) {
	tok := p.cur()
	if tok.Kind == TokWord {
		switch tok.Val {
		case "let":
			return p.parseLet(isGlobal)
		case "set":
			return p.parseSet()
		case "append":
			if !(p.peek(1).Kind == TokWord && p.peek(1).Val == "of" && p.peek(2).Kind == TokWord && p.mods[p.peek(2).Val] != "") {
				return p.parseAppend()
			}
		case "ask":
			return p.parseAsk(isGlobal)
		case "if":
			return p.parseIf()
		case "loop":
			return p.parseLoop()
		case "for":
			return p.parseForEach()
		case "give":
			return p.parseReturn()
		case "stop":
			p.advance()
			if err := p.expectNewlineOrEOF(); err != nil {
				return nil, err
			}
			return &BreakStmt{Line: tok.Line}, nil
		case "skip":
			p.advance()
			if err := p.expectNewlineOrEOF(); err != nil {
				return nil, err
			}
			return &ContinueStmt{Line: tok.Line}, nil
		}
	}

	expr, err := p.parseExpr(true)
	if err != nil {
		return nil, err
	}
	if err := p.expectNewlineOrEOF(); err != nil {
		return nil, err
	}
	return &ExprStmt{Expr: expr, Line: tok.Line}, nil
}

func (p *Parser) parseLet(isGlobal bool) (*LetStmt, error) {
	line := p.cur().Line
	p.advance() // let
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	var expType *Type
	if p.atWord("as") {
		p.advance()
		t, err := p.parseType()
		if err != nil {
			return nil, err
		}
		expType = &t
	}
	if err := p.expectWord("be"); err != nil {
		return nil, err
	}
	expr, err := p.parseExpr(true)
	if err != nil {
		return nil, err
	}
	if err := p.expectNewlineOrEOF(); err != nil {
		return nil, err
	}
	return &LetStmt{
		Name:         name,
		ExplicitType: expType,
		Expr:         expr,
		IsGlobal:     isGlobal,
		Line:         line,
	}, nil
}

func (p *Parser) parseSet() (Stmt, error) {
	line := p.cur().Line
	p.advance() // set
	if p.atWord("item") {
		// set item <index> of <list> to <value>
		p.advance()
		idxExpr, err := p.parseExpr(false)
		if err != nil {
			return nil, err
		}
		if err := p.expectWord("of"); err != nil {
			return nil, err
		}
		listName, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		if err := p.expectWord("to"); err != nil {
			return nil, err
		}
		valExpr, err := p.parseExpr(true)
		if err != nil {
			return nil, err
		}
		if err := p.expectNewlineOrEOF(); err != nil {
			return nil, err
		}
		return &SetIndexStmt{ListName: listName, Index: idxExpr, Value: valExpr, Line: line}, nil
	}

	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if err := p.expectWord("to"); err != nil {
		return nil, err
	}
	expr, err := p.parseExpr(true)
	if err != nil {
		return nil, err
	}
	if err := p.expectNewlineOrEOF(); err != nil {
		return nil, err
	}
	return &SetStmt{Name: name, Expr: expr, Line: line}, nil
}

func (p *Parser) parseAppend() (*AppendStmt, error) {
	line := p.cur().Line
	p.advance() // append
	val, err := p.parseExpr(false)
	if err != nil {
		return nil, err
	}
	if err := p.expectWord("to"); err != nil {
		return nil, err
	}
	listName, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if err := p.expectNewlineOrEOF(); err != nil {
		return nil, err
	}
	return &AppendStmt{ListName: listName, Value: val, Line: line}, nil
}

func (p *Parser) parseAsk(isGlobal bool) (*AskStmt, error) {
	line := p.cur().Line
	p.advance() // ask
	wantNum := false
	if p.atWord("num") {
		wantNum = true
		p.advance()
	} else if p.atWord("text") {
		p.advance()
	}
	prompt, err := p.parseExpr(false)
	if err != nil {
		return nil, err
	}
	if err := p.expectWord("into"); err != nil {
		return nil, err
	}
	target, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if err := p.expectNewlineOrEOF(); err != nil {
		return nil, err
	}
	return &AskStmt{Target: target, Prompt: prompt, WantNum: wantNum, IsGlobal: isGlobal, Line: line}, nil
}

func (p *Parser) parseIf() (*IfStmt, error) {
	line := p.cur().Line
	p.advance() // if
	cond, err := p.parseExpr(true)
	if err != nil {
		return nil, err
	}
	if err := p.expectWord("do"); err != nil {
		return nil, err
	}
	if err := p.expectNewlineOrEOF(); err != nil {
		return nil, err
	}

	body, err := p.parseBlock("else", "end")
	if err != nil {
		return nil, err
	}
	stmt := &IfStmt{
		Branches: []IfBranch{{Cond: cond, Body: body, Line: line}},
		Line:     line,
	}

	for p.atWord("else") {
		eLine := p.cur().Line
		p.advance() // else
		if p.atWord("if") {
			p.advance() // if
			eCond, err := p.parseExpr(true)
			if err != nil {
				return nil, err
			}
			if err := p.expectWord("do"); err != nil {
				return nil, err
			}
			if err := p.expectNewlineOrEOF(); err != nil {
				return nil, err
			}
			eBody, err := p.parseBlock("else", "end")
			if err != nil {
				return nil, err
			}
			stmt.Branches = append(stmt.Branches, IfBranch{Cond: eCond, Body: eBody, Line: eLine})
		} else {
			if err := p.expectWord("do"); err != nil {
				return nil, err
			}
			if err := p.expectNewlineOrEOF(); err != nil {
				return nil, err
			}
			elseBody, err := p.parseBlock("end")
			if err != nil {
				return nil, err
			}
			stmt.ElseBody = elseBody
			break
		}
	}

	if err := p.expectWord("end"); err != nil {
		return nil, err
	}
	if err := p.expectNewlineOrEOF(); err != nil {
		return nil, err
	}
	return stmt, nil
}

func (p *Parser) parseLoop() (Stmt, error) {
	line := p.cur().Line
	p.advance() // loop
	if p.atWord("when") {
		p.advance()
		cond, err := p.parseExpr(true)
		if err != nil {
			return nil, err
		}
		if err := p.expectWord("do"); err != nil {
			return nil, err
		}
		if err := p.expectNewlineOrEOF(); err != nil {
			return nil, err
		}
		body, err := p.parseBlock("end")
		if err != nil {
			return nil, err
		}
		if err := p.expectWord("end"); err != nil {
			return nil, err
		}
		if err := p.expectNewlineOrEOF(); err != nil {
			return nil, err
		}
		return &WhileStmt{Cond: cond, Body: body, Line: line}, nil
	}

	if p.atWord("from") {
		// loop from <start> to <end> as <var> do ... end
		p.advance()
		startExpr, err := p.parseExpr(false)
		if err != nil {
			return nil, err
		}
		if err := p.expectWord("to"); err != nil {
			return nil, err
		}
		endExpr, err := p.parseExpr(false)
		if err != nil {
			return nil, err
		}
		if err := p.expectWord("as"); err != nil {
			return nil, err
		}
		varName, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		if err := p.expectWord("do"); err != nil {
			return nil, err
		}
		if err := p.expectNewlineOrEOF(); err != nil {
			return nil, err
		}
		body, err := p.parseBlock("end")
		if err != nil {
			return nil, err
		}
		if err := p.expectWord("end"); err != nil {
			return nil, err
		}
		if err := p.expectNewlineOrEOF(); err != nil {
			return nil, err
		}
		return &RangeStmt{VarName: varName, Start: startExpr, End: endExpr, Body: body, Line: line}, nil
	}

	return nil, &CompileError{Line: line, Msg: fmt.Sprintf("expected 'when' or 'from' after 'loop', got %q", p.cur().Val)}
}

func (p *Parser) parseForEach() (*ForEachStmt, error) {
	line := p.cur().Line
	p.advance() // for
	if err := p.expectWord("each"); err != nil {
		return nil, err
	}
	varName, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if err := p.expectWord("in"); err != nil {
		return nil, err
	}
	listExpr, err := p.parseExpr(false)
	if err != nil {
		return nil, err
	}
	if err := p.expectWord("do"); err != nil {
		return nil, err
	}
	if err := p.expectNewlineOrEOF(); err != nil {
		return nil, err
	}
	body, err := p.parseBlock("end")
	if err != nil {
		return nil, err
	}
	if err := p.expectWord("end"); err != nil {
		return nil, err
	}
	if err := p.expectNewlineOrEOF(); err != nil {
		return nil, err
	}
	return &ForEachStmt{VarName: varName, ListExpr: listExpr, Body: body, Line: line}, nil
}

func (p *Parser) parseReturn() (*ReturnStmt, error) {
	line := p.cur().Line
	p.advance() // give
	if err := p.expectWord("back"); err != nil {
		return nil, err
	}
	if p.at(TokNewline) || p.at(TokEOF) {
		if err := p.expectNewlineOrEOF(); err != nil {
			return nil, err
		}
		return &ReturnStmt{Line: line}, nil
	}
	val, err := p.parseExpr(true)
	if err != nil {
		return nil, err
	}
	if err := p.expectNewlineOrEOF(); err != nil {
		return nil, err
	}
	return &ReturnStmt{Value: val, Line: line}, nil
}

// Expression parsing (recursive descent with full precedence)
// allowLogicalAnd is false inside 'and'-delimited argument/list items so 'and' separates args.

func (p *Parser) parseExpr(allowLogicalAnd bool) (Expr, error) {
	return p.parseOr(allowLogicalAnd)
}

func (p *Parser) parseOr(allowLogicalAnd bool) (Expr, error) {
	left, err := p.parseAnd(allowLogicalAnd)
	if err != nil {
		return nil, err
	}
	for p.atWord("or") {
		line := p.cur().Line
		p.advance()
		right, err := p.parseAnd(allowLogicalAnd)
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "||", Left: left, Right: right, Line: line}
	}
	return left, nil
}

func (p *Parser) parseAnd(allowLogicalAnd bool) (Expr, error) {
	left, err := p.parseComparison(allowLogicalAnd)
	if err != nil {
		return nil, err
	}
	if allowLogicalAnd {
		for p.atWord("and") {
			line := p.cur().Line
			p.advance()
			right, err := p.parseComparison(allowLogicalAnd)
			if err != nil {
				return nil, err
			}
			left = &BinaryExpr{Op: "&&", Left: left, Right: right, Line: line}
		}
	}
	return left, nil
}

func (p *Parser) parseComparison(allowLogicalAnd bool) (Expr, error) {
	left, err := p.parseAdditive(allowLogicalAnd)
	if err != nil {
		return nil, err
	}
	for p.atWord("is") {
		line := p.cur().Line
		p.advance() // is
		op := "=="
		if p.atWord("not") {
			p.advance()
			op = "!="
		} else if p.atWord("under") {
			p.advance()
			op = "<"
		} else if p.atWord("over") {
			p.advance()
			op = ">"
		} else if p.atWord("at") {
			p.advance()
			if p.atWord("least") {
				p.advance()
				op = ">="
			} else if p.atWord("most") {
				p.advance()
				op = "<="
			} else {
				return nil, &CompileError{Line: line, Msg: "expected 'least' or 'most' after 'is at'"}
			}
		}
		right, err := p.parseAdditive(allowLogicalAnd)
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right, Line: line}
	}
	return left, nil
}

func (p *Parser) parseAdditive(allowLogicalAnd bool) (Expr, error) {
	left, err := p.parseMultiplicative(allowLogicalAnd)
	if err != nil {
		return nil, err
	}
	for p.atWord("plus") || p.atWord("minus") {
		line := p.cur().Line
		op := "+"
		if p.advance().Val == "minus" {
			op = "-"
		}
		right, err := p.parseMultiplicative(allowLogicalAnd)
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right, Line: line}
	}
	return left, nil
}

func (p *Parser) parseMultiplicative(allowLogicalAnd bool) (Expr, error) {
	left, err := p.parseUnary(allowLogicalAnd)
	if err != nil {
		return nil, err
	}
	for {
		line := p.cur().Line
		if p.atWord("times") {
			p.advance()
			right, err := p.parseUnary(allowLogicalAnd)
			if err != nil {
				return nil, err
			}
			left = &BinaryExpr{Op: "*", Left: left, Right: right, Line: line}
		} else if p.atWord("divided") && p.peek(1).Kind == TokWord && p.peek(1).Val == "by" {
			p.advance()
			p.advance()
			right, err := p.parseUnary(allowLogicalAnd)
			if err != nil {
				return nil, err
			}
			left = &BinaryExpr{Op: "/", Left: left, Right: right, Line: line}
		} else if p.atWord("over") {
			p.advance()
			right, err := p.parseUnary(allowLogicalAnd)
			if err != nil {
				return nil, err
			}
			left = &BinaryExpr{Op: "/", Left: left, Right: right, Line: line}
		} else if p.atWord("mod") || p.atWord("remainder") {
			p.advance()
			right, err := p.parseUnary(allowLogicalAnd)
			if err != nil {
				return nil, err
			}
			left = &BinaryExpr{Op: "%", Left: left, Right: right, Line: line}
		} else {
			break
		}
	}
	return left, nil
}

func (p *Parser) parseUnary(allowLogicalAnd bool) (Expr, error) {
	line := p.cur().Line
	if p.atWord("negative") {
		p.advance()
		right, err := p.parseUnary(allowLogicalAnd)
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "-", Right: right, Line: line}, nil
	}
	if p.atWord("not") {
		p.advance()
		right, err := p.parseUnary(allowLogicalAnd)
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "!", Right: right, Line: line}, nil
	}
	return p.parsePrimary()
}

func (p *Parser) parsePrimary() (Expr, error) {
	tok := p.cur()
	switch tok.Kind {
	case TokNum:
		p.advance()
		v, err := strconv.ParseInt(tok.Val, 10, 64)
		if err != nil {
			return nil, &CompileError{Line: tok.Line, Msg: fmt.Sprintf("invalid integer %q", tok.Val)}
		}
		return &NumLit{Val: v, Line: tok.Line}, nil

	case TokString:
		p.advance()
		return &TextLit{Val: tok.Val, Line: tok.Line}, nil

	case TokBool:
		p.advance()
		return &FlagLit{Val: tok.Val == "true", Line: tok.Line}, nil

	case TokWord:
		switch tok.Val {
		case "group":
			// group <expr> done (zero-symbol parentheses)
			p.advance()
			inner, err := p.parseExpr(true)
			if err != nil {
				return nil, err
			}
			if err := p.expectWord("done"); err != nil {
				return nil, err
			}
			return inner, nil

		case "exec":
			// exec <fn> [with <arg1> and <arg2> ...]
			p.advance()
			fnName, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			var args []Expr
			if p.atWord("with") {
				p.advance()
				args, err = p.parseArgList()
				if err != nil {
					return nil, err
				}
			}
			return &CallExpr{Name: fnName, Args: args, Line: tok.Line}, nil

		case "item":
			// item <index> of <list>
			p.advance()
			idxExpr, err := p.parseExpr(false)
			if err != nil {
				return nil, err
			}
			if err := p.expectWord("of"); err != nil {
				return nil, err
			}
			listExpr, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			return &IndexExpr{List: listExpr, Index: idxExpr, Line: tok.Line}, nil

		case "length":
			// length of <expr>  (unless <mod> is an imported module like 'length of text contains ...')
			if p.peek(1).Kind == TokWord && p.peek(1).Val == "of" {
				if p.peek(2).Kind == TokWord {
					if _, isMod := p.mods[p.peek(2).Val]; isMod && p.peek(3).Kind == TokWord && p.peek(3).Val == "contains" {
						return p.parseModCall()
					}
				}
				p.advance() // length
				p.advance() // of
				target, err := p.parsePrimary()
				if err != nil {
					return nil, err
				}
				return &LenExpr{Target: target, Line: tok.Line}, nil
			}

		case "list":
			// list of <e1> and <e2> end
			if p.peek(1).Kind == TokWord && p.peek(1).Val == "of" {
				p.advance() // list
				p.advance() // of
				var elems []Expr
				if !p.atWord("end") {
					var err error
					elems, err = p.parseArgList()
					if err != nil {
						return nil, err
					}
				}
				if err := p.expectWord("end"); err != nil {
					return nil, err
				}
				return &ListLitExpr{Elements: elems, Line: tok.Line}, nil
			}

		case "empty":
			// empty list of <type>
			if p.peek(1).Kind == TokWord && p.peek(1).Val == "list" {
				p.advance() // empty
				p.advance() // list
				if err := p.expectWord("of"); err != nil {
					return nil, err
				}
				elemType, err := p.parseType()
				if err != nil {
					return nil, err
				}
				return &ListLitExpr{Elements: nil, ElemType: elemType, Line: tok.Line}, nil
			}
		}

		// Module call: <method> of <modAlias> [contains <args>]
		if p.peek(1).Kind == TokWord && p.peek(1).Val == "of" && p.peek(2).Kind == TokWord {
			if _, ok := p.mods[p.peek(2).Val]; ok {
				return p.parseModCall()
			}
		}

		p.advance()
		return &IdentExpr{Name: tok.Val, Line: tok.Line}, nil
	}

	return nil, &CompileError{Line: tok.Line, Msg: fmt.Sprintf("unexpected token %q in expression", tok.Val)}
}

func (p *Parser) parseModCall() (*ModCallExpr, error) {
	line := p.cur().Line
	method := p.advance().Val
	p.advance() // of
	alias := p.advance().Val
	modName := p.mods[alias]

	// Determine if this stdlib method has a fixed arity so nested calls inside
	// outer 'and'-delimited argument lists don't greedily consume outer 'and's.
	maxArgs := -1
	if methods, ok := stdlibModules[modName]; ok {
		if sig, ok := methods[method]; ok && !sig.Variadic {
			maxArgs = len(sig.Args)
		}
	}

	var args []Expr
	if p.atWord("contains") {
		p.advance()
		var err error
		if maxArgs > 0 {
			args, err = p.parseBoundedArgList(maxArgs)
		} else {
			args, err = p.parseArgList()
		}
		if err != nil {
			return nil, err
		}
	}
	return &ModCallExpr{
		ModAlias: alias,
		ModName:  modName,
		Method:   method,
		Args:     args,
		Line:     line,
	}, nil
}

func (p *Parser) parseBoundedArgList(maxArgs int) ([]Expr, error) {
	var args []Expr
	for {
		arg, err := p.parseExpr(false)
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if len(args) < maxArgs && p.atWord("and") {
			p.advance()
			continue
		}
		break
	}
	return args, nil
}

func (p *Parser) parseArgList() ([]Expr, error) {
	var args []Expr
	for {
		arg, err := p.parseExpr(false)
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if p.atWord("and") {
			p.advance()
			continue
		}
		break
	}
	return args, nil
}

// ============================================================================
// 4. TYPE CHECKER & SEMANTIC ANALYZER
// ============================================================================

type VarBinding struct {
	Type     Type
	IsGlobal bool
	Used     *bool
}

type Scope struct {
	Parent *Scope
	Vars   map[string]*VarBinding
}

func newScope(parent *Scope) *Scope {
	return &Scope{Parent: parent, Vars: map[string]*VarBinding{}}
}

func (s *Scope) lookup(name string) *VarBinding {
	for cur := s; cur != nil; cur = cur.Parent {
		if b, ok := cur.Vars[name]; ok {
			return b
		}
	}
	return nil
}

type ModMethodSig struct {
	Args     []Type // nil if variadic
	Variadic bool
	Ret      Type
}

var stdlibModules = map[string]map[string]ModMethodSig{
	"print": {
		"ln":  {Variadic: true, Ret: TypeVoid},
		"raw": {Variadic: true, Ret: TypeVoid},
	},
	"math": {
		"abs":   {Args: []Type{TypeNum}, Ret: TypeNum},
		"min":   {Args: []Type{TypeNum, TypeNum}, Ret: TypeNum},
		"max":   {Args: []Type{TypeNum, TypeNum}, Ret: TypeNum},
		"pow":   {Args: []Type{TypeNum, TypeNum}, Ret: TypeNum},
		"sqrt":  {Args: []Type{TypeNum}, Ret: TypeNum},
		"rand":  {Args: []Type{TypeNum}, Ret: TypeNum},
		"clamp": {Args: []Type{TypeNum, TypeNum, TypeNum}, Ret: TypeNum},
	},
	"text": {
		"upper":   {Args: []Type{TypeText}, Ret: TypeText},
		"lower":   {Args: []Type{TypeText}, Ret: TypeText},
		"trim":    {Args: []Type{TypeText}, Ret: TypeText},
		"join":    {Args: []Type{ListOf(TypeText), TypeText}, Ret: TypeText},
		"split":   {Args: []Type{TypeText, TypeText}, Ret: ListOf(TypeText)},
		"has":     {Args: []Type{TypeText, TypeText}, Ret: TypeFlag},
		"replace": {Args: []Type{TypeText, TypeText, TypeText}, Ret: TypeText},
		"fromnum": {Args: []Type{TypeNum}, Ret: TypeText},
		"tonum":   {Args: []Type{TypeText}, Ret: TypeNum},
		"length":  {Args: []Type{TypeText}, Ret: TypeNum},
	},
	"file": {
		"read":   {Args: []Type{TypeText}, Ret: TypeText},
		"write":  {Args: []Type{TypeText, TypeText}, Ret: TypeVoid},
		"append": {Args: []Type{TypeText, TypeText}, Ret: TypeVoid},
		"exists": {Args: []Type{TypeText}, Ret: TypeFlag},
	},
	"time": {
		"now":   {Args: []Type{}, Ret: TypeNum},
		"sleep": {Args: []Type{TypeNum}, Ret: TypeVoid},
	},
}

type Checker struct {
	prog      *Program
	funcs     map[string]*FuncDecl
	global    *Scope
	curRet    *Type
	loopDepth int
}

func checkProgram(prog *Program) error {
	c := &Checker{
		prog:   prog,
		funcs:  map[string]*FuncDecl{},
		global: newScope(nil),
	}

	// Pass 1: Validate imports and register all function signatures
	for _, imp := range prog.Imports {
		if _, ok := stdlibModules[imp.Module]; !ok {
			return &CompileError{
				Line: imp.Line,
				Msg:  fmt.Sprintf("unknown standard library module %q (available: print, math, text, file, time)", imp.Module),
			}
		}
	}
	for _, fn := range prog.Funcs {
		if _, exists := c.funcs[fn.Name]; exists {
			return &CompileError{Line: fn.Line, Msg: fmt.Sprintf("duplicate function %q", fn.Name)}
		}
		c.funcs[fn.Name] = fn
	}

	// Pass 2: Check top-level variable declarations (let / ask) so functions can reference globals
	for _, stmt := range prog.Stmts {
		switch s := stmt.(type) {
		case *LetStmt:
			if err := c.checkStmt(s, c.global); err != nil {
				return err
			}
		case *AskStmt:
			if err := c.checkStmt(s, c.global); err != nil {
				return err
			}
		}
	}

	// Pass 3: Check all function bodies
	for _, fn := range prog.Funcs {
		fnScope := newScope(c.global)
		for _, param := range fn.Params {
			used := true // Go allows unused function parameters
			fnScope.Vars[param.Name] = &VarBinding{Type: param.Type, Used: &used}
		}
		rt := fn.ReturnType
		c.curRet = &rt
		for _, stmt := range fn.Body {
			if err := c.checkStmt(stmt, fnScope); err != nil {
				return err
			}
		}
		c.curRet = nil
	}

	// Pass 4: Check remaining top-level statements
	for _, stmt := range prog.Stmts {
		switch stmt.(type) {
		case *LetStmt, *AskStmt:
			continue
		default:
			if err := c.checkStmt(stmt, c.global); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Checker) checkStmt(stmt Stmt, scope *Scope) error {
	switch s := stmt.(type) {
	case *LetStmt:
		if _, exists := scope.Vars[s.Name]; exists {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("variable %q is already declared in this scope (use 'set %s to ...' to reassign)", s.Name, s.Name)}
		}
		t, err := c.checkExpr(s.Expr, scope)
		if err != nil {
			return err
		}
		// Lazy Action Binding: void calls become thunks (func())
		if t.Kind == TyVoid {
			s.Expr = &ThunkExpr{Inner: s.Expr, Line: s.Line}
			t = TypeAction
		}
		if s.ExplicitType != nil && !s.ExplicitType.Equal(t) {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("type mismatch in 'let %s': declared %s, got %s", s.Name, s.ExplicitType, t)}
		}
		s.ResolvedType = t
		scope.Vars[s.Name] = &VarBinding{Type: t, IsGlobal: s.IsGlobal, Used: &s.Used}
		return nil

	case *SetStmt:
		b := scope.lookup(s.Name)
		if b == nil {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("cannot set undeclared variable %q", s.Name)}
		}
		*b.Used = true
		t, err := c.checkExpr(s.Expr, scope)
		if err != nil {
			return err
		}
		if t.Kind == TyVoid && b.Type.Kind == TyAction {
			s.Expr = &ThunkExpr{Inner: s.Expr, Line: s.Line}
			t = TypeAction
		}
		if !b.Type.Equal(t) {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("cannot assign %s to variable %q of type %s", t, s.Name, b.Type)}
		}
		return nil

	case *SetIndexStmt:
		b := scope.lookup(s.ListName)
		if b == nil {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("undeclared list %q", s.ListName)}
		}
		*b.Used = true
		if b.Type.Kind != TyList {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("%q is %s, not a list", s.ListName, b.Type)}
		}
		idxT, err := c.checkExpr(s.Index, scope)
		if err != nil {
			return err
		}
		if idxT.Kind != TyNum {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("list index must be num, got %s", idxT)}
		}
		valT, err := c.checkExpr(s.Value, scope)
		if err != nil {
			return err
		}
		if !b.Type.Elem.Equal(valT) {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("cannot store %s in %s", valT, b.Type)}
		}
		return nil

	case *AppendStmt:
		b := scope.lookup(s.ListName)
		if b == nil {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("undeclared list %q", s.ListName)}
		}
		*b.Used = true
		if b.Type.Kind != TyList {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("cannot append to %q of type %s", s.ListName, b.Type)}
		}
		valT, err := c.checkExpr(s.Value, scope)
		if err != nil {
			return err
		}
		if !b.Type.Elem.Equal(valT) {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("cannot append %s to %s", valT, b.Type)}
		}
		return nil

	case *AskStmt:
		pt, err := c.checkExpr(s.Prompt, scope)
		if err != nil {
			return err
		}
		if pt.Kind != TyText {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("ask prompt must be text, got %s", pt)}
		}
		targetType := TypeText
		if s.WantNum {
			targetType = TypeNum
		}
		b := scope.lookup(s.Target)
		if b == nil {
			s.IsNew = true
			scope.Vars[s.Target] = &VarBinding{Type: targetType, IsGlobal: s.IsGlobal, Used: &s.Used}
		} else {
			*b.Used = true
			if !b.Type.Equal(targetType) {
				return &CompileError{Line: s.Line, Msg: fmt.Sprintf("ask into %q expects %s, but variable is %s", s.Target, targetType, b.Type)}
			}
		}
		return nil

	case *IfStmt:
		for _, br := range s.Branches {
			ct, err := c.checkExpr(br.Cond, scope)
			if err != nil {
				return err
			}
			if ct.Kind != TyFlag {
				return &CompileError{Line: br.Line, Msg: fmt.Sprintf("if condition must be flag, got %s", ct)}
			}
			brScope := newScope(scope)
			for _, st := range br.Body {
				if err := c.checkStmt(st, brScope); err != nil {
					return err
				}
			}
		}
		if len(s.ElseBody) > 0 {
			elseScope := newScope(scope)
			for _, st := range s.ElseBody {
				if err := c.checkStmt(st, elseScope); err != nil {
					return err
				}
			}
		}
		return nil

	case *WhileStmt:
		ct, err := c.checkExpr(s.Cond, scope)
		if err != nil {
			return err
		}
		if ct.Kind != TyFlag {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("loop condition must be flag, got %s", ct)}
		}
		c.loopDepth++
		loopScope := newScope(scope)
		for _, st := range s.Body {
			if err := c.checkStmt(st, loopScope); err != nil {
				c.loopDepth--
				return err
			}
		}
		c.loopDepth--
		return nil

	case *RangeStmt:
		st, err := c.checkExpr(s.Start, scope)
		if err != nil {
			return err
		}
		et, err := c.checkExpr(s.End, scope)
		if err != nil {
			return err
		}
		if st.Kind != TyNum || et.Kind != TyNum {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("loop from ... to ... requires num bounds, got %s and %s", st, et)}
		}
		c.loopDepth++
		loopScope := newScope(scope)
		loopScope.Vars[s.VarName] = &VarBinding{Type: TypeNum, Used: &s.Used}
		for _, stmt := range s.Body {
			if err := c.checkStmt(stmt, loopScope); err != nil {
				c.loopDepth--
				return err
			}
		}
		c.loopDepth--
		return nil

	case *ForEachStmt:
		lt, err := c.checkExpr(s.ListExpr, scope)
		if err != nil {
			return err
		}
		if lt.Kind != TyList {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("for each requires a list, got %s", lt)}
		}
		c.loopDepth++
		loopScope := newScope(scope)
		loopScope.Vars[s.VarName] = &VarBinding{Type: *lt.Elem, Used: &s.Used}
		for _, stmt := range s.Body {
			if err := c.checkStmt(stmt, loopScope); err != nil {
				c.loopDepth--
				return err
			}
		}
		c.loopDepth--
		return nil

	case *ReturnStmt:
		if c.curRet == nil {
			return &CompileError{Line: s.Line, Msg: "'give back' can only be used inside a function"}
		}
		if s.Value == nil {
			if c.curRet.Kind != TyVoid {
				return &CompileError{Line: s.Line, Msg: fmt.Sprintf("function expects return value of type %s", *c.curRet)}
			}
			return nil
		}
		vt, err := c.checkExpr(s.Value, scope)
		if err != nil {
			return err
		}
		if !c.curRet.Equal(vt) {
			return &CompileError{Line: s.Line, Msg: fmt.Sprintf("return type mismatch: function returns %s, got %s", *c.curRet, vt)}
		}
		return nil

	case *BreakStmt:
		if c.loopDepth == 0 {
			return &CompileError{Line: s.Line, Msg: "'stop' can only be used inside a loop"}
		}
		return nil

	case *ContinueStmt:
		if c.loopDepth == 0 {
			return &CompileError{Line: s.Line, Msg: "'skip' can only be used inside a loop"}
		}
		return nil

	case *ExprStmt:
		_, err := c.checkExpr(s.Expr, scope)
		return err
	}
	return nil
}

func (c *Checker) checkExpr(expr Expr, scope *Scope) (Type, error) {
	switch e := expr.(type) {
	case *NumLit:
		return TypeNum, nil
	case *TextLit:
		return TypeText, nil
	case *FlagLit:
		return TypeFlag, nil

	case *IdentExpr:
		b := scope.lookup(e.Name)
		if b == nil {
			return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("undeclared identifier %q", e.Name)}
		}
		*b.Used = true
		e.ResolvedType = b.Type
		return b.Type, nil

	case *UnaryExpr:
		rt, err := c.checkExpr(e.Right, scope)
		if err != nil {
			return Type{}, err
		}
		if e.Op == "-" {
			if rt.Kind != TyNum {
				return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("'negative' requires num, got %s", rt)}
			}
			return TypeNum, nil
		}
		if e.Op == "!" {
			if rt.Kind != TyFlag {
				return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("'not' requires flag, got %s", rt)}
			}
			return TypeFlag, nil
		}

	case *BinaryExpr:
		lt, err := c.checkExpr(e.Left, scope)
		if err != nil {
			return Type{}, err
		}
		rt, err := c.checkExpr(e.Right, scope)
		if err != nil {
			return Type{}, err
		}
		switch e.Op {
		case "+":
			if lt.Kind == TyNum && rt.Kind == TyNum {
				return TypeNum, nil
			}
			if lt.Kind == TyText && rt.Kind == TyText {
				return TypeText, nil
			}
			return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("'plus' requires matching num or text operands, got %s and %s", lt, rt)}
		case "-", "*", "/", "%":
			if lt.Kind != TyNum || rt.Kind != TyNum {
				return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("arithmetic operator %q requires num operands, got %s and %s", e.Op, lt, rt)}
			}
			return TypeNum, nil
		case "==", "!=":
			if !lt.Equal(rt) || lt.Kind == TyList || lt.Kind == TyAction || lt.Kind == TyVoid {
				return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("cannot compare %s and %s", lt, rt)}
			}
			return TypeFlag, nil
		case "<", ">", "<=", ">=":
			if (lt.Kind == TyNum && rt.Kind == TyNum) || (lt.Kind == TyText && rt.Kind == TyText) {
				return TypeFlag, nil
			}
			return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("ordered comparison requires matching num or text operands, got %s and %s", lt, rt)}
		case "&&", "||":
			if lt.Kind != TyFlag || rt.Kind != TyFlag {
				return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("logical operator requires flag operands, got %s and %s", lt, rt)}
			}
			return TypeFlag, nil
		}

	case *ListLitExpr:
		if len(e.Elements) == 0 {
			if e.ElemType.Kind == TyUnknown {
				return Type{}, &CompileError{Line: e.Line, Msg: "empty list requires a type: use 'empty list of <type>'"}
			}
			return ListOf(e.ElemType), nil
		}
		firstT, err := c.checkExpr(e.Elements[0], scope)
		if err != nil {
			return Type{}, err
		}
		for i := 1; i < len(e.Elements); i++ {
			et, err := c.checkExpr(e.Elements[i], scope)
			if err != nil {
				return Type{}, err
			}
			if !firstT.Equal(et) {
				return Type{}, &CompileError{Line: e.Elements[i].exprLine(), Msg: fmt.Sprintf("list element %d has type %s, expected %s", i, et, firstT)}
			}
		}
		e.ElemType = firstT
		return ListOf(firstT), nil

	case *IndexExpr:
		lt, err := c.checkExpr(e.List, scope)
		if err != nil {
			return Type{}, err
		}
		if lt.Kind != TyList {
			return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("cannot index %s (expected list)", lt)}
		}
		it, err := c.checkExpr(e.Index, scope)
		if err != nil {
			return Type{}, err
		}
		if it.Kind != TyNum {
			return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("list index must be num, got %s", it)}
		}
		return *lt.Elem, nil

	case *LenExpr:
		tt, err := c.checkExpr(e.Target, scope)
		if err != nil {
			return Type{}, err
		}
		if tt.Kind != TyList && tt.Kind != TyText {
			return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("'length of' requires list or text, got %s", tt)}
		}
		return TypeNum, nil

	case *CallExpr:
		fn, ok := c.funcs[e.Name]
		if !ok {
			return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("undeclared function %q", e.Name)}
		}
		if len(e.Args) != len(fn.Params) {
			return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("function %q expects %d argument(s), got %d", e.Name, len(fn.Params), len(e.Args))}
		}
		for i, arg := range e.Args {
			at, err := c.checkExpr(arg, scope)
			if err != nil {
				return Type{}, err
			}
			if !fn.Params[i].Type.Equal(at) {
				return Type{}, &CompileError{Line: arg.exprLine(), Msg: fmt.Sprintf("argument %d to %q expects %s, got %s", i+1, e.Name, fn.Params[i].Type, at)}
			}
		}
		return fn.ReturnType, nil

	case *ModCallExpr:
		methods, ok := stdlibModules[e.ModName]
		if !ok {
			return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("unknown module %q", e.ModName)}
		}
		sig, ok := methods[e.Method]
		if !ok {
			return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("module %q has no method %q", e.ModName, e.Method)}
		}
		if sig.Variadic {
			for _, arg := range e.Args {
				at, err := c.checkExpr(arg, scope)
				if err != nil {
					return Type{}, err
				}
				if at.Kind == TyVoid {
					return Type{}, &CompileError{Line: arg.exprLine(), Msg: "cannot pass void expression as argument"}
				}
			}
			return sig.Ret, nil
		}
		if len(e.Args) != len(sig.Args) {
			return Type{}, &CompileError{Line: e.Line, Msg: fmt.Sprintf("%s of %s expects %d argument(s), got %d", e.Method, e.ModName, len(sig.Args), len(e.Args))}
		}
		for i, arg := range e.Args {
			at, err := c.checkExpr(arg, scope)
			if err != nil {
				return Type{}, err
			}
			if !sig.Args[i].Equal(at) {
				return Type{}, &CompileError{Line: arg.exprLine(), Msg: fmt.Sprintf("argument %d to %s of %s expects %s, got %s", i+1, e.Method, e.ModName, sig.Args[i], at)}
			}
		}
		return sig.Ret, nil

	case *ThunkExpr:
		if _, err := c.checkExpr(e.Inner, scope); err != nil {
			return Type{}, err
		}
		return TypeAction, nil
	}

	return Type{}, &CompileError{Line: expr.exprLine(), Msg: "unsupported expression"}
}

// ============================================================================
// 5. GO CODE GENERATOR
// ============================================================================

var goReserved = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
	// Predeclared identifiers & entry point
	"main": true, "init": true, "string": true, "int": true, "int64": true,
	"bool": true, "len": true, "cap": true, "append": true, "copy": true,
	"delete": true, "make": true, "new": true, "panic": true, "print": true,
	"println": true, "fmt": true, "os": true, "math": true, "strings": true,
	"strconv": true, "time": true, "bufio": true, "rand": true,
}

func goIdent(name string) string {
	if goReserved[name] || strings.HasPrefix(name, "fks_") {
		return "fks_" + name
	}
	return name
}

type CodeGen struct {
	imports map[string]bool
	helpers map[string]bool
}

func generateGo(prog *Program) (string, error) {
	g := &CodeGen{
		imports: map[string]bool{},
		helpers: map[string]bool{},
	}

	var globalsBuf, funcsBuf, mainBuf strings.Builder

	// Package-level variable declarations for top-level let / ask
	for _, stmt := range prog.Stmts {
		switch s := stmt.(type) {
		case *LetStmt:
			globalsBuf.WriteString(fmt.Sprintf("var %s %s\n", goIdent(s.Name), s.ResolvedType.GoType()))
		case *AskStmt:
			if s.IsNew {
				t := TypeText
				if s.WantNum {
					t = TypeNum
				}
				globalsBuf.WriteString(fmt.Sprintf("var %s %s\n", goIdent(s.Target), t.GoType()))
			}
		}
	}

	// Functions
	for _, fn := range prog.Funcs {
		var params []string
		for _, p := range fn.Params {
			params = append(params, fmt.Sprintf("%s %s", goIdent(p.Name), p.Type.GoType()))
		}
		ret := ""
		if fn.ReturnType.Kind != TyVoid {
			ret = " " + fn.ReturnType.GoType()
		}
		funcsBuf.WriteString(fmt.Sprintf("func %s(%s)%s {\n", goIdent(fn.Name), strings.Join(params, ", "), ret))
		for _, st := range fn.Body {
			g.emitStmt(&funcsBuf, st)
		}
		funcsBuf.WriteString("}\n\n")
	}

	// Main body (all top-level statements in exact source order)
	for _, st := range prog.Stmts {
		g.emitStmt(&mainBuf, st)
	}

	// Assemble final Go source
	var out strings.Builder
	out.WriteString("package main\n\n")

	if len(g.imports) > 0 {
		var imps []string
		for imp := range g.imports {
			imps = append(imps, imp)
		}
		sort.Strings(imps)
		out.WriteString("import (\n")
		for _, imp := range imps {
			out.WriteString(fmt.Sprintf("\t%q\n", imp))
		}
		out.WriteString(")\n\n")
	}

	if len(g.helpers) > 0 {
		out.WriteString(g.emitHelpers())
	}

	if globalsBuf.Len() > 0 {
		out.WriteString(globalsBuf.String())
		out.WriteString("\n")
	}

	out.WriteString(funcsBuf.String())
	out.WriteString("func main() {\n")
	out.WriteString(mainBuf.String())
	out.WriteString("}\n")

	formatted, err := format.Source([]byte(out.String()))
	if err != nil {
		return out.String(), fmt.Errorf("internal codegen formatting error: %w\n--- generated Go ---\n%s", err, out.String())
	}
	return string(formatted), nil
}

func (g *CodeGen) emitStmt(out *strings.Builder, stmt Stmt) {
	switch s := stmt.(type) {
	case *LetStmt:
		if s.IsGlobal {
			out.WriteString(fmt.Sprintf("%s = %s\n", goIdent(s.Name), g.emitExpr(s.Expr)))
		} else {
			out.WriteString(fmt.Sprintf("var %s %s = %s\n", goIdent(s.Name), s.ResolvedType.GoType(), g.emitExpr(s.Expr)))
			if !s.Used {
				out.WriteString(fmt.Sprintf("_ = %s\n", goIdent(s.Name)))
			}
		}

	case *SetStmt:
		out.WriteString(fmt.Sprintf("%s = %s\n", goIdent(s.Name), g.emitExpr(s.Expr)))

	case *SetIndexStmt:
		out.WriteString(fmt.Sprintf("%s[%s] = %s\n", goIdent(s.ListName), g.emitExpr(s.Index), g.emitExpr(s.Value)))

	case *AppendStmt:
		out.WriteString(fmt.Sprintf("%s = append(%s, %s)\n", goIdent(s.ListName), goIdent(s.ListName), g.emitExpr(s.Value)))

	case *AskStmt:
		g.imports["fmt"] = true
		g.imports["bufio"] = true
		g.imports["os"] = true
		g.helpers["scanner"] = true
		out.WriteString(fmt.Sprintf("fmt.Print(%s)\n", g.emitExpr(s.Prompt)))
		rhs := "fks_read_line()"
		if s.WantNum {
			g.imports["strconv"] = true
			g.imports["strings"] = true
			g.helpers["tonum"] = true
			rhs = "fks_tonum(fks_read_line())"
		}
		if s.IsNew && !s.IsGlobal {
			out.WriteString(fmt.Sprintf("%s := %s\n", goIdent(s.Target), rhs))
			if !s.Used {
				out.WriteString(fmt.Sprintf("_ = %s\n", goIdent(s.Target)))
			}
		} else {
			out.WriteString(fmt.Sprintf("%s = %s\n", goIdent(s.Target), rhs))
		}

	case *IfStmt:
		for i, br := range s.Branches {
			if i == 0 {
				out.WriteString(fmt.Sprintf("if %s {\n", g.emitExpr(br.Cond)))
			} else {
				out.WriteString(fmt.Sprintf("} else if %s {\n", g.emitExpr(br.Cond)))
			}
			for _, st := range br.Body {
				g.emitStmt(out, st)
			}
		}
		if len(s.ElseBody) > 0 {
			out.WriteString("} else {\n")
			for _, st := range s.ElseBody {
				g.emitStmt(out, st)
			}
		}
		out.WriteString("}\n")

	case *WhileStmt:
		out.WriteString(fmt.Sprintf("for %s {\n", g.emitExpr(s.Cond)))
		for _, st := range s.Body {
			g.emitStmt(out, st)
		}
		out.WriteString("}\n")

	case *RangeStmt:
		v := goIdent(s.VarName)
		out.WriteString(fmt.Sprintf("for %s := %s; %s < %s; %s++ {\n", v, g.emitExpr(s.Start), v, g.emitExpr(s.End), v))
		if !s.Used {
			out.WriteString(fmt.Sprintf("_ = %s\n", v))
		}
		for _, st := range s.Body {
			g.emitStmt(out, st)
		}
		out.WriteString("}\n")

	case *ForEachStmt:
		v := goIdent(s.VarName)
		if !s.Used {
			v = "_"
		}
		out.WriteString(fmt.Sprintf("for _, %s := range %s {\n", v, g.emitExpr(s.ListExpr)))
		for _, st := range s.Body {
			g.emitStmt(out, st)
		}
		out.WriteString("}\n")

	case *ReturnStmt:
		if s.Value == nil {
			out.WriteString("return\n")
		} else {
			out.WriteString(fmt.Sprintf("return %s\n", g.emitExpr(s.Value)))
		}

	case *BreakStmt:
		out.WriteString("break\n")

	case *ContinueStmt:
		out.WriteString("continue\n")

	case *ExprStmt:
		if id, ok := s.Expr.(*IdentExpr); ok && id.ResolvedType.Kind == TyAction {
			out.WriteString(fmt.Sprintf("%s()\n", goIdent(id.Name)))
		} else {
			out.WriteString(g.emitExpr(s.Expr) + "\n")
		}
	}
}

func (g *CodeGen) emitExpr(expr Expr) string {
	switch e := expr.(type) {
	case *NumLit:
		return fmt.Sprintf("int64(%d)", e.Val)
	case *TextLit:
		return strconv.Quote(e.Val)
	case *FlagLit:
		if e.Val {
			return "true"
		}
		return "false"
	case *IdentExpr:
		return goIdent(e.Name)
	case *UnaryExpr:
		return fmt.Sprintf("(%s%s)", e.Op, g.emitExpr(e.Right))
	case *BinaryExpr:
		return fmt.Sprintf("(%s %s %s)", g.emitExpr(e.Left), e.Op, g.emitExpr(e.Right))
	case *ListLitExpr:
		var elems []string
		for _, el := range e.Elements {
			elems = append(elems, g.emitExpr(el))
		}
		return fmt.Sprintf("[]%s{%s}", e.ElemType.GoType(), strings.Join(elems, ", "))
	case *IndexExpr:
		return fmt.Sprintf("%s[%s]", g.emitExpr(e.List), g.emitExpr(e.Index))
	case *LenExpr:
		return fmt.Sprintf("int64(len(%s))", g.emitExpr(e.Target))
	case *CallExpr:
		var args []string
		for _, a := range e.Args {
			args = append(args, g.emitExpr(a))
		}
		return fmt.Sprintf("%s(%s)", goIdent(e.Name), strings.Join(args, ", "))
	case *ThunkExpr:
		return fmt.Sprintf("func() { %s }", g.emitExpr(e.Inner))
	case *ModCallExpr:
		return g.emitModCall(e)
	}
	return ""
}

func (g *CodeGen) emitModCall(e *ModCallExpr) string {
	var args []string
	for _, a := range e.Args {
		args = append(args, g.emitExpr(a))
	}

	switch e.ModName {
	case "print":
		g.imports["fmt"] = true
		if e.Method == "ln" {
			return fmt.Sprintf("fmt.Println(%s)", strings.Join(args, ", "))
		}
		return fmt.Sprintf("fmt.Print(%s)", strings.Join(args, ", "))

	case "math":
		switch e.Method {
		case "abs":
			g.helpers["abs"] = true
			return fmt.Sprintf("fks_abs(%s)", args[0])
		case "min":
			return fmt.Sprintf("min(%s, %s)", args[0], args[1])
		case "max":
			return fmt.Sprintf("max(%s, %s)", args[0], args[1])
		case "pow":
			g.helpers["pow"] = true
			return fmt.Sprintf("fks_pow(%s, %s)", args[0], args[1])
		case "sqrt":
			g.imports["math"] = true
			return fmt.Sprintf("int64(math.Sqrt(float64(%s)))", args[0])
		case "rand":
			g.imports["math/rand/v2"] = true
			return fmt.Sprintf("rand.Int64N(%s)", args[0])
		case "clamp":
			return fmt.Sprintf("min(max(%s, %s), %s)", args[0], args[1], args[2])
		}

	case "text":
		switch e.Method {
		case "upper":
			g.imports["strings"] = true
			return fmt.Sprintf("strings.ToUpper(%s)", args[0])
		case "lower":
			g.imports["strings"] = true
			return fmt.Sprintf("strings.ToLower(%s)", args[0])
		case "trim":
			g.imports["strings"] = true
			return fmt.Sprintf("strings.TrimSpace(%s)", args[0])
		case "join":
			g.imports["strings"] = true
			return fmt.Sprintf("strings.Join(%s, %s)", args[0], args[1])
		case "split":
			g.imports["strings"] = true
			return fmt.Sprintf("strings.Split(%s, %s)", args[0], args[1])
		case "has":
			g.imports["strings"] = true
			return fmt.Sprintf("strings.Contains(%s, %s)", args[0], args[1])
		case "replace":
			g.imports["strings"] = true
			return fmt.Sprintf("strings.ReplaceAll(%s, %s, %s)", args[0], args[1], args[2])
		case "fromnum":
			g.imports["strconv"] = true
			return fmt.Sprintf("strconv.FormatInt(%s, 10)", args[0])
		case "tonum":
			g.imports["strconv"] = true
			g.imports["strings"] = true
			g.helpers["tonum"] = true
			return fmt.Sprintf("fks_tonum(%s)", args[0])
		case "length":
			return fmt.Sprintf("int64(len(%s))", args[0])
		}

	case "file":
		g.imports["os"] = true
		switch e.Method {
		case "read":
			g.helpers["file_read"] = true
			return fmt.Sprintf("fks_file_read(%s)", args[0])
		case "write":
			g.helpers["file_write"] = true
			return fmt.Sprintf("fks_file_write(%s, %s)", args[0], args[1])
		case "append":
			g.helpers["file_append"] = true
			return fmt.Sprintf("fks_file_append(%s, %s)", args[0], args[1])
		case "exists":
			g.helpers["file_exists"] = true
			return fmt.Sprintf("fks_file_exists(%s)", args[0])
		}

	case "time":
		g.imports["time"] = true
		switch e.Method {
		case "now":
			return "time.Now().UnixMilli()"
		case "sleep":
			return fmt.Sprintf("time.Sleep(time.Duration(%s) * time.Millisecond)", args[0])
		}
	}
	return ""
}

func (g *CodeGen) emitHelpers() string {
	var b strings.Builder
	if g.helpers["scanner"] {
		b.WriteString(`var fks_scanner = bufio.NewScanner(os.Stdin)

func fks_read_line() string {
	if fks_scanner.Scan() {
		return fks_scanner.Text()
	}
	return ""
}

`)
	}
	if g.helpers["abs"] {
		b.WriteString(`func fks_abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

`)
	}
	if g.helpers["pow"] {
		b.WriteString(`func fks_pow(base, exp int64) int64 {
	if exp < 0 {
		return 0
	}
	var res int64 = 1
	for exp > 0 {
		if exp&1 == 1 {
			res *= base
		}
		base *= base
		exp >>= 1
	}
	return res
}

`)
	}
	if g.helpers["tonum"] {
		b.WriteString(`func fks_tonum(s string) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

`)
	}
	if g.helpers["file_read"] {
		b.WriteString(`func fks_file_read(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

`)
	}
	if g.helpers["file_write"] {
		b.WriteString(`func fks_file_write(path, content string) {
	_ = os.WriteFile(path, []byte(content), 0644)
}

`)
	}
	if g.helpers["file_append"] {
		b.WriteString(`func fks_file_append(path, content string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(content)
}

`)
	}
	if g.helpers["file_exists"] {
		b.WriteString(`func fks_file_exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

`)
	}
	return b.String()
}

// ============================================================================
// 6. COMPILER PIPELINE & CLI
// ============================================================================

func compileSource(src string) (string, error) {
	tokens, err := lex(src)
	if err != nil {
		return "", err
	}
	prog, err := parse(tokens)
	if err != nil {
		return "", err
	}
	if err := checkProgram(prog); err != nil {
		return "", err
	}
	return generateGo(prog)
}

func formatErrorWithContext(file, src string, err error) string {
	ce, ok := err.(*CompileError)
	if !ok || ce.Line <= 0 {
		return fmt.Sprintf("%s: %s", file, err.Error())
	}
	lines := strings.Split(src, "\n")
	snippet := ""
	if ce.Line <= len(lines) {
		snippet = "\n  | " + strings.TrimRight(lines[ce.Line-1], "\r")
	}
	return fmt.Sprintf("%s:%d: error: %s%s", file, ce.Line, ce.Msg, snippet)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `FakeScript Compiler (fksc v%s) — Zero-Symbol AOT Language

Usage:
  fksc <program.fks>          Compile program to a native binary
  fksc build <program.fks>    Compile program to a native binary
  fksc run <program.fks>      Compile and execute immediately
  fksc emit <program.fks>     Print generated Go source code
  fksc check <program.fks>    Lex, parse, and type-check without building
  fksc version                Print compiler version
`, fkscVersion)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := "build"
	filePath := ""

	switch os.Args[1] {
	case "version":
		fmt.Printf("fksc v%s (%s/%s)\n", fkscVersion, runtime.GOOS, runtime.GOARCH)
		return
	case "help":
		printUsage()
		return
	case "build", "run", "emit", "check":
		cmd = os.Args[1]
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Missing .fks file for '%s'\n", cmd)
			os.Exit(1)
		}
		filePath = os.Args[2]
	default:
		filePath = os.Args[1]
	}

	if !strings.HasSuffix(filePath, ".fks") {
		fmt.Fprintf(os.Stderr, "\033[31mExpected a .fks source file, got %q\033[0m\n", filePath)
		os.Exit(1)
	}

	rawSrc, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31mError reading file: %v\033[0m\n", err)
		os.Exit(1)
	}
	src := string(rawSrc)

	goCode, err := compileSource(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, formatErrorWithContext(filePath, src, err))
		os.Exit(1)
	}

	switch cmd {
	case "check":
		fmt.Printf("OK %s\n", filePath)
		return

	case "emit":
		fmt.Print(goCode)
		return

	case "build", "run":
		stem := strings.TrimSuffix(filePath, ".fks")
		tmpDir, err := os.MkdirTemp("", "fksc_build_*")
		if err != nil {
			fmt.Fprintln(os.Stderr, "\033[31mTemp dir error: %v\033[0m\n", err)
			os.Exit(1)
		}
		defer os.RemoveAll(tmpDir)

		tmpGo := filepath.Join(tmpDir, "main.go")
		if err := os.WriteFile(tmpGo, []byte(goCode), 0644); err != nil {
			fmt.Fprintln(os.Stderr, "\033[31mWrite temp Go error: %v\033[0m\n", err)
			os.Exit(1)
		}

		ext := ""
		if runtime.GOOS == "windows" {
			ext = ".exe"
		}
		var outBin string
		if cmd == "run" {
			outBin = filepath.Join(tmpDir, "fks_run"+ext)
		} else {
			outBin = stem + ext
		}

		buildCmd := exec.Command("go", "build", "-o", outBin, tmpGo)
		buildCmd.Stderr = os.Stderr
		if err := buildCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "\033[31mNative build failed: %v\033[0m\n", err)
			os.Exit(1)
		}

		if cmd == "run" {
			runCmd := exec.Command(outBin)
			runCmd.Stdin = os.Stdin
			runCmd.Stdout = os.Stdout
			runCmd.Stderr = os.Stderr
			if err := runCmd.Run(); err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					os.Exit(exitErr.ExitCode())
				}
				fmt.Fprintf(os.Stderr, "\033[31mNative build failed: %v\033[0m\n", err)
				os.Exit(1)
			}
			return
		}

		fmt.Printf("\033[1;32mCompiled\033[0m %s -> %s\n", filePath, outBin)
	}
}
