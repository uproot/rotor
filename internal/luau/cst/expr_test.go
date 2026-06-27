package cst

import "testing"

func roundtripExpr(t *testing.T, src string) {
	t.Helper()
	c := newCursor(src)
	e := c.parseExpr()
	if got := Unparse(e); got != src {
		t.Fatalf("expr roundtrip: %q -> %q", src, got)
	}
	if len(c.diags) != 0 {
		t.Fatalf("%q: diags %v", src, c.diags)
	}
	if !c.atEnd() {
		t.Fatalf("%q: leftover at %q", src, c.peek().Token.Text)
	}
}

func TestExprRoundtrip(t *testing.T) {
	cases := []string{
		// literals
		"nil", "true", "false", "...", "1", "1.5", "0xFF", "1_000",
		`"hi"`, "'x'", "[[long]]", "[==[ a ]] b ]==]",
		"`simple`", "`a{x}b`", "`a{x}b{y}c`", "`{ {n=1} }`",
		// names
		"foo", "_bar",
		// parens
		"(a)", "( a )",
		// suffix chains
		"a.b.c", "a[b]", "f(x, y)", "f()", "a:m(1)", `f"s"`, "f{1, 2}",
		"a.b:c(d)[e]", "(f or g)(x)", "f`x{y}z`",
		// operators
		"1 + 2 * 3", "1+2*3", "a and b or c", "-x", "not a", "#t",
		"-x ^ 2", "a .. b .. c", "1 < 2", "a == b", "a ~= b and not c",
		// if-then-else expression
		"if a then b else c", "if a then b elseif c then d else e",
		// if-then-else as a binary/unary operand (React uses `... or if x then y else z`)
		"a or if c then d else e", "x and if a then b else c",
		"a or b or if c then d else e", "not if a then b else c",
		// tables
		"{}", "{ }", "{1, 2, 3}", "{a = 1}", "{[k] = v}",
		"{a = 1, [b] = 2; 3}", "{a = 1,}", "{1; 2; 3}",
	}
	for _, src := range cases {
		roundtripExpr(t, src)
	}
}
