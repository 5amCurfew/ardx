package lib

import "testing"

// ──────────────────────────────────────────────────────────────────────────────
// parsePorts
// ──────────────────────────────────────────────────────────────────────────────

const boardListJSON = `{
  "detected_ports": [
    {"port": {"address": "/dev/cu.Bluetooth-Incoming-Port", "label": "/dev/cu.Bluetooth-Incoming-Port",
      "protocol": "serial", "protocol_label": "Serial Port", "properties": {}}},
    {"port": {"address": "/dev/cu.usbserial-110", "label": "/dev/cu.usbserial-110", "protocol": "serial",
      "protocol_label": "Serial Port (USB)", "properties": {"vid": "0x1A86", "pid": "0x7523"}}},
    {"matching_boards": [{"name": "Arduino UNO", "fqbn": "arduino:avr:uno"}],
     "port": {"address": "/dev/cu.usbmodem1101", "label": "/dev/cu.usbmodem1101", "protocol": "serial",
      "protocol_label": "Serial Port (USB)", "properties": {"vid": "0x2341", "pid": "0x0043", "serialNumber": "ABC"}}}
  ]
}`

func TestParsePorts_ClassifiesAndSortsArduinoFirst(t *testing.T) {
	ports, err := parsePorts([]byte(boardListJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 3 {
		t.Fatalf("expected 3 ports, got %d", len(ports))
	}
	want := []struct {
		addr string
		kind PortKind
	}{
		{"/dev/cu.usbmodem1101", PortArduino},
		{"/dev/cu.usbserial-110", PortUSBSerial},
		{"/dev/cu.Bluetooth-Incoming-Port", PortOther},
	}
	for i, w := range want {
		if ports[i].Address != w.addr || ports[i].Kind != w.kind {
			t.Errorf("port %d: got %s (kind %d), want %s (kind %d)", i, ports[i].Address, ports[i].Kind, w.addr, w.kind)
		}
	}
	if ports[0].FQBN() != "arduino:avr:uno" {
		t.Errorf("expected FQBN arduino:avr:uno, got %q", ports[0].FQBN())
	}
	if ports[1].VID != "0x1a86" {
		t.Errorf("expected VID lower-cased, got %q", ports[1].VID)
	}
	if ports[0].SerialNumber != "ABC" {
		t.Errorf("expected serial number ABC, got %q", ports[0].SerialNumber)
	}
}

func TestParsePorts_LegacyArrayFormat(t *testing.T) {
	ports, err := parsePorts([]byte(`[{"port": {"address": "/dev/ttyACM0", "protocol": "serial"},
		"matching_boards": [{"name": "Arduino Mega", "fqbn": "arduino:avr:mega"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 1 || ports[0].FQBN() != "arduino:avr:mega" {
		t.Fatalf("unexpected ports: %+v", ports)
	}
	if ports[0].Label != "/dev/ttyACM0" {
		t.Errorf("expected label to default to address, got %q", ports[0].Label)
	}
}

func TestParsePorts_InvalidJSON(t *testing.T) {
	if _, err := parsePorts([]byte("not json")); err == nil {
		t.Error("expected an error for invalid JSON")
	}
}

func TestClassifyPort_ArduinoVIDWithoutMatchingBoard(t *testing.T) {
	if k := classifyPort(Port{VID: "0x2341"}); k != PortArduino {
		t.Errorf("expected PortArduino for Arduino VID, got %d", k)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Port.Description / FQBN
// ──────────────────────────────────────────────────────────────────────────────

func TestPortDescription(t *testing.T) {
	cases := []struct {
		port Port
		want string
	}{
		{Port{Boards: []Board{{"Arduino UNO", "arduino:avr:uno"}}}, "Arduino UNO (arduino:avr:uno)"},
		{Port{Boards: []Board{{"A", "x:y:a"}, {"B", "x:y:b"}}}, "A / B"},
		{Port{VID: "0x2341", PID: "0x0043", Kind: PortArduino}, "Arduino board (0x2341:0x0043), board type unknown"},
		{Port{VID: "0x1a86", Kind: PortUSBSerial}, "USB serial (CH340), board type unknown"},
		{Port{ProtocolLabel: "Serial Port", Kind: PortOther}, "Serial Port"},
	}
	for _, c := range cases {
		if got := c.port.Description(); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestPortFQBN_AmbiguousIsEmpty(t *testing.T) {
	p := Port{Boards: []Board{{"A", "x:y:a"}, {"B", "x:y:b"}}}
	if p.FQBN() != "" {
		t.Errorf("expected empty FQBN with multiple boards, got %q", p.FQBN())
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// parseBoards / platformID
// ──────────────────────────────────────────────────────────────────────────────

func TestParseBoards_SortedByName(t *testing.T) {
	boards, err := parseBoards([]byte(`{"boards": [
		{"name": "Arduino UNO", "fqbn": "arduino:avr:uno"},
		{"name": "arduino Mega", "fqbn": "arduino:avr:mega"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if boards[0].FQBN != "arduino:avr:mega" || boards[1].FQBN != "arduino:avr:uno" {
		t.Errorf("expected case-insensitive name sort, got %+v", boards)
	}
}

func TestPlatformID(t *testing.T) {
	if id, err := platformID("arduino:avr:nano:cpu=atmega328old"); err != nil || id != "arduino:avr" {
		t.Errorf("got %q, %v", id, err)
	}
	for _, bad := range []string{"", "arduino", "arduino:avr", "arduino::uno"} {
		if _, err := platformID(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}
