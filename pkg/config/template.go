package config

import (
	"fmt"
	"reflect"
	"strings"
)

type liquidTemplate struct {
	nodes []templateNode
}

type templateNode interface {
	render(ctx map[string]interface{}) (string, error)
}

type textNode struct {
	text string
}

type variableNode struct {
	expr string
}

type ifNode struct {
	cond     string
	thenBody []templateNode
	elseBody []templateNode
}

func ParseLiquidTemplate(input string) (*liquidTemplate, error) {
	idx := 0
	nodes, stop, err := parseTemplateNodes(input, &idx)
	if err != nil {
		return nil, err
	}
	if stop != "" {
		return nil, fmt.Errorf("unexpected tag %q", stop)
	}
	return &liquidTemplate{nodes: nodes}, nil
}

func RenderLiquidTemplate(input string, ctx map[string]interface{}) (string, error) {
	tmpl, err := ParseLiquidTemplate(input)
	if err != nil {
		return "", err
	}
	return tmpl.Render(ctx)
}

func (t *liquidTemplate) Render(ctx map[string]interface{}) (string, error) {
	var out strings.Builder
	for _, node := range t.nodes {
		rendered, err := node.render(ctx)
		if err != nil {
			return "", err
		}
		out.WriteString(rendered)
	}
	return out.String(), nil
}

func parseTemplateNodes(input string, idx *int) ([]templateNode, string, error) {
	nodes := []templateNode{}
	for *idx < len(input) {
		nextVar := strings.Index(input[*idx:], "{{")
		nextTag := strings.Index(input[*idx:], "{%")
		next := nearestPositive(nextVar, nextTag)
		if next == -1 {
			nodes = append(nodes, textNode{text: input[*idx:]})
			*idx = len(input)
			break
		}

		next += *idx
		if next > *idx {
			nodes = append(nodes, textNode{text: input[*idx:next]})
		}

		switch {
		case strings.HasPrefix(input[next:], "{{"):
			end := strings.Index(input[next+2:], "}}")
			if end == -1 {
				return nil, "", fmt.Errorf("unterminated variable tag")
			}
			expr := strings.TrimSpace(input[next+2 : next+2+end])
			if expr == "" {
				return nil, "", fmt.Errorf("empty variable expression")
			}
			if strings.Contains(expr, "|") {
				return nil, "", fmt.Errorf("unknown filter in %q", expr)
			}
			nodes = append(nodes, variableNode{expr: expr})
			*idx = next + 2 + end + 2
		case strings.HasPrefix(input[next:], "{%"):
			end := strings.Index(input[next+2:], "%}")
			if end == -1 {
				return nil, "", fmt.Errorf("unterminated tag")
			}
			tag := strings.TrimSpace(input[next+2 : next+2+end])
			*idx = next + 2 + end + 2
			switch {
			case strings.HasPrefix(tag, "if "):
				cond := strings.TrimSpace(strings.TrimPrefix(tag, "if "))
				if cond == "" {
					return nil, "", fmt.Errorf("empty if condition")
				}
				thenBody, stop, err := parseTemplateNodes(input, idx)
				if err != nil {
					return nil, "", err
				}
				node := ifNode{cond: cond, thenBody: thenBody}
				if stop == "else" {
					elseBody, stop2, err := parseTemplateNodes(input, idx)
					if err != nil {
						return nil, "", err
					}
					if stop2 != "endif" {
						return nil, "", fmt.Errorf("missing endif for if %q", cond)
					}
					node.elseBody = elseBody
				} else if stop != "endif" {
					return nil, "", fmt.Errorf("missing endif for if %q", cond)
				}
				nodes = append(nodes, node)
			case tag == "else", tag == "endif":
				return nodes, tag, nil
			default:
				return nil, "", fmt.Errorf("unknown tag %q", tag)
			}
		}
	}
	return nodes, "", nil
}

func nearestPositive(a, b int) int {
	switch {
	case a == -1:
		return b
	case b == -1:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

func (n textNode) render(_ map[string]interface{}) (string, error) {
	return n.text, nil
}

func (n variableNode) render(ctx map[string]interface{}) (string, error) {
	v, err := lookupTemplateValue(ctx, n.expr)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(v), nil
}

func (n ifNode) render(ctx map[string]interface{}) (string, error) {
	v, err := lookupTemplateValue(ctx, n.cond)
	if err != nil {
		return "", err
	}
	body := n.elseBody
	if truthy(v) {
		body = n.thenBody
	}

	var out strings.Builder
	for _, child := range body {
		rendered, err := child.render(ctx)
		if err != nil {
			return "", err
		}
		out.WriteString(rendered)
	}
	return out.String(), nil
}

func lookupTemplateValue(ctx map[string]interface{}, expr string) (interface{}, error) {
	parts := strings.Split(strings.TrimSpace(expr), ".")
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("unknown variable %q", expr)
	}

	var cur interface{} = ctx
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("unknown variable %q", expr)
		}
		switch typed := cur.(type) {
		case map[string]interface{}:
			next, ok := typed[part]
			if !ok {
				return nil, fmt.Errorf("unknown variable %q", expr)
			}
			cur = next
		default:
			return nil, fmt.Errorf("unknown variable %q", expr)
		}
	}
	return cur, nil
}

func truthy(v interface{}) bool {
	if v == nil {
		return false
	}
	switch typed := v.(type) {
	case bool:
		return typed
	case string:
		return strings.TrimSpace(typed) != ""
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() > 0
	case reflect.Pointer, reflect.Interface:
		return !rv.IsNil()
	}
	return true
}
