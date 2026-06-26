package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
)

const MaxUDPPacket = 64 * 1024

type UDPHeader struct {
	Magic    string `json:"magic"`
	RouteID  string `json:"route_id"`
	HopIndex int    `json:"hop_index"`
	Secret   string `json:"secret"`
}

func EncodeUDPFrame(header UDPHeader, payload []byte) ([]byte, error) {
	header.Magic = Magic
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	if len(headerBytes) > 4096 {
		return nil, errors.New("udp header is too large")
	}
	if len(payload)+len(headerBytes)+2 > MaxUDPPacket {
		return nil, errors.New("udp frame is too large")
	}
	out := make([]byte, 2+len(headerBytes)+len(payload))
	binary.BigEndian.PutUint16(out[:2], uint16(len(headerBytes)))
	copy(out[2:], headerBytes)
	copy(out[2+len(headerBytes):], payload)
	return out, nil
}

func DecodeUDPFrame(frame []byte) (UDPHeader, []byte, error) {
	if len(frame) < 2 {
		return UDPHeader{}, nil, errors.New("udp frame is too small")
	}
	headerLen := int(binary.BigEndian.Uint16(frame[:2]))
	if headerLen == 0 || headerLen > 4096 || 2+headerLen > len(frame) {
		return UDPHeader{}, nil, errors.New("invalid udp header size")
	}
	var header UDPHeader
	if err := json.Unmarshal(frame[2:2+headerLen], &header); err != nil {
		return UDPHeader{}, nil, err
	}
	if header.Magic != Magic {
		return UDPHeader{}, nil, errors.New("invalid udp header magic")
	}
	return header, frame[2+headerLen:], nil
}
