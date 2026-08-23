package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
)

const (
	MaxUDPPacket          = 64 * 1024
	MaxUDPIdentifierBytes = 1024
)

type UDPDatagramFrame struct {
	ForwardID string `json:"forward_id"`
	SessionID string `json:"session_id"`
	Payload   []byte `json:"payload"`
}

func WriteUDPDatagramFrame(w io.Writer, frame UDPDatagramFrame) error {
	if len(frame.ForwardID) > MaxUDPIdentifierBytes || len(frame.SessionID) > MaxUDPIdentifierBytes {
		return errors.New("udp frame identifier is too large")
	}
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if len(payload) == 0 || len(payload) > MaxUDPPacket+4096 {
		return errors.New("udp frame is too large")
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(payload)))
	if _, err := w.Write(size[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func ReadUDPDatagramFrame(r io.Reader) (UDPDatagramFrame, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return UDPDatagramFrame{}, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > MaxUDPPacket+4096 {
		return UDPDatagramFrame{}, errors.New("invalid udp frame size")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return UDPDatagramFrame{}, err
	}
	var frame UDPDatagramFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		return UDPDatagramFrame{}, err
	}
	if len(frame.ForwardID) > MaxUDPIdentifierBytes || len(frame.SessionID) > MaxUDPIdentifierBytes {
		return UDPDatagramFrame{}, errors.New("udp frame identifier is too large")
	}
	if len(frame.Payload) > MaxUDPPacket {
		return UDPDatagramFrame{}, errors.New("udp payload is too large")
	}
	return frame, nil
}
