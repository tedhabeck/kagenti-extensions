// Package edit implements abctl's in-place pipeline editor. The flow is:
// fetch the agent's ConfigMap via kubectl, locate the pipeline: subtree,
// open just that subtree in the user's $EDITOR, splice the edit back into
// the original ConfigMap manifest, kubectl apply --server-side, then poll
// /reload/status until the framework reloads.
//
// All kubectl interaction goes through the Runner injection seam so tests
// can stub it out.
package edit

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// FindPipelineRange returns the byte offsets [start, end) in innerYAML
// that span the "pipeline:" subtree, including the "pipeline:" key line
// itself but not any following top-level keys. Used by the editor to
// extract just the pipeline subtree for the user, and by Splice to
// replace it with the user's edit.
//
// Returns an error if innerYAML is not valid YAML or if no top-level
// "pipeline" key exists.
func FindPipelineRange(innerYAML []byte) (start, end int, err error) {
	var root yaml.Node
	if err := yaml.Unmarshal(innerYAML, &root); err != nil {
		return 0, 0, fmt.Errorf("parse runtime YAML: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return 0, 0, fmt.Errorf("runtime YAML is not a document")
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return 0, 0, fmt.Errorf("runtime YAML root is not a mapping")
	}

	// Children of a MappingNode alternate key, value, key, value, ...
	// Find the index of the "pipeline" key, capture its line, and find
	// the next sibling's line (or end-of-document if it's the last key).
	pipelineKeyIdx := -1
	for i := 0; i < len(doc.Content); i += 2 {
		k := doc.Content[i]
		if k.Value == "pipeline" {
			pipelineKeyIdx = i
			break
		}
	}
	if pipelineKeyIdx == -1 {
		return 0, 0, fmt.Errorf("no top-level pipeline key in runtime YAML")
	}

	pipelineKeyLine := doc.Content[pipelineKeyIdx].Line // 1-indexed
	var nextKeyLine int                                  // 1-indexed; 0 if pipeline is last
	if pipelineKeyIdx+2 < len(doc.Content) {
		nextKeyLine = doc.Content[pipelineKeyIdx+2].Line
	}

	// Map line numbers to byte offsets. yaml.v3 Line is 1-indexed.
	lineStarts := []int{0} // lineStarts[i] = byte offset where line i+1 starts
	for i, b := range innerYAML {
		if b == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}

	if pipelineKeyLine < 1 || pipelineKeyLine > len(lineStarts) {
		return 0, 0, fmt.Errorf("pipeline key line %d out of range", pipelineKeyLine)
	}
	start = lineStarts[pipelineKeyLine-1]

	if nextKeyLine == 0 {
		end = len(innerYAML)
	} else {
		if nextKeyLine < 1 || nextKeyLine > len(lineStarts) {
			return 0, 0, fmt.Errorf("next-key line %d out of range", nextKeyLine)
		}
		end = lineStarts[nextKeyLine-1]
	}
	return start, end, nil
}

// Splice replaces the byte range [start, end) of innerYAML with newSubtree
// and returns the result. Used to apply the user's edit to just the pipeline
// subtree, leaving everything outside it byte-for-byte unchanged. Comments,
// blank lines, and field ordering outside the pipeline subtree all survive.
func Splice(innerYAML []byte, start, end int, newSubtree []byte) []byte {
	var b bytes.Buffer
	b.Grow(len(innerYAML) - (end - start) + len(newSubtree))
	b.Write(innerYAML[:start])
	b.Write(newSubtree)
	b.Write(innerYAML[end:])
	return b.Bytes()
}

// BuildManifest takes the original ConfigMap YAML manifest (as returned by
// kubectl get cm -o yaml) and a new inner runtime YAML (the contents that
// belong in data.config.yaml). Returns a manifest ready for kubectl apply.
//
// The manifest passes through yaml.v3 so the outer structure (apiVersion,
// kind, metadata, etc.) is preserved. Only data.config.yaml is replaced.
// Comments inside the inner runtime YAML survive because we set the
// data.config.yaml value to a literal block (|) string carrying newInner
// verbatim.
func BuildManifest(origCMYAML, newInner []byte) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(origCMYAML, &root); err != nil {
		return nil, fmt.Errorf("parse ConfigMap manifest: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, fmt.Errorf("ConfigMap manifest is not a document")
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("ConfigMap manifest root is not a mapping")
	}

	// Find data → config.yaml.
	var dataNode *yaml.Node
	for i := 0; i < len(doc.Content); i += 2 {
		if doc.Content[i].Value == "data" {
			dataNode = doc.Content[i+1]
			break
		}
	}
	if dataNode == nil || dataNode.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("ConfigMap has no data: mapping")
	}
	var configValueNode *yaml.Node
	for i := 0; i < len(dataNode.Content); i += 2 {
		if dataNode.Content[i].Value == "config.yaml" {
			configValueNode = dataNode.Content[i+1]
			break
		}
	}
	if configValueNode == nil {
		return nil, fmt.Errorf("ConfigMap data has no config.yaml key")
	}

	// Set the value to a literal-block scalar carrying newInner.
	configValueNode.Kind = yaml.ScalarNode
	configValueNode.Tag = "!!str"
	configValueNode.Style = yaml.LiteralStyle
	configValueNode.Value = string(newInner)

	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, fmt.Errorf("emit ConfigMap manifest: %w", err)
	}
	return out, nil
}
