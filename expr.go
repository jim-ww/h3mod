package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Conditions of conditional options, a subset of the filtrex expressions SMF
// evaluates against its config: or, and, not, comparisons, in / not in,
// parentheses, string/number/boolean literals and dotted names.

type exprParser struct {
	toks []string
	pos  int
	vars map[string]any
}

func evalCondition(src string, vars map[string]any) (bool, error) {
	toks, err := tokenize(src)
	if err != nil {
		return false, err
	}
	p := &exprParser{toks: toks, vars: vars}
	v, err := p.or()
	if err != nil {
		return false, err
	}
	if p.pos != len(p.toks) {
		return false, fmt.Errorf("unexpected %q", p.toks[p.pos])
	}
	return truthy(v), nil
}

func tokenize(s string) ([]string, error) {
	var toks []string
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string in %q", s)
			}
			toks = append(toks, s[i:j+1])
			i = j + 1
		case strings.ContainsRune("()", rune(c)):
			toks = append(toks, string(c))
			i++
		case strings.HasPrefix(s[i:], "==") || strings.HasPrefix(s[i:], "!=") ||
			strings.HasPrefix(s[i:], "<=") || strings.HasPrefix(s[i:], ">="):
			toks = append(toks, s[i:i+2])
			i += 2
		case c == '<' || c == '>':
			toks = append(toks, string(c))
			i++
		case unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) || c == '_' || c == '.' || c == '-':
			j := i
			for j < len(s) && (unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j])) || strings.ContainsRune("_.-", rune(s[j]))) {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		default:
			return nil, fmt.Errorf("unsupported %q in %q", c, s)
		}
	}
	return toks, nil
}

func (p *exprParser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *exprParser) or() (any, error) {
	l, err := p.and()
	for err == nil && p.peek() == "or" {
		p.pos++
		var r any
		r, err = p.and()
		l = truthy(l) || truthy(r)
	}
	return l, err
}

func (p *exprParser) and() (any, error) {
	l, err := p.not()
	for err == nil && p.peek() == "and" {
		p.pos++
		var r any
		r, err = p.not()
		l = truthy(l) && truthy(r)
	}
	return l, err
}

func (p *exprParser) not() (any, error) {
	if p.peek() == "not" {
		p.pos++
		v, err := p.not()
		return !truthy(v), err
	}
	return p.compare()
}

func (p *exprParser) compare() (any, error) {
	l, err := p.value()
	if err != nil {
		return nil, err
	}
	op := p.peek()
	if op == "not" && p.pos+1 < len(p.toks) && p.toks[p.pos+1] == "in" {
		p.pos += 2
		r, err := p.value()
		return !contains(r, l), err
	}
	switch op {
	case "in":
		p.pos++
		r, err := p.value()
		return contains(r, l), err
	case "==", "!=":
		p.pos++
		r, err := p.value()
		eq := fmt.Sprint(l) == fmt.Sprint(r)
		return eq == (op == "=="), err
	case "<", ">", "<=", ">=":
		p.pos++
		r, err := p.value()
		a, b := toFloat(l), toFloat(r)
		return map[string]bool{"<": a < b, ">": a > b, "<=": a <= b, ">=": a >= b}[op], err
	}
	return l, nil
}

func (p *exprParser) value() (any, error) {
	t := p.peek()
	if t == "" {
		return nil, fmt.Errorf("unexpected end of expression")
	}
	p.pos++
	switch {
	case t == "(":
		v, err := p.or()
		if p.peek() != ")" {
			return nil, fmt.Errorf("missing )")
		}
		p.pos++
		return v, err
	case t[0] == '"' || t[0] == '\'':
		return strings.ReplaceAll(t[1:len(t)-1], `\`+t[:1], t[:1]), nil
	case t == "true", t == "false":
		return t == "true", nil
	}
	if f, err := strconv.ParseFloat(t, 64); err == nil {
		return f, nil
	}
	var cur any = p.vars
	for part := range strings.SplitSeq(t, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, nil // optional chaining: missing is undefined
		}
		cur = m[part]
	}
	return cur, nil
}

func contains(haystack, needle any) bool {
	switch h := haystack.(type) {
	case []string:
		return slices.Contains(h, fmt.Sprint(needle))
	case string:
		return strings.Contains(h, fmt.Sprint(needle))
	case map[string]any:
		_, ok := h[fmt.Sprint(needle)]
		return ok
	}
	return false
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return true
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}
