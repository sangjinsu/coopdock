package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func TestDecodeStrict(t *testing.T) {
	valid := `{"version":1,"op":"room.create","request_id":"a","payload":{},"error":null}`
	r, err := Decode([]byte(valid))
	if err != nil || r.Create.Capacity != 6 || r.Create.Visibility != "public" {
		t.Fatalf("defaults: %+v %v", r, err)
	}
	invalid := []string{
		`{} `, valid + valid, `{"version":1,`,
		`{"version":2,"op":"room.list","request_id":"a","payload":{},"error":null}`,
		`{"version":1,"op":"room.list","payload":{},"error":null}`,
		`{"version":1,"op":"room.list","request_id":5,"payload":{},"error":null}`,
		`{"version":1,"op":"room.list","request_id":"a","payload":null,"error":null}`,
		`{"version":1,"op":"room.list","request_id":"a","payload":[],"error":null}`,
		`{"version":1,"op":"room.list","request_id":"a","payload":{},"error":{}}`,
		`{"version":1,"op":"room.list","request_id":"a","payload":{},"error":null,"session_id":"forged"}`,
		`{"version":1,"op":"room.list","request_id":"a","payload":{},"error":null,"version":1}`,
	}
	for _, p := range []string{`{"capacity":0}`, `{"capacity":7}`, `{"capacity":1.5}`, `{"capacity":null}`, `{"visibility":""}`, `{"visibility":null}`, `{"password":""}`, `{"password":null}`, `{"visibility":"private"}`, `{"visibility":"private","password":123}`, `{"visibility":"private","password":"abc"}`, `{"session_id":"forged"}`} {
		invalid = append(invalid, fmt.Sprintf(`{"version":1,"op":"room.create","request_id":"a","payload":%s,"error":null}`, p))
	}
	for _, p := range []string{`{}`, `{"room_number":123456}`, `{"room_number":"１２３４５６"}`, `{"room_number":"12345"}`, `{"room_number":"1234567"}`, `{"room_number":"123456","password":null}`} {
		invalid = append(invalid, fmt.Sprintf(`{"version":1,"op":"room.join","request_id":"a","payload":%s,"error":null}`, p))
	}
	for _, op := range []string{"room.unknown", "game.action", "admin.drain", "session.guest"} {
		invalid = append(invalid, fmt.Sprintf(`{"version":1,"op":%q,"request_id":"a","payload":{},"error":null}`, op))
	}
	invalid = append(invalid, string(append([]byte(valid[:20]), 0xff)))
	for _, input := range invalid {
		t.Run(input, func(t *testing.T) {
			if _, e := Decode([]byte(input)); e == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
}
func TestPasswordAndNumberPreserved(t *testing.T) {
	r, e := Decode([]byte(`{"version":1,"op":"room.join","request_id":"a","payload":{"room_number":"004271","password":" 가 나 "},"error":null}`))
	if e != nil || r.Join.RoomNumber != "004271" || *r.Join.Password != " 가 나 " {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestRejectsUnpairedUnicodeEscapes(t *testing.T) {
	for _, op := range []string{"room.create", "room.join"} {
		for _, password := range []string{`"\ud800abc"`, `"\udfffabc"`, `"\ud800\u0041abc"`, `"\ud800\ud800abc"`, `"\udfff\ud800abc"`} {
			t.Run(op+"/"+password, func(t *testing.T) {
				prefix := `"visibility":"private"`
				if op == "room.join" {
					prefix = `"room_number":"004271"`
				}
				input := fmt.Sprintf(`{"version":1,"op":%q,"request_id":"valid","payload":{%s,"password":%s},"error":null}`, op, prefix, password)
				if _, e := Decode([]byte(input)); e == nil {
					t.Fatal("unpaired surrogate accepted")
				}
			})
		}
	}
	for _, input := range []string{
		`{"version":1,"op":"room.list","request_id":"\ud800abc","payload":{},"error":null}`,
		`{"version":1,"op":"room.list","request_id":"\udfffabc","payload":{},"error":null}`,
		`{"version":1,"op":"room.list","request_id":"abc","payload":{},"error":null,"\ud800":0}`,
	} {
		if _, e := Decode([]byte(input)); e == nil {
			t.Fatal("invalid envelope string accepted")
		}
	}
}
func TestValidUnicodeEscapesPreserveValues(t *testing.T) {
	for _, pair := range []struct{ raw, want string }{{`"\ud83d\ude00abc"`, "😀abc"}, {`"😀abc"`, "😀abc"}, {`"\ufffdabc"`, "�abc"}, {`"�abc"`, "�abc"}, {`"\\ud800abc"`, `\ud800abc`}, {`"\\udfffabc"`, `\udfffabc`}, {`"\\\ud83d\ude00abc"`, `\😀abc`}} {
		for _, op := range []string{"room.create", "room.join"} {
			t.Run(op+"/"+pair.raw, func(t *testing.T) {
				prefix := `"visibility":"private"`
				if op == "room.join" {
					prefix = `"room_number":"004271"`
				}
				input := fmt.Sprintf(`{"version":1,"op":%q,"request_id":"\ud83d\ude00-id","payload":{%s,"\u0070assword":%s},"error":null}`, op, prefix, pair.raw)
				r, e := Decode([]byte(input))
				if e != nil {
					t.Fatal(e)
				}
				password := r.Create.Password
				if op == "room.join" {
					password = r.Join.Password
				}
				if password == nil || *password != pair.want || r.ID == nil || *r.ID != "😀-id" {
					t.Fatal("valid string changed")
				}
			})
		}
	}
}

// Removing any required numeric or exact-key validation must fail this table.
func TestDecodeMovementStrict(t *testing.T) {
	valid := `{"version":1,"op":"game.action","request_id":"a","payload":{"match_id":"m","input_seq":1,"action":{"type":"move","direction":{"x":1,"y":0,"z":0}}},"error":null}`
	if _, err := Decode([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{`"z":0`, `"z":null`}, {`"z":0`, `"q":0`}, {`"z":0`, `"z":1e400`}, {`"y":0`, `"y":1e400`},
		{`"x":1`, `"x":1e400`}, {`"x":1`, `"x":1.1`}, {`"x":1`, `"x":-1.1`}, {`"x":1`, `"x":"1"`},
		{`"x":1`, `"x":1,"x":0`}, {`"x":1,`, ``}, {`,"y":0`, ``}, {`,"z":0`, ``},
		{`"direction":{"x":1,"y":0,"z":0}`, `"direction":null`},
		{`"direction":{"x":1,"y":0,"z":0}`, `"direction":[]`},
		{`"type":"move",`, ``}, {`"type":"move"`, `"type":null`}, {`"type":"move"`, `"type":"stop"`},
		{`"action":`, `"dt":0,"action":`}, {`"type":"move"`, `"type":"move","position":{}`},
		{`"input_seq":1`, `"input_seq":9007199254740992`}, {`"input_seq":1`, `"input_seq":1.5`},
		{`"input_seq":1`, `"input_seq":null`}, {`"input_seq":1`, `"input_seq":0`},
		{`"input_seq":1`, `"input_seq":-1`}, {`"input_seq":1`, `"input_seq":1e0`},
		{`"input_seq":1`, `"input_seq":"1"`}, {`"input_seq":1,`, ``},
		{`"match_id":"m",`, ``}, {`"match_id":"m"`, `"match_id":null`}, {`"match_id":"m"`, `"match_id":""`},
		{`"request_id":"a"`, `"request_id":null`}, {`"request_id":"a"`, `"request_id":""`},
		{`"action":{"type":"move","direction":{"x":1,"y":0,"z":0}}`, `"action":null`},
	} {
		bad := strings.Replace(valid, pair[0], pair[1], 1)
		t.Run(bad, func(t *testing.T) {
			if _, err := Decode([]byte(bad)); err == nil {
				t.Fatalf("accepted %s", bad)
			}
		})
	}
	for _, seq := range []string{"1", "9007199254740991"} {
		if _, err := Decode([]byte(strings.Replace(valid, `"input_seq":1`, `"input_seq":`+seq, 1))); err != nil {
			t.Fatal(err)
		}
	}
	start := `{"version":1,"op":"room.start","request_id":"a","payload":{},"error":null}`
	if _, err := Decode([]byte(start)); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`null`, `{"extra":1}`, `[]`} {
		if _, err := Decode([]byte(strings.Replace(start, `"payload":{}`, `"payload":`+payload, 1))); err == nil {
			t.Fatal("invalid start accepted")
		}
	}
}
