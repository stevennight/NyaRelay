package protocol

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

type shortUDPFrameWriter struct {
	w   io.Writer
	max int
}

func (w shortUDPFrameWriter) Write(payload []byte) (int, error) {
	if len(payload) > w.max {
		payload = payload[:w.max]
	}
	return w.w.Write(payload)
}

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

func TestUDPDatagramFrameSupportsMultipleFramesAndShortWrites(t *testing.T) {
	var wire bytes.Buffer
	writer := shortUDPFrameWriter{w: &wire, max: 3}
	frames := []UDPDatagramFrame{
		{ForwardID: "forward", SessionID: "session", Payload: []byte("first")},
		{ForwardID: "forward", SessionID: "session", Payload: []byte("second")},
	}
	for _, frame := range frames {
		if err := WriteUDPDatagramFrame(writer, frame); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range frames {
		got, err := ReadUDPDatagramFrame(&wire)
		if err != nil {
			t.Fatal(err)
		}
		if got.ForwardID != want.ForwardID || got.SessionID != want.SessionID || string(got.Payload) != string(want.Payload) {
			t.Fatalf("frame = %+v, want %+v", got, want)
		}
	}
}

func TestUDPDatagramFrameAcceptsMaximumPayload(t *testing.T) {
	want := bytes.Repeat([]byte{'x'}, MaxUDPPacket)
	var wire bytes.Buffer
	if err := WriteUDPDatagramFrame(&wire, UDPDatagramFrame{ForwardID: "forward", SessionID: "session", Payload: want}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadUDPDatagramFrame(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Payload) != len(want) || !bytes.Equal(got.Payload, want) {
		t.Fatalf("payload length = %d, want %d", len(got.Payload), len(want))
	}
}

func TestUDPDatagramFrameRejectsOversizedPayload(t *testing.T) {
	payload := bytes.Repeat([]byte{'x'}, MaxUDPPacket+1)
	if err := WriteUDPDatagramFrame(&bytes.Buffer{}, UDPDatagramFrame{ForwardID: "forward", SessionID: "session", Payload: payload}); err == nil {
		t.Fatal("expected oversized UDP payload to be rejected")
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
