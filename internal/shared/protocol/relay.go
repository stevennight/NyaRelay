package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
)

const Magic = "NYAR1"

type RelayHello struct {
	Magic    string `json:"magic"`
	RouteID  string `json:"route_id"`
	HopIndex int    `json:"hop_index"`
	Network  string `json:"network"`
	Secret   string `json:"secret"`
}

func WriteHello(w io.Writer, hello RelayHello) error {
	hello.Magic = Magic
	payload, err := json.Marshal(hello)
	if err != nil {
		return err
	}
	if len(payload) > 64*1024 {
		return errors.New("relay hello is too large")
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(payload)))
	if _, err := w.Write(size[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func ReadHello(r io.Reader) (RelayHello, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return RelayHello{}, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > 64*1024 {
		return RelayHello{}, errors.New("invalid relay hello size")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return RelayHello{}, err
	}
	var hello RelayHello
	if err := json.Unmarshal(payload, &hello); err != nil {
		return RelayHello{}, err
	}
	if hello.Magic != Magic {
		return RelayHello{}, errors.New("invalid relay hello magic")
	}
	return hello, nil
}
