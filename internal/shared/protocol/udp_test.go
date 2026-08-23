package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestUDPDatagramFrameRejectsOversizedIdentifiers(t *testing.T) {
	frame := UDPDatagramFrame{
		ForwardID: "forward",
		SessionID: string(bytes.Repeat([]byte{'s'}, MaxUDPIdentifierBytes+1)),
		Payload:   []byte("payload"),
	}
	if err := WriteUDPDatagramFrame(&bytes.Buffer{}, frame); err == nil {
		t.Fatal("expected oversized UDP session identifier to be rejected")
	}
}

func TestRelayHelloRejectsTrailingJSON(t *testing.T) {
	payload := []byte(`{"magic":"NYAR1","version":1,"tunnel_id":"tunnel","forward_id":"forward","from_stage_index":0,"to_stage_index":1,"network":"tcp"} {}`)
	var frame bytes.Buffer
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(payload)))
	_, _ = frame.Write(size[:])
	_, _ = frame.Write(payload)

	if _, err := ReadHello(&frame); err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
}
