package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// StreamPort is one port claim of a stream: a (transport, port) pair.
// 53/TCP and 53/UDP are two distinct claims. Transport is "TCP" or "UDP".
type StreamPort struct {
	Transport string `json:"transport"`
	Port      int    `json:"port"`
}

// StreamPorts is the set of port claims of a stream, stored as JSONB on
// streams.ports.
type StreamPorts []StreamPort

// Value implements driver.Valuer. A nil list is stored as the empty array so
// the NOT NULL column never receives JSON null.
func (p StreamPorts) Value() (driver.Value, error) {
	if p == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(p)
}

// Scan implements sql.Scanner.
func (p *StreamPorts) Scan(value interface{}) error {
	if value == nil {
		*p = nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("failed to scan StreamPorts: unsupported type")
	}

	return json.Unmarshal(bytes, p)
}

// Has reports whether the exact (transport, port) claim is present.
func (p StreamPorts) Has(transport string, port int) bool {
	for _, c := range p {
		if c.Transport == transport && c.Port == port {
			return true
		}
	}
	return false
}

// Normalize returns a copy with upper-cased transports, sorted TCP before UDP
// and then by port. It does not remove duplicates.
func (p StreamPorts) Normalize() StreamPorts {
	out := make(StreamPorts, len(p))
	for i, c := range p {
		out[i] = StreamPort{Transport: strings.ToUpper(c.Transport), Port: c.Port}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Transport != out[j].Transport {
			return out[i].Transport < out[j].Transport
		}
		return out[i].Port < out[j].Port
	})
	return out
}
