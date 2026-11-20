package lib

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PortKind classifies how confident ardx is that a port has an Arduino on it.
type PortKind int

const (
	PortArduino   PortKind = iota // identified as an Arduino (matching board or Arduino USB vendor id)
	PortUSBSerial                 // a USB-serial chip common on Arduino clones (CH340, FTDI, CP210x, ...)
	PortOther                     // anything else (Bluetooth, modems, ...)
)

// arduinoVIDs are USB vendor ids belonging to Arduino and close relatives
// whose boards ship with native USB.
var arduinoVIDs = map[string]string{
	"0x2341": "Arduino",
	"0x2a03": "Arduino",
	"0x1b4f": "SparkFun",
	"0x239a": "Adafruit",
}

// usbSerialVIDs are USB vendor ids of USB-serial converter chips that clone
// boards (Nano, Uno, Mega clones, ESP boards) commonly use.
var usbSerialVIDs = map[string]string{
	"0x1a86": "CH340",
	"0x0403": "FTDI",
	"0x10c4": "CP210x",
	"0x067b": "PL2303",
}

// Board is a board definition known to arduino-cli.
type Board struct {
	Name string `json:"name"`
	FQBN string `json:"fqbn"`
}

// Port is a port reported by `arduino-cli board list`, along with any boards
// arduino-cli matched to it.
type Port struct {
	Address       string
	Label         string
	Protocol      string
	ProtocolLabel string
	VID           string
	PID           string
	SerialNumber  string
	Boards        []Board
	Kind          PortKind
}

// Description is a short human-readable summary of what is on the port.
func (p Port) Description() string {
	switch {
	case len(p.Boards) == 1:
		return fmt.Sprintf("%s (%s)", p.Boards[0].Name, p.Boards[0].FQBN)
	case len(p.Boards) > 1:
		names := make([]string, len(p.Boards))
		for i, b := range p.Boards {
			names[i] = b.Name
		}
		return strings.Join(names, " / ")
	case p.Kind == PortArduino:
		return fmt.Sprintf("%s board (%s:%s), board type unknown", arduinoVIDs[p.VID], p.VID, p.PID)
	case p.Kind == PortUSBSerial:
		return fmt.Sprintf("USB serial (%s), board type unknown", usbSerialVIDs[p.VID])
	case p.VID != "":
		return fmt.Sprintf("%s (%s:%s)", p.ProtocolLabel, p.VID, p.PID)
	default:
		return p.ProtocolLabel
	}
}

// FQBN returns the board's fully qualified name if exactly one board matched.
func (p Port) FQBN() string {
	if len(p.Boards) == 1 {
		return p.Boards[0].FQBN
	}
	return ""
}

// detectedPortJSON mirrors one entry of `arduino-cli board list --format json`.
type detectedPortJSON struct {
	MatchingBoards []Board `json:"matching_boards"`
	Port           struct {
		Address       string         `json:"address"`
		Label         string         `json:"label"`
		Protocol      string         `json:"protocol"`
		ProtocolLabel string         `json:"protocol_label"`
		Properties    map[string]any `json:"properties"`
	} `json:"port"`
}

// parsePorts converts `arduino-cli board list --format json` output into
// Ports, classified and sorted with Arduino ports first. Both the current
// object format and the bare array emitted by arduino-cli < 1.0 are accepted.
func parsePorts(data []byte) ([]Port, error) {
	var wrapped struct {
		DetectedPorts []detectedPortJSON `json:"detected_ports"`
	}
	var entries []detectedPortJSON
	if err := json.Unmarshal(data, &wrapped); err == nil {
		entries = wrapped.DetectedPorts
	} else if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parsing board list: %w", err)
	}

	ports := make([]Port, 0, len(entries))
	for _, e := range entries {
		p := Port{
			Address:       e.Port.Address,
			Label:         e.Port.Label,
			Protocol:      e.Port.Protocol,
			ProtocolLabel: e.Port.ProtocolLabel,
			VID:           strings.ToLower(propString(e.Port.Properties, "vid")),
			PID:           strings.ToLower(propString(e.Port.Properties, "pid")),
			SerialNumber:  propString(e.Port.Properties, "serialNumber"),
			Boards:        e.MatchingBoards,
		}
		if p.Label == "" {
			p.Label = p.Address
		}
		p.Kind = classifyPort(p)
		ports = append(ports, p)
	}
	sort.SliceStable(ports, func(i, j int) bool { return ports[i].Kind < ports[j].Kind })
	return ports, nil
}

// classifyPort decides how likely a port is to be an Arduino.
func classifyPort(p Port) PortKind {
	if len(p.Boards) > 0 {
		return PortArduino
	}
	if _, ok := arduinoVIDs[p.VID]; ok {
		return PortArduino
	}
	if _, ok := usbSerialVIDs[p.VID]; ok {
		return PortUSBSerial
	}
	return PortOther
}

// propString reads a port property as a string, tolerating non-string values.
func propString(props map[string]any, key string) string {
	v, ok := props[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// parseBoards converts `arduino-cli board listall --format json` output into
// Boards, sorted by name.
func parseBoards(data []byte) ([]Board, error) {
	var parsed struct {
		Boards []Board `json:"boards"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parsing board listall: %w", err)
	}
	sort.SliceStable(parsed.Boards, func(i, j int) bool {
		return strings.ToLower(parsed.Boards[i].Name) < strings.ToLower(parsed.Boards[j].Name)
	})
	return parsed.Boards, nil
}

// platformID returns the "vendor:arch" platform part of an FQBN.
func platformID(fqbn string) (string, error) {
	parts := strings.Split(fqbn, ":")
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", fmt.Errorf("invalid FQBN %q, expected vendor:arch:board", fqbn)
	}
	return parts[0] + ":" + parts[1], nil
}
