package cst

import "rotor/internal/luau/lex"

const unaryPrec = 7

// binPrec returns the binding precedence of a binary operator token and whether it
// is right-associative. ok is false for non-operators.
func binPrec(t lex.Token) (prec int, rightAssoc, ok bool) {
	if t.Kind == lex.Name {
		switch t.Text {
		case "or":
			return 1, false, true
		case "and":
			return 2, false, true
		}
		return 0, false, false
	}
	if t.Kind == lex.Symbol {
		switch t.Text {
		case "<", ">", "<=", ">=", "~=", "==":
			return 3, false, true
		case "..":
			return 4, true, true
		case "+", "-":
			return 5, false, true
		case "*", "/", "//", "%":
			return 6, false, true
		case "^":
			return 8, true, true
		}
	}
	return 0, false, false
}

func (c *cursor) isUnaryOp() bool {
	t := c.peek().Token
	return (t.Kind == lex.Symbol && (t.Text == "-" || t.Text == "#")) ||
		(t.Kind == lex.Name && t.Text == "not")
}

// parseExpr parses a full expression (including the if-then-else expression form).
func (c *cursor) parseExpr() Node {
	if c.atKeyword("if") {
		return c.parseIfExpr()
	}
	return c.parseBinary(1)
}

func (c *cursor) parseBinary(minPrec int) Node {
	left := c.parseUnary()
	for {
		prec, rightAssoc, ok := binPrec(c.peek().Token)
		if !ok || prec < minPrec {
			break
		}
		op := c.next()
		nextMin := prec + 1
		if rightAssoc {
			nextMin = prec
		}
		right := c.parseBinary(nextMin)
		left = &Binary{Left: left, Op: op, Right: right}
	}
	return left
}

func (c *cursor) parseUnary() Node {
	// An if-then-else expression may appear as any operand, not just at the top of
	// an expression (e.g. `a or if cond then x else y`), so handle it here too —
	// parseBinary reaches every operand through parseUnary.
	if c.atKeyword("if") {
		return c.parseIfExpr()
	}
	if c.isUnaryOp() {
		op := c.next()
		operand := c.parseBinary(unaryPrec)
		return &Unary{Op: op, Operand: operand}
	}
	return c.parseAsExp()
}

// parseAsExp parses a value expression followed by zero or more `:: Type`
// assertions. (Roundtrip is unaffected by how `::` associates with surrounding
// operators, since Unparse depends only on token order.)
func (c *cursor) parseAsExp() Node {
	e := c.parseValue()
	for c.atSymbol("::") {
		op := c.next()
		t := c.parseType()
		e = &TypeAssert{Expr: e, ColonColon: op, Type: t}
	}
	return e
}

func (c *cursor) parseIfExpr() Node {
	n := &IfExpr{}
	n.If = c.next() // if
	n.Cond = c.parseExpr()
	n.Then = c.expectKeyword("then")
	n.ThenExpr = c.parseExpr()
	for c.atKeyword("elseif") {
		var cl IfExprClause
		cl.Elseif = c.next()
		cl.Cond = c.parseExpr()
		cl.Then = c.expectKeyword("then")
		cl.Value = c.parseExpr()
		n.Elseifs = append(n.Elseifs, cl)
	}
	n.Else = c.expectKeyword("else")
	n.ElseExpr = c.parseExpr()
	return n
}

// parseValue parses a simple/primary expression. Only *prefix expressions* (a name
// or a parenthesized expression) may take a suffix chain (.field, [i], call,
// :method); *simple expressions* (literals, tables, function exprs, varargs,
// strings, interpolated strings) may not. This mirrors Lua's prefixexp vs simpleexp
// split — it stops a table constructor or literal from absorbing a following "(" on
// the next line as a call (the classic ambiguity).
func (c *cursor) parseValue() Node {
	switch {
	case c.atKeyword("nil"):
		return &Nil{Tok: c.next()}
	case c.atKeyword("true"):
		return &True{Tok: c.next()}
	case c.atKeyword("false"):
		return &False{Tok: c.next()}
	case c.atKeyword("function"):
		fn := c.next()
		return &FuncExpr{Func: fn, Body: c.parseFuncBody()}
	case c.atSymbol("..."):
		return &Vararg{Tok: c.next()}
	case c.atSymbol("{"):
		return c.parseTable()
	case c.peek().Token.Kind == lex.Number:
		return &Number{Tok: c.next()}
	case c.peek().Token.Kind == lex.String:
		return &String{Tok: c.next()}
	case c.peek().Token.Kind == lex.InterpSimple || c.peek().Token.Kind == lex.InterpStart:
		return c.parseInterpString()
	case c.atSymbol("("), c.peek().Token.Kind == lex.Name:
		return c.parseSuffixes(c.parsePrefixAtom())
	default:
		c.errHere("expected an expression")
		return &Name{Tok: c.next()}
	}
}

// parsePrefixExpr parses a prefix expression: a name or parenthesized expression
// followed by any suffix chain. Used for assignment targets and call statements.
func (c *cursor) parsePrefixExpr() Node {
	return c.parseSuffixes(c.parsePrefixAtom())
}

// parsePrefixAtom parses the atom that begins a prefix expression: a parenthesized
// expression or a bare name.
func (c *cursor) parsePrefixAtom() Node {
	if c.atSymbol("(") {
		return c.parseParen()
	}
	return &Name{Tok: c.next()}
}

