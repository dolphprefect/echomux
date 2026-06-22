package audio

import (
	"encoding/json"
	"fmt"
)

type pwNode struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Info struct {
		Props map[string]any `json:"props"`
	} `json:"info"`
}

type pwLink struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Info struct {
		OutputNodeID int    `json:"output-node-id"`
		InputNodeID  int    `json:"input-node-id"`
		State        string `json:"state"`
	} `json:"info"`
}

// ParseLinks extracts all PipeWire links and their current state from pw-dump JSON output.
func ParseLinks(data []byte) ([]LinkInfo, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var out []LinkInfo
	for _, msg := range raw {
		var l pwLink
		if err := json.Unmarshal(msg, &l); err != nil {
			continue
		}
		if l.Type != "PipeWire:Interface:Link" {
			continue
		}
		if l.Info.State == "" {
			continue // skip links with missing state to avoid false-positive zombie detection
		}
		out = append(out, LinkInfo{
			ID:           l.ID,
			OutputNodeID: l.Info.OutputNodeID,
			InputNodeID:  l.Info.InputNodeID,
			State:        l.Info.State,
		})
	}
	return out, nil
}

func prop(n pwNode, key string) string {
	v, ok := n.Info.Props[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// ParseNodes extracts Bluetooth A2DP sink nodes from pw-dump JSON output.
func ParseNodes(data []byte) ([]Node, error) {
	raw, err := parsePWNodes(data)
	if err != nil {
		return nil, err
	}
	var out []Node
	for _, n := range raw {
		if prop(n, "media.class") != "Audio/Sink" {
			continue
		}
		mac := prop(n, "api.bluez5.address")
		if mac == "" {
			continue
		}
		out = append(out, Node{ID: n.ID, MAC: mac, Name: prop(n, "node.name")})
	}
	return out, nil
}

// ParseSources extracts Audio/Source nodes from pw-dump JSON output.
// This includes the rtp-source virtual node and any BT A2DP input (phone connected as speaker).
func ParseSources(data []byte) ([]Node, error) {
	raw, err := parsePWNodes(data)
	if err != nil {
		return nil, err
	}
	var out []Node
	for _, n := range raw {
		if prop(n, "media.class") != "Audio/Source" {
			continue
		}
		out = append(out, Node{
			ID:   n.ID,
			MAC:  prop(n, "api.bluez5.address"),
			Name: prop(n, "node.name"),
		})
	}
	return out, nil
}

// ParseSourceNodeID finds the PipeWire node ID for the named source node.
func ParseSourceNodeID(data []byte, nodeName string) (int, error) {
	sources, err := ParseSources(data)
	if err != nil {
		return 0, err
	}
	for _, n := range sources {
		if n.Name == nodeName {
			return n.ID, nil
		}
	}
	return 0, fmt.Errorf("source node %q not found in pw-dump output", nodeName)
}

// ParseNodeByName returns the PipeWire node ID with the given node.name, or -1 if not found.
func ParseNodeByName(data []byte, name string) (int, error) {
	raw, err := parsePWNodes(data)
	if err != nil {
		return -1, err
	}
	for _, n := range raw {
		if prop(n, "node.name") == name {
			return n.ID, nil
		}
	}
	return -1, nil
}

// ParseNodesByName returns a map of node.name → node.ID for all PipeWire nodes.
func ParseNodesByName(data []byte) (map[string]int, error) {
	raw, err := parsePWNodes(data)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(raw))
	for _, n := range raw {
		if name := prop(n, "node.name"); name != "" {
			out[name] = n.ID
		}
	}
	return out, nil
}

func parsePWNodes(data []byte) ([]pwNode, error) {
	var raw []pwNode
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	// filter to Interface:Node only
	var nodes []pwNode
	for _, n := range raw {
		if n.Type == "PipeWire:Interface:Node" {
			nodes = append(nodes, n)
		}
	}
	return nodes, nil
}
