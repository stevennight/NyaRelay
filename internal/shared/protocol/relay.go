package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
)

const (
	Magic        = "NYAR1"
	HelloVersion = 1
)

type RelayHello struct {
	Magic          string `json:"magic"`
	Version        int    `json:"version"`
	TunnelID       string `json:"tunnel_id"`
	ForwardID      string `json:"forward_id"`
	FromStageIndex int    `json:"from_stage_index"`
	ToStageIndex   int    `json:"to_stage_index"`
	Network        string `json:"network"`
	Secret         string `json:"secret,omitempty"`
}

func WriteHello(w io.Writer, hello RelayHello) error {
	hello.Magic = Magic
	if hello.Version == 0 {
		hello.Version = HelloVersion
	}
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
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var hello RelayHello
	if err := decoder.Decode(&hello); err != nil {
		return RelayHello{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return RelayHello{}, errors.New("relay hello must contain one JSON value")
		}
		return RelayHello{}, err
	}
	if hello.Magic != Magic {
		return RelayHello{}, errors.New("invalid relay hello magic")
	}
	if hello.Version != HelloVersion {
		return RelayHello{}, errors.New("unsupported relay hello version")
	}
	return hello, nil
}
