package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// RelevanceGrade indicates how relevant a result is to a query.
type RelevanceGrade int

const (
	Irrelevant RelevanceGrade = 0
	Partial    RelevanceGrade = 1
	Relevant   RelevanceGrade = 2
	Perfect    RelevanceGrade = 3
)

// ExpectedResult is a ground-truth expected result for an eval query.
type ExpectedResult struct {
	NodeKey  string         `yaml:"nodeKey" json:"nodeKey"`
	Grade    RelevanceGrade `yaml:"grade" json:"grade"`
	NodeType string         `yaml:"nodeType,omitempty" json:"nodeType,omitempty"`
	Comment  string         `yaml:"comment,omitempty" json:"comment,omitempty"`
}

// EvalQuery is a single evaluation query with its expected results.
type EvalQuery struct {
	ID          string           `yaml:"id" json:"id"`
	Query       string           `yaml:"query" json:"query"`
	Description string           `yaml:"description,omitempty" json:"description,omitempty"`
	Category    string           `yaml:"category,omitempty" json:"category,omitempty"`
	Expected    []ExpectedResult `yaml:"expected" json:"expected"`
	Limit       int              `yaml:"limit,omitempty" json:"limit,omitempty"`
}

// EvalDataset is a collection of eval queries loaded from a file.
type EvalDataset struct {
	Name        string      `yaml:"name" json:"name"`
	Description string      `yaml:"description,omitempty" json:"description,omitempty"`
	Version     string      `yaml:"version,omitempty" json:"version,omitempty"`
	DefaultK    int         `yaml:"defaultK,omitempty" json:"defaultK,omitempty"`
	Queries     []EvalQuery `yaml:"queries" json:"queries"`
}

// LoadDataset reads an EvalDataset from a YAML or JSON file.
func LoadDataset(path string) (*EvalDataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset %s: %w", path, err)
	}

	var ds EvalDataset
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &ds); err != nil {
			return nil, fmt.Errorf("parse YAML dataset: %w", err)
		}
	case ".json":
		if err := json.Unmarshal(data, &ds); err != nil {
			return nil, fmt.Errorf("parse JSON dataset: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported dataset format %q (use .yaml or .json)", ext)
	}

	if len(ds.Queries) == 0 {
		return nil, fmt.Errorf("dataset %s has no queries", path)
	}
	if ds.DefaultK <= 0 {
		ds.DefaultK = 20
	}

	return &ds, nil
}

// RelevanceMap builds a nodeKey→grade lookup from the expected results.
func (q *EvalQuery) RelevanceMap() map[string]RelevanceGrade {
	m := make(map[string]RelevanceGrade, len(q.Expected))
	for _, e := range q.Expected {
		m[e.NodeKey] = e.Grade
	}
	return m
}
