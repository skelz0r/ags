package config

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type KV struct {
	Key   string
	Value string
}

type Edit func(root *yaml.Node) error

func ApplyEdit(data []byte, edit Edit) ([]byte, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
	}
	if doc.Kind == 0 || len(doc.Content) == 0 || doc.Content[0].Tag == "!!null" {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected a mapping at the top level")
	}
	if err := edit(root); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func SetJobField(job, key, value string) Edit {
	return func(root *yaml.Node) error {
		node := ensureMapping(ensureMapping(root, "jobs"), job)
		setScalar(node, key, value)
		return nil
	}
}

func AddJob(job string, fields []KV) Edit {
	return func(root *yaml.Node) error {
		jobs := ensureMapping(root, "jobs")
		if lookup(jobs, job) != nil {
			return fmt.Errorf("job %q already exists", job)
		}
		node := ensureMapping(jobs, job)
		for _, f := range fields {
			setScalar(node, f.Key, f.Value)
		}
		return nil
	}
}

func DeleteJob(job string) Edit {
	return func(root *yaml.Node) error {
		jobs := lookup(root, "jobs")
		if jobs == nil || !removeKey(jobs, job) {
			return fmt.Errorf("job %q not found", job)
		}
		return nil
	}
}

func SetTopLevel(key, value string) Edit {
	return func(root *yaml.Node) error {
		setScalar(root, key, value)
		return nil
	}
}

func JobLine(data []byte, job string) int {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) == 0 {
		return 1
	}
	jobs := lookup(doc.Content[0], "jobs")
	if jobs == nil {
		return 1
	}
	for _, p := range pairsOf(jobs) {
		if p.key.Value == job {
			return p.key.Line
		}
	}
	return jobs.Line
}

func ensureMapping(parent *yaml.Node, key string) *yaml.Node {
	if existing := lookup(parent, key); existing != nil {
		if existing.Kind != yaml.MappingNode {
			existing.Kind, existing.Tag, existing.Value, existing.Content = yaml.MappingNode, "!!map", "", nil
		}
		existing.Style = 0
		return existing
	}
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, node)
	return node
}

func setScalar(parent *yaml.Node, key, value string) {
	node := scalarNode(key, value)
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			node.HeadComment = parent.Content[i+1].HeadComment
			node.LineComment = parent.Content[i+1].LineComment
			parent.Content[i+1] = node
			return
		}
	}
	parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, node)
}

func scalarNode(key, value string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	switch {
	case key == "enabled":
		node.Tag = "!!bool"
	case key == "version":
		node.Tag = "!!int"
	case strings.Contains(value, "\n"):
		node.Style = yaml.LiteralStyle
		if !strings.HasSuffix(value, "\n") {
			node.Value += "\n"
		}
	case key == "schedule" || strings.ContainsAny(value, ":#{}[]*&!|>'\"%@`"):
		node.Style = yaml.DoubleQuotedStyle
	}
	return node
}

func removeKey(parent *yaml.Node, key string) bool {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
			return true
		}
	}
	return false
}
