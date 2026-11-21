package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SetDefaultModel validates ref against the current configuration and writes
// default_model to $RENSTRA_HOME/config.yaml, preserving existing YAML nodes.
func SetDefaultModel(ref string) (resolvedModel, storedRef string, err error) {
	cfg, err := Load()
	if err != nil {
		return "", "", err
	}
	pcfg, model, err := Resolve(ref, cfg)
	if err != nil {
		return "", "", err
	}

	storedRef = strings.TrimSpace(ref)
	if _, isAlias := cfg.Aliases[storedRef]; !isAlias && !strings.Contains(storedRef, ":") {
		for name, p := range cfg.Providers {
			if p.Kind == pcfg.Kind && p.BaseURL == pcfg.BaseURL && p.DefaultModel == pcfg.DefaultModel {
				storedRef = name + ":" + model
				break
			}
		}
	}

	home := Home()
	if err := os.MkdirAll(home, 0o755); err != nil {
		return "", "", err
	}
	cfgPath := filepath.Join(home, "config.yaml")

	var doc yaml.Node
	raw, rerr := os.ReadFile(cfgPath)
	if rerr == nil && len(strings.TrimSpace(string(raw))) > 0 {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return "", "", fmt.Errorf("%s: %w", cfgPath, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{
			Kind: yaml.DocumentNode,
			Content: []*yaml.Node{{
				Kind: yaml.MappingNode,
			}},
		}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", "", fmt.Errorf("%s: format YAML root bukan mapping", cfgPath)
	}

	updated := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "default_model" {
			root.Content[i+1].Kind = yaml.ScalarNode
			root.Content[i+1].Tag = "!!str"
			root.Content[i+1].Value = storedRef
			updated = true
			break
		}
	}
	if !updated {
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "default_model"}
		valNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: storedRef}
		root.Content = append([]*yaml.Node{keyNode, valNode}, root.Content...)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", "", err
	}
	tmp := cfgPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmp, cfgPath); err != nil {
		_ = os.Remove(tmp)
		return "", "", err
	}
	return model, storedRef, nil
}
