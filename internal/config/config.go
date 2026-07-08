// Package config reads and writes gclean's YAML config file. Path:
// $XDG_CONFIG_HOME/gclean/config.yaml or ~/.config/gclean/config.yaml.
// Overridable via GCLEAN_CONFIG_PATH.
//
// We use yaml.v3 instead of Viper for the scaffold: Viper adds 30+ transitive
// deps for a single config file. Swap is trivial and called out in the README.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gclean/internal/engine"
)

const defaultConfig = `# gclean default configuration
# See: gclean rules --help
keep:
  contacts: true
  replied: true
  starred: true
  important: true
  sent_by_user: true
  recent_days: 365

delete:
  - "has:unsubscribe older_than:180d"
  - "category:promotions older_than:365d"
  - "from:noreply older_than:90d"

archive:
  - "subject:receipt"

ignore:
  - bank.com
`

// Document mirrors config.yaml. YAML tags are stable; renaming breaks users.
type Document struct {
	Keep    engine.KeepConfig `yaml:"keep"`
	Delete  []string          `yaml:"delete"`
	Archive []string          `yaml:"archive"`
	Ignore  []string          `yaml:"ignore"`
}

// DefaultPath returns the resolved config file path.
func DefaultPath() (string, error) {
	if p := os.Getenv("GCLEAN_CONFIG_PATH"); p != "" {
		return p, nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "gclean", "config.yaml"), nil
}

// Load reads the config; on first run, it writes the default doc and returns it.
func Load() (Document, error) {
	p, err := DefaultPath()
	if err != nil {
		return Document{}, err
	}
	if _, err := os.Stat(p); os.IsNotExist(err) {
		if err := writeDefault(p); err != nil {
			return Document{}, fmt.Errorf("write default config %s: %w", p, err)
		}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Document{}, fmt.Errorf("read config %s: %w", p, err)
	}
	return Parse(data)
}

// Parse is Load() exposed for tests and for `gclean config --print`.
func Parse(data []byte) (Document, error) {
	var doc Document
	if err := yamlUnmarshal(data, &doc); err != nil {
		return doc, fmt.Errorf("parse config: %w", err)
	}
	return doc, nil
}

func writeDefault(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultConfig), 0o644)
}

// Compile converts a Document into a parseable RuleConfig the planner uses.
// Returns the first parse error so we can point users at the offending rule.
func (d Document) Compile() (engine.RuleConfig, error) {
	rc := engine.RuleConfig{Ignore: d.Ignore}
	for _, raw := range d.Delete {
		r, err := engine.ParseRule("delete", raw)
		if err != nil {
			return rc, fmt.Errorf("delete rule %q: %w", raw, err)
		}
		rc.Delete = append(rc.Delete, r)
	}
	for _, raw := range d.Archive {
		r, err := engine.ParseRule("archive", raw)
		if err != nil {
			return rc, fmt.Errorf("archive rule %q: %w", raw, err)
		}
		rc.Archive = append(rc.Archive, r)
	}
	// Note: d.Keep (engine.KeepConfig) is the §6 protection profile with
	// boolean toggles + recent_days. It's enforced by Protect() in the
	// planner, NOT by DSL rule matching. The keep:/* entries in config.yaml
	// therefore do not need parsing.
	return rc, nil
}

// CompiledConfig is the planner-ready form of a Document: the parsed rules
// plus the keep/protect profile. It's what engine.Pipeline consumes so the
// engine package never imports config (keeps the dependency direction one-way:
// config → engine, never engine → config).
type CompiledConfig struct {
	Rules engine.RuleConfig
	Keep  engine.KeepConfig
}

// CompileFull returns both the parsed RuleConfig and the KeepConfig in one
// call, surfacing the first parse error so we can point users at the
// offending rule.
func (d Document) CompileFull() (CompiledConfig, error) {
	rc, err := d.Compile()
	if err != nil {
		return CompiledConfig{}, err
	}
	return CompiledConfig{Rules: rc, Keep: d.Keep}, nil
}
