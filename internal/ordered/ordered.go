// Package ordered provides a map that keeps insertion order in JSON and YAML.
package ordered

import (
	"bytes"
	"encoding/json"

	"gopkg.in/yaml.v3"
)

type KV struct {
	Key   string
	Value any
}

// Map marshals as a JSON/YAML object with keys in slice order.
type Map []KV

func (m Map) Get(key string) (any, bool) {
	for _, kv := range m {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return nil, false
}

func (m Map) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, kv := range m {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, err := Marshal(kv.Key)
		if err != nil {
			return nil, err
		}
		v, err := Marshal(kv.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(v)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (m Map) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, kv := range m {
		var v yaml.Node
		if err := v.Encode(kv.Value); err != nil {
			return nil, err
		}
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: kv.Key}, &v)
	}
	return node, nil
}

// Marshal is json.Marshal without HTML escaping (keeps "&" in URLs readable).
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// MarshalIndent is Marshal with two-space indentation.
func MarshalIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
