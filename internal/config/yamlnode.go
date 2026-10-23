package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type pair struct {
	key   *yaml.Node
	value *yaml.Node
}

var yamlLine = regexp.MustCompile(`line (\d+): (.*)`)

func parseDocument(file string, data []byte) (*yaml.Node, Issues) {
	var issues Issues
	var doc yaml.Node
	err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc)
	if errors.Is(err, io.EOF) {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	if err != nil {
		message := strings.TrimPrefix(err.Error(), "yaml: ")
		if m := yamlLine.FindStringSubmatch(message); m != nil {
			line, _ := strconv.Atoi(m[1])
			issues.add(file, line, "%s", m[2])
		} else {
			issues.add(file, 0, "%s", message)
		}
		return nil, issues
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	if root.Kind != yaml.MappingNode {
		issues.add(file, root.Line, "expected a mapping at the top level")
		return nil, issues
	}
	return root, nil
}

func pairsOf(node *yaml.Node) []pair {
	var out []pair
	for i := 0; i+1 < len(node.Content); i += 2 {
		out = append(out, pair{key: node.Content[i], value: node.Content[i+1]})
	}
	return out
}

func lookup(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for _, p := range pairsOf(node) {
		if p.key.Value == key {
			return p.value
		}
	}
	return nil
}

func scalarString(node *yaml.Node) (string, error) {
	if node.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("expected a string")
	}
	if node.Tag == "!!null" {
		return "", nil
	}
	return node.Value, nil
}

func scalarBool(node *yaml.Node) (bool, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
		return false, fmt.Errorf("expected true or false")
	}
	var b bool
	err := node.Decode(&b)
	return b, err
}

func stringList(node *yaml.Node) ([]string, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("expected a list")
	}
	var out []string
	for _, item := range node.Content {
		s, err := scalarString(item)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func stringMap(node *yaml.Node) (map[string]string, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected a mapping")
	}
	out := map[string]string{}
	for _, p := range pairsOf(node) {
		s, err := scalarString(p.value)
		if err != nil {
			return nil, err
		}
		out[p.key.Value] = s
	}
	return out, nil
}

func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || days < 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid duration %q: use 30s, 45m, 12h or 2d", s)
	}
	return d, nil
}

func FormatDuration(d time.Duration) string {
	if d < time.Minute {
		return "0m"
	}
	d = d.Truncate(time.Minute)
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	minutes := (d - hours*time.Hour) / time.Minute
	var b strings.Builder
	if days > 0 {
		fmt.Fprintf(&b, "%dd", days)
	}
	if hours > 0 {
		fmt.Fprintf(&b, "%dh", hours)
	}
	if minutes > 0 {
		fmt.Fprintf(&b, "%dm", minutes)
	}
	return b.String()
}

func errorf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
