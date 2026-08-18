package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// defaultConfigMode is what a configuration file created from nothing gets. It
// holds no secret - the WireGuard keys live in the .conf files, which the
// parser never reads - so it is readable like any other dotfile.
const defaultConfigMode os.FileMode = 0o644

// AddToGroup adds a tunnel to a group in the configuration file, leaving
// everything else in the file exactly as it was.
//
// The file is edited as a node tree rather than re-marshalled from Config.
// This is a file a person maintains by hand: marshalling the struct back would
// write a correct configuration that had silently lost every comment in it,
// along with the order they were written in. A tool that eats your comments the
// first time you use it is a tool you stop using.
//
// A tunnel already in the group is left alone rather than added twice.
func AddToGroup(path, group, tunnel string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return writeNew(path, group, tunnel)
	}
	if err != nil {
		return err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		// An empty file parses without error and without a document to edit.
		return writeNew(path, group, tunnel)
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: the configuration is not a mapping", path)
	}

	groups := childOf(root, "groups")
	if groups == nil {
		groups = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content, scalar("groups"), groups)
	}
	if groups.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: groups is not a mapping", path)
	}

	members := childOf(groups, group)
	if members == nil {
		members = &yaml.Node{Kind: yaml.SequenceNode}
		groups.Content = append(groups.Content, scalar(group), members)
	}
	if members.Kind != yaml.SequenceNode {
		// `all:` with nothing after it parses as a null scalar. Turning it into
		// a sequence in place keeps whatever comment sits on the key.
		members.Kind = yaml.SequenceNode
		members.Tag = ""
		members.Value = ""
		members.Content = nil
	}

	for _, member := range members.Content {
		if member.Value == tunnel {
			return nil
		}
	}
	members.Content = append(members.Content, scalar(tunnel))

	return replace(path, &doc)
}

// childOf returns the value node of a key in a mapping, or nil. A mapping's
// Content alternates key, value, key, value.
func childOf(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func scalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: value}
}

// writeNew creates a configuration holding nothing but the group, for the case
// where there was no file at all. Everything else keeps its built-in default,
// and writing those out would freeze today's values into the user's file.
func writeNew(path, group, tunnel string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("groups:\n  %s:\n    - %s\n", group, tunnel)
	return os.WriteFile(path, []byte(body), defaultConfigMode)
}

// replace rewrites the file through a temporary one in the same directory, so
// an interrupted write cannot leave the configuration truncated.
func replace(path string, doc *yaml.Node) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	// Close flushes, so its error is the encoder's too. Neither can fail for a
	// document that was just parsed out of a file and written into a buffer,
	// which is why one branch covers both.
	err := enc.Encode(doc)
	if closeErr := enc.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		// NOT TESTED: the document was parsed out of a file moments ago and is
		// written into a buffer, so neither call has a way to fail. Reaching
		// this would mean the node tree built above is malformed, which is a
		// bug rather than a condition.
		// See docs/coverage-gaps.md, "profile.replace".
		return fmt.Errorf("render %s: %w", path, err)
	}

	mode := defaultConfigMode
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}

	// Written beside the original and moved over it, so an interrupted write
	// cannot leave the configuration truncated.
	tmp := path + ".tmp"
	defer os.Remove(tmp) //nolint:errcheck // already gone once the rename succeeded

	// The umask can take a bite out of mode, which tightens the file and never
	// widens it. Tightening a configuration is safe; a chmod to put the bit
	// back would be a branch that cannot fail on a file just written.
	if err := os.WriteFile(tmp, buf.Bytes(), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
