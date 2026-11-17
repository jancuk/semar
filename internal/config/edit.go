package config

import (
	"errors"
	"fmt"
	"strings"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/yamlfile"
)

// SetWebSignIn gives an account of semar.yaml the web block, or replaces the one it has. It changes
// that block and no other line, so every comment and every other setting stays as it was. It
// refuses an account that it cannot place without rewriting the file, and a result that the
// configuration would not accept.
func SetWebSignIn(data []byte, account string, w WebSignIn) ([]byte, error) {
	var doc yaml.Node
	if err := yamlfile.Decode(data, fileName, &doc); err != nil {
		return nil, err
	}
	profile := valueOf(valueOf(doc.Content[0], "authProfiles"), account)
	switch {
	case profile == nil || profile.Kind != yaml.MappingNode:
		return nil, fmt.Errorf("%s has no account %s under authProfiles", fileName, account)
	case profile.Style&yaml.FlowStyle != 0 || len(profile.Content) == 0:
		return nil, fmt.Errorf("the account %s is written on one line in %s; add its web block by hand", account, fileName)
	}
	block, err := yaml.Marshal(&yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "user"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: w.User},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "password"}, {Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.DoubleQuotedStyle, Value: w.Password},
	}})
	if err != nil {
		return nil, err
	}
	indent := strings.Repeat(" ", profile.Content[0].Column-1)
	line := indent + "web: " + strings.TrimSpace(string(block)) + "\n"

	lines := strings.SplitAfter(string(data), "\n")
	first, last := len(lines), len(lines)
	for i := 0; i+1 < len(profile.Content); i += 2 {
		key, value := profile.Content[i], profile.Content[i+1]
		if key.Value == "web" {
			first, last = key.Line-1, lastLine(value)
			break
		}
		first, last = lastLine(value), lastLine(value)
	}
	if !strings.HasSuffix(lines[first-1], "\n") && first == last {
		lines[first-1] += "\n"
	}
	out := strings.Join(lines[:first], "") + line + strings.Join(lines[last:], "")
	if _, err := parse([]byte(out), fileName); err != nil {
		return nil, errors.New("the web block is not valid: " + strings.TrimPrefix(err.Error(), fileName+": "))
	}
	return []byte(out), nil
}

func valueOf(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return resolveAlias(m.Content[i+1])
		}
	}
	return nil
}

// lastLine is the last line, counted from 1, that the node covers.
func lastLine(n *yaml.Node) int {
	last := n.Line
	if n.Kind == yaml.ScalarNode && (n.Style&yaml.LiteralStyle != 0 || n.Style&yaml.FoldedStyle != 0) {
		last += strings.Count(strings.TrimRight(n.Value, "\n"), "\n") + 1
	}
	for _, c := range n.Content {
		last = max(last, lastLine(c))
	}
	return last
}