// parseSuffixes applies any chain of suffixes to a prefix-expression base:
// .name, [expr], (args), {table}, "string", `interp`, :method(args).
func (c *cursor) parseSuffixes(base Node) Node {
	for {
		switch {
		case c.atSymbol("."):
			dot := c.next()
			name := c.expectName()
			base = &Index{Base: base, Dot: dot, Name: name}
		case c.atSymbol("["):
			open := c.next()
			key := c.parseExpr()
			closeTok := c.expectSymbol("]")
			base = &IndexExpr{Base: base, Open: open, Key: key, Close: closeTok}
		case c.atSymbol("("), c.atSymbol("{"), c.atCallString():
			base = &Call{Base: base, Args: c.parseCallArgs()}
		case c.atSymbol(":"):
			colon := c.next()
			name := c.expectName()
			args := c.parseCallArgs()
			base = &MethodCall{Base: base, Colon: colon, Name: name, Args: args}
		default:
			return base
		}
	}
}

func (c *cursor) atCallString() bool {
	k := c.peek().Token.Kind
	return k == lex.String || k == lex.InterpSimple || k == lex.InterpStart
}

// parseCallArgs parses the argument form of a call: (list), {table}, "string", or
// `interp`.
func (c *cursor) parseCallArgs() Node {
	switch {
	case c.atSymbol("("):
		return c.parseParenArgs()
	case c.atSymbol("{"):
		return c.parseTable()
	case c.peek().Token.Kind == lex.String:
		s := c.next()
		return &String{Tok: s}
	case c.peek().Token.Kind == lex.InterpSimple || c.peek().Token.Kind == lex.InterpStart:
		return c.parseInterpString()
	default:
		c.errHere("expected call arguments")
		open := c.next()
		return &ParenArgs{Open: open}
	}
}

func (c *cursor) parseParenArgs() Node {
	open := c.expectSymbol("(")
	var list ExprList
	if !c.atSymbol(")") && !c.atEnd() {
		c.parseExprListInto(&list, ")")
	}
	closeTok := c.expectSymbol(")")
	return &ParenArgs{Open: open, List: list, Close: closeTok}
}

// parseExprListInto parses a comma-separated expression list, stopping before stop.
func (c *cursor) parseExprListInto(list *ExprList, stop string) {
	list.Items = append(list.Items, c.parseExpr())
	for c.atSymbol(",") {
		list.Seps = append(list.Seps, c.next())
		if c.atSymbol(stop) || c.atEnd() {
			break
		}
		list.Items = append(list.Items, c.parseExpr())
	}
}

func (c *cursor) parsePrimary() Node {
	switch {
	case c.atKeyword("nil"):
		return &Nil{Tok: c.next()}
	case c.atKeyword("true"):
		return &True{Tok: c.next()}
	case c.atKeyword("false"):
		return &False{Tok: c.next()}
	case c.atKeyword("function"):
		fn := c.next()
		return &FuncExpr{Func: fn, Body: c.parseFuncBody()}
	case c.atSymbol("..."):
		return &Vararg{Tok: c.next()}
	case c.atSymbol("("):
		return c.parseParen()
	case c.atSymbol("{"):
		return c.parseTable()
	case c.peek().Token.Kind == lex.Number:
		return &Number{Tok: c.next()}
	case c.peek().Token.Kind == lex.String:
		return &String{Tok: c.next()}
	case c.peek().Token.Kind == lex.InterpSimple || c.peek().Token.Kind == lex.InterpStart:
		return c.parseInterpString()
	case c.peek().Token.Kind == lex.Name:
		return &Name{Tok: c.next()}
	default:
		c.errHere("expected an expression")
		return &Name{Tok: c.next()}
	}
}

func (c *cursor) parseParen() Node {
	open := c.expectSymbol("(")
	inner := c.parseExpr()
	closeTok := c.expectSymbol(")")
	return &Paren{Open: open, Inner: inner, Close: closeTok}
}

func (c *cursor) parseTable() Node {
	open := c.expectSymbol("{")
	n := &Table{Open: open}
	for !c.atSymbol("}") && !c.atEnd() {
		f := c.parseTableField()
		if c.atSymbol(",") || c.atSymbol(";") {
			sep := c.next()
			f.Sep = &sep
		}
		n.Fields = append(n.Fields, f)
		if f.Sep == nil {
			break // no separator => field list is finished
		}
	}
	n.Close = c.expectSymbol("}")
	return n
}

func (c *cursor) parseTableField() TableField {
	switch {
	case c.atSymbol("["):
		lb := c.next()
		key := c.parseExpr()
		rb := c.expectSymbol("]")
		eq := c.expectSymbol("=")
		val := c.parseExpr()
		return TableField{LBracket: &lb, Key: key, RBracket: &rb, Eq: &eq, Value: val}
	case c.peek().Token.Kind == lex.Name && c.peek2().Token.Kind == lex.Symbol && c.peek2().Token.Text == "=":
		name := c.next()
		eq := c.next()
		val := c.parseExpr()
		return TableField{NameTok: &name, Eq: &eq, Value: val}
	default:
		return TableField{Value: c.parseExpr()}
	}
}

// parseInterpString parses a backtick interpolated string expression.
func (c *cursor) parseInterpString() Node {
	if c.peek().Token.Kind == lex.InterpSimple {
		r := c.next()
		return &InterpString{Simple: &r}
	}
	start := c.next() // InterpStart
	is := &InterpString{Start: &start}
	for {
		if c.atEnd() {
			break
		}
		expr := c.parseExpr()
		mid := c.next()
		is.Exprs = append(is.Exprs, expr)
		is.Mids = append(is.Mids, mid)
		if mid.Token.Kind != lex.InterpMid {
			break // InterpEnd (normal) or unexpected token (recovery)
		}
	}
	return is
}
