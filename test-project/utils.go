package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// UtilityStruct represents a utility structure
type UtilityStruct struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
	Tags  []string `json:"tags"`
}

// ProcessData processes some data with utility functions
func ProcessData(data []byte) (*UtilityStruct, error) {
	var result UtilityStruct
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	// Process tags
	for i, tag := range result.Tags {
		result.Tags[i] = strings.ToUpper(tag)
	}

	return &result, nil
}

// WriteOutput writes processed data to an output stream
func WriteOutput(w io.Writer, data *UtilityStruct) error {
	output, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal output: %w", err)
	}

	_, err = w.Write(output)
	return err
}