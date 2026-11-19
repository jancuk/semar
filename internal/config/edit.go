package config

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/hostrule"
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
	key, profile := entryOf(valueOf(doc.Content[0], "authProfiles"), account)
	switch {
	case profile == nil || profile.Kind != yaml.MappingNode:
		return nil, fmt.Errorf("%s has no account %s under authProfiles", fileName, account)
	case len(profile.Content) > 0 && profile.Style&yaml.FlowStyle != 0:
		return nil, fmt.Errorf("the account %s is written on one line in %s; add its web block by hand", account, fileName)
	}
	block, err := yaml.Marshal(&yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "user"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: w.User},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "password"}, {Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.DoubleQuotedStyle, Value: w.Password},
	}})
	if err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(data), "\n")
	if len(profile.Content) == 0 {
		// semar init writes an account with no token as "name: {}". The web block becomes its first line.
		at := key.Line - 1
		open := strings.Index(lines[at][key.Column-1:], "{")
		shut := strings.Index(lines[at], "}")
		if profile.Line != key.Line || open < 0 || shut < 0 {
			return nil, fmt.Errorf("the account %s is written on one line in %s; add its web block by hand", account, fileName)
		}
		open += key.Column - 1
		rest := strings.TrimRight(lines[at][shut+1:], "\n")
		lines[at] = strings.TrimRight(lines[at][:open], " ") + rest + "\n" +
			strings.Repeat(" ", key.Column+1) + "web: " + strings.TrimSpace(string(block)) + "\n"
		out := strings.Join(lines, "")
		if _, err := parse([]byte(out), fileName); err != nil {
			return nil, errors.New("the web block is not valid: " + strings.TrimPrefix(err.Error(), fileName+": "))
		}
		return []byte(out), nil
	}
	indent := strings.Repeat(" ", profile.Content[0].Column-1)
	line := indent + "web: " + strings.TrimSpace(string(block)) + "\n"

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

// entryOf gives the key and the value of one entry of a mapping, or nil for both.
func entryOf(m *yaml.Node, name string) (key, value *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == name {
			return m.Content[i], resolveAlias(m.Content[i+1])
		}
	}
	return nil, nil
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

// AddHosts lists more hosts under hosts of an environment of semar.yaml. It changes that list and no
// other line, so every comment and every other setting stays as it was. A host that is listed
// already is left out, and a file with nothing to add comes back as it was. It writes the new
// entries as the list is written: inside the brackets of a list on one line, or as one line each
// below a list of lines. It refuses a list that it cannot place without rewriting the file, and a
// result that the configuration would not accept.
func AddHosts(data []byte, env string, add []string) ([]byte, error) {
	for _, h := range add {
		if err := hostrule.CheckEntry(h); err != nil {
			return nil, err
		}
	}
	var doc yaml.Node
	if err := yamlfile.Decode(data, fileName, &doc); err != nil {
		return nil, err
	}
	node := valueOf(valueOf(doc.Content[0], "environments"), env)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s has no environment %s", fileName, env)
	}
	list := valueOf(node, "hosts")
	var listed []string
	if list != nil && list.Kind == yaml.SequenceNode {
		for _, item := range list.Content {
			listed = append(listed, strings.ToLower(item.Value))
		}
	}
	var fresh []string
	for _, h := range add {
		if !slices.Contains(listed, strings.ToLower(h)) && !slices.Contains(fresh, h) {
			fresh = append(fresh, h)
		}
	}
	if len(fresh) == 0 {
		return data, nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	var err error
	switch {
	case list == nil:
		lines, err = addHostsKey(lines, node, fresh)
	case list.Kind == yaml.SequenceNode && list.Style&yaml.FlowStyle != 0:
		lines, err = addToFlowHosts(lines, node, list, fresh)
	case list.Kind == yaml.SequenceNode && len(list.Content) > 0:
		lines, err = addToBlockHosts(lines, list, fresh)
	default:
		err = errors.New("the hosts of the environment " + env + " are written in a way that Semar cannot add to")
	}
	if err != nil {
		return nil, fmt.Errorf("%w; add the hosts to %s by hand", err, fileName)
	}
	out := strings.Join(lines, "")
	cfg, err := parse([]byte(out), fileName)
	if err != nil {
		return nil, errors.New("the hosts are not valid: " + strings.TrimPrefix(err.Error(), fileName+": "))
	}
	for _, h := range fresh {
		if !slices.Contains(cfg.Environments[env].Hosts, h) {
			return nil, fmt.Errorf("the host %s did not land in hosts of %s; add the hosts to %s by hand", h, env, fileName)
		}
	}
	return []byte(out), nil
}

