package config

import "gopkg.in/yaml.v3"

func yamlUnmarshal(data []byte, v any) error {
	return yaml.Unmarshal(data, v)
}

func yamlMarshal(v any) ([]byte, error) { return yaml.Marshal(v) }
