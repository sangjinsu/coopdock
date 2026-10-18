extends RefCounted

class FakeClient extends CoopClient:
	var sent: Array[String] = []
	var clock_ms: int = 0
	var send_error: Error = OK
	var opened: bool = true

	func _socket_open() -> bool:
		return opened

	func _send_text(text: String) -> Error:
		if send_error == OK:
			sent.append(text)
		return send_error

	func _now_msec() -> int:
		return clock_ms

static func snapshot(room_id: String = "rA", match_id: Variant = null,
		revision: int = 1, seq: int = 1, ids: Array = ["sA"]) -> Dictionary:
	var members: Array = []
	var players: Array = []
	var processed: Dictionary = {}
	for index: int in ids.size():
		var id: String = ids[index]
		members.append({"session_id": id})
		processed[id] = 0
		players.append({"session_id": id, "position": {"x": index * 2, "y": 0, "z": 0},
			"velocity": {"x": 0, "y": 0, "z": 0}})
	return {"game_id": "default", "room_id": room_id, "room_number": "004271", "visibility": "public",
		"capacity": 6, "match_id": match_id,
		"phase": "WAITING" if match_id == null else "PLAYING", "host_id": ids[0],
		"members": members, "revision": revision,
		"game_state": null if match_id == null else {"players": players},
		"server_tick": 0, "snapshot_seq": seq, "last_processed_input_seq": processed}

static func event(op: String, payload: Variant, id: Variant = null, error: Variant = null) -> String:
	return JSON.stringify({"version": 1, "op": op, "request_id": id,
		"payload": payload, "error": error})

static func ready_client(playing: bool = false) -> FakeClient:
	var client: FakeClient = FakeClient.new()
	client.state.new_connection(1)
	client.state.accept_guest("sA", "default")
	if playing:
		var epoch: int = client.state.begin_admission()
		client.state.apply_admission(snapshot("rA", "mA"), epoch)
	return client

class FailOnceClient extends FakeClient:
	var fail_next: bool = true
	func _send_text(text: String) -> Error:
		if fail_next:
			fail_next = false
			return ERR_BUSY
		sent.append(text)
		return OK