// HostEntry is a host as the entry of a list of lines under hosts, so that a person can add it by hand.
func HostEntry(host string) string { return hostText(host, false) }

// hostText is a host as one entry of a list. In flow style a host with a bracket or a comma, and in
// either style a host that YAML would read as another type, is quoted.
func hostText(host string, flow bool) string {
	quoted := strconv.Quote(host)
	if flow && strings.ContainsAny(host, ",[]{}") {
		return quoted
	}
	out, err := yaml.Marshal(&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: host})
	if err != nil {
		return quoted
	}
	return strings.TrimSpace(string(out))
}

// addToFlowHosts puts the hosts before the closing bracket of a list that is on one line.
func addToFlowHosts(lines []string, env, list *yaml.Node, fresh []string) ([]string, error) {
	row := list.Line - 1
	line := lines[row]
	open := list.Column - 1
	if open < 0 || open >= len(line) || line[open] != '[' {
		return nil, errors.New("the list of hosts is not where Semar expects it")
	}
	closing := closingBracket(line, open)
	if closing < 0 {
		return nil, errors.New("the list of hosts goes over several lines")
	}
	quoteAll := len(list.Content) > 0 && list.Content[0].Style&yaml.DoubleQuotedStyle != 0
	texts := make([]string, len(fresh))
	for i, h := range fresh {
		texts[i] = hostText(h, true)
		if quoteAll {
			texts[i] = strconv.Quote(h)
		}
	}
	inner := strings.TrimRight(line[open+1:closing], " \t")
	at := open + 1 + len(inner)
	added := strings.Join(texts, ", ")
	switch {
	case strings.TrimSpace(inner) == "":
		at = open + 1
	case strings.HasSuffix(inner, ","):
		added = " " + added
	default:
		added = ", " + added
	}
	lines[row] = line[:at] + added + line[at:]
	return lines, nil
}

// closingBracket is where the bracket that opens at open closes, in the line, or -1. Text in quotes
// and a comment hide brackets.
func closingBracket(line string, open int) int {
	depth := 0
	var quote byte
	for i := open; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#' && i > 0 && (line[i-1] == ' ' || line[i-1] == '\t'):
			return -1
		case c == '[':
			depth++
		case c == ']':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

var dashPrefix = regexp.MustCompile(`^(\s*-\s+)`)

// addToBlockHosts adds one line for each host below the last entry of a list of lines.
func addToBlockHosts(lines []string, list *yaml.Node, fresh []string) ([]string, error) {
	first := list.Content[0]
	prefix := dashPrefix.FindString(lines[first.Line-1])
	if prefix == "" {
		return nil, errors.New("the list of hosts does not start its entries on the line of their dash")
	}
	quoteAll := first.Style&yaml.DoubleQuotedStyle != 0
	last := lastLine(list)
	if !strings.HasSuffix(lines[last-1], "\n") {
		lines[last-1] += "\n"
	}
	added := make([]string, len(fresh))
	for i, h := range fresh {
		text := hostText(h, false)
		if quoteAll {
			text = strconv.Quote(h)
		}
		added[i] = prefix + text + "\n"
	}
	return slices.Concat(lines[:last], added, lines[last:]), nil
}

// addHostsKey gives an environment that has no hosts a list of lines, as its last setting.
func addHostsKey(lines []string, env *yaml.Node, fresh []string) ([]string, error) {
	if env.Style&yaml.FlowStyle != 0 || len(env.Content) == 0 {
		return nil, errors.New("the environment is written on one line")
	}
	indent := strings.Repeat(" ", env.Content[0].Column-1)
	last := lastLine(env)
	if !strings.HasSuffix(lines[last-1], "\n") {
		lines[last-1] += "\n"
	}
	added := []string{indent + "hosts:\n"}
	for _, h := range fresh {
		added = append(added, indent+"  - "+hostText(h, false)+"\n")
	}
	return slices.Concat(lines[:last], added, lines[last:]), nil
}
