// Package protocol validates the version 1 room entry wire contract.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"roomserver/internal/room"
	"strconv"
	"unicode/utf8"
)

type Request struct {
	Op     string
	ID     *string
	Create room.CreateOptions
	Join   room.JoinOptions
}
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Envelope struct {
	Version   int     `json:"version"`
	Op        string  `json:"op"`
	RequestID *string `json:"request_id"`
	Payload   any     `json:"payload"`
	Error     *Error  `json:"error"`
}

var invalid = errors.New("Invalid message.")

func Event(op string, p any) Envelope { return Envelope{Version: 1, Op: op, Payload: p} }
func Response(r Request, p any, e error) Envelope {
	v := Envelope{Version: 1, Op: r.Op, RequestID: r.ID, Payload: p}
	if e != nil {
		v.Payload = nil
		v.Error = &Error{Code: room.CodeOf(e), Message: "Request failed."}
		var re *room.Error
		if errors.As(e, &re) {
			v.Error.Message = re.Message
		} else if v.Error.Code == "REQUEST_TIMEOUT" {
			v.Error.Message = "Request timed out."
		}
	}
	return v
}
func Invalid(r Request) Envelope {
	return Envelope{Version: 1, Op: r.Op, RequestID: r.ID, Error: &Error{Code: "INVALID_MESSAGE", Message: "Invalid message."}}
}
func Decode(data []byte) (Request, error) {
	var r Request
	if !utf8.Valid(data) || !validUnicodeEscapes(data) {
		return r, invalid
	}
	// Reject duplicate keys before decoding, including keys in nested payloads.
	d := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueValue(d); err != nil {
		return r, invalid
	}
	if _, err := d.Token(); err != io.EOF {
		return r, invalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return r, invalid
	}
	if raw, ok := fields["op"]; ok {
		json.Unmarshal(raw, &r.Op)
	}
	if raw, ok := fields["request_id"]; ok && !bytes.Equal(raw, []byte("null")) {
		var id string
		if json.Unmarshal(raw, &id) == nil {
			r.ID = &id
		}
	}
	if len(fields) != 5 || r.ID == nil || *r.ID == "" || len(*r.ID) > 128 || len(r.Op) > 64 {
		return r, invalid
	}
	var version int
	if json.Unmarshal(fields["version"], &version) != nil || version != 1 || !bytes.Equal(fields["error"], []byte("null")) {
		return r, invalid
	}
	for k := range fields {
		switch k {
		case "version", "op", "request_id", "payload", "error":
		default:
			return r, invalid
		}
	}
	p, e := object(fields["payload"])
	if e != nil {
		return r, invalid
	}
	switch r.Op {
	case "room.create":
		if !keys(p, "capacity", "visibility", "password") {
			return r, invalid
		}
		r.Create.Capacity = 6
		r.Create.Visibility = "public"
		if v, ok := p["capacity"]; ok {
			if isNull(v) || json.Unmarshal(v, &r.Create.Capacity) != nil || r.Create.Capacity < 1 || r.Create.Capacity > 6 {
				return r, invalid
			}
		}
		if v, ok := p["visibility"]; ok {
			if isNull(v) || json.Unmarshal(v, &r.Create.Visibility) != nil || (r.Create.Visibility != "public" && r.Create.Visibility != "private") {
				return r, invalid
			}
		}
		r.Create.Password, e = password(p)
		if e != nil || (r.Create.Visibility == "public" && r.Create.Password != nil) || (r.Create.Visibility == "private" && r.Create.Password == nil) {
			return r, invalid
		}
	case "room.join":
		if !keys(p, "room_number", "password") {
			return r, invalid
		}
		if json.Unmarshal(p["room_number"], &r.Join.RoomNumber) != nil || len(r.Join.RoomNumber) != 6 {
			return r, invalid
		}
		for _, c := range r.Join.RoomNumber {
			if c < '0' || c > '9' {
				return r, invalid
			}
		}
		r.Join.Password, e = password(p)
		if e != nil {
			return r, invalid
		}
	case "room.list", "room.leave":
		if len(p) != 0 {
			return r, invalid
		}
	default:
		return r, invalid
	}
	return r, nil
}
func isNull(v []byte) bool { return bytes.Equal(bytes.TrimSpace(v), []byte("null")) }
func object(v []byte) (map[string]json.RawMessage, error) {
	var p map[string]json.RawMessage
	if len(v) == 0 || json.Unmarshal(v, &p) != nil || p == nil {
		return nil, invalid
	}
	return p, nil
}
func keys(p map[string]json.RawMessage, allowed ...string) bool {
	for k := range p {
		found := false
		for _, a := range allowed {
			if k == a {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func password(p map[string]json.RawMessage) (*string, error) {
	v, ok := p["password"]
	if !ok {
		return nil, nil
	}
	var s string
	if isNull(v) || json.Unmarshal(v, &s) != nil || utf8.RuneCountInString(s) < 4 || utf8.RuneCountInString(s) > 64 {
		return nil, invalid
	}
	return &s, nil
}
func uniqueValue(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return invalid
			}
			seen[s] = true
			if e = uniqueValue(d); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e = uniqueValue(d); e != nil {
				return e
			}
		}
	default:
		return invalid
	}
	_, e = d.Token()
	return e
}

// Validate all JSON strings before encoding/json can replace lone surrogates.
func validUnicodeEscapes(data []byte) bool {
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		code, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code < 0xd800 || code > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return !inString
}
