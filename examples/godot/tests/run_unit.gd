extends SceneTree

const Support = preload("res://tests/test_support.gd")
var checks: int = 0
var failures: int = 0

func check(condition: bool, message: String) -> void:
	checks += 1
	if not condition:
		failures += 1
		printerr("FAIL: ", message)

func _initialize() -> void:
	test_wire_payloads()
	test_decode_envelope_and_numbers()
	test_message_role_and_identity()
	test_state_fences()
	test_metadata_and_periodic_freshness()
	test_sequence_lifetime()
	test_requests_and_timeouts()
	test_lifecycle_deadlines_and_list_isolation()
	test_heartbeat_and_limits()
	print("unit checks=", checks, " failures=", failures)
	quit(0 if failures == 0 else 1)

func test_wire_payloads() -> void:
	check(WireCodec.valid_room_number("004271"), "leading zero ASCII number accepted")
	for bad: String in ["4271", "0000000", "１２３４５６", " 004271", "00427a"]:
		check(not WireCodec.valid_room_number(bad), "invalid room number rejected")
	check(WireCodec.valid_password("가나다라"), "Unicode codepoint password accepted")
	check(WireCodec.valid_password(" abcd "), "password spaces accepted")
	check(not WireCodec.valid_password("abc"), "short password rejected")
	check(WireCodec.valid_password("가".repeat(64)), "64 codepoint password accepted")
	check(not WireCodec.valid_password("가".repeat(65)), "65 codepoint password rejected")
	for capacity: int in [1, 6]:
		check(WireCodec.valid_create(capacity, "public", ""), "capacity boundary accepted")
	for capacity: int in [0, 7]:
		check(not WireCodec.valid_create(capacity, "public", ""), "invalid capacity rejected")
	check(not WireCodec.valid_create(6, "private", ""), "private missing password rejected")
	check(not WireCodec.valid_create(6, "invalid", "abcd"), "unknown visibility rejected")
	check(WireCodec.create_payload(6, "public", "unused") == {"capacity": 6, "visibility": "public"}, "public password omitted")
	check(WireCodec.create_payload(6, "private", " abcd ") == {"capacity": 6, "visibility": "private", "password": " abcd "}, "private password exact")
	check(WireCodec.join_payload("004271") == {"room_number": "004271"}, "join password omitted")
	check(WireCodec.join_payload("004271", " abcd ") == {"room_number": "004271", "password": " abcd "}, "join password exact")
	var client: Support.FakeClient = Support.ready_client()
	check(client.create_room(6, "private", "").is_empty() and client.sent.is_empty(), "invalid private never falls through to public create")
	check(client.join_room("004271", "abc").is_empty() and client.sent.is_empty(), "invalid join password never sent")
	client.free()

func test_decode_envelope_and_numbers() -> void:
	check(WireCodec.decode_server(Support.event("session.guest", {"session_id": "sA"})).ok, "guest decoded")
	check(WireCodec.decode_server(Support.event("room.join", null, "r1", {"code": "PASSWORD_REQUIRED", "message": "Room password is required."})).ok, "server error decoded")
	for text: String in ["[]", "null", "{", '{"version":1,"op":"room.state"}', '{"version":1,"op":"room.list","request_id":1,"payload":{},"error":null}', '{"version":true,"op":"room.list","request_id":"r1","payload":{},"error":null}', '{"version":1,"op":"room.list","request_id":"r1","payload":{},"error":{}}', '{"version":1,"op":"room.list","request_id":"r1","payload":null,"error":null}', '{"version":1,"op":"room.list","request_id":"r1","payload":{},"error":null,"extra":1}']:
		check(not WireCodec.decode_server(text).ok, "invalid envelope rejected")
	for field: String in ["revision", "snapshot_seq", "server_tick"]:
		for value: Variant in [true, -1, 1.5, 9007199254740992.0]:
			var snapshot: Dictionary = Support.snapshot()
			snapshot[field] = value
			check(not WireCodec.decode_server(Support.event("room.state", snapshot)).ok, "unsafe snapshot numeric rejected")
	for sequence: int in [1, 9007199254740991]:
		check(WireCodec.decode_server(Support.event("game.action", {"match_id": "mA", "input_seq": sequence, "server_tick": 1}, "r1")).ok, "safe input sequence decoded")
	for sequence: Variant in [0, -1, true, 1.5, 9007199254740992.0]:
		check(not WireCodec.decode_server(Support.event("game.action", {"match_id": "mA", "input_seq": sequence, "server_tick": 1}, "r1")).ok, "unsafe input sequence rejected")
	var invalid: Dictionary = Support.snapshot("rA", "mA")
	invalid.game_state.players[0].position.x = 1e39
	check(not WireCodec.decode_server(Support.event("game.snapshot", invalid)).ok, "overflowing Vector3 value rejected")
	invalid.game_state.players[0].position.x = INF
	check(not WireCodec.valid_snapshot(invalid), "nonfinite native snapshot rejected")
	invalid = Support.snapshot()
	invalid.last_processed_input_seq.sA = 1.5
	check(not WireCodec.decode_server(Support.event("room.state", invalid)).ok, "processed sequence checked")

func test_state_fences() -> void:
	var state: RoomState = RoomState.new()
	state.new_connection(1)
	state.accept_guest("sB")
	var epoch: int = state.begin_admission()
	var admission: Dictionary = Support.snapshot("rA", null, 1, 1, ["sA", "sB"])
	check(state.apply_admission(admission, epoch), "matching admission accepted")
	admission.host_id = "mutated"
	check(state.current_snapshot.get("host_id") == "sA", "admission snapshot deep copied")
	check(state.apply_event("game.started", {"snapshot": Support.snapshot("rA", "mA", 2, 2, ["sA", "sB"])}), "new match accepted")
	check(not state.apply_event("game.snapshot", Support.snapshot("rB", "mA", 9, 99)), "foreign room rejected")
	check(not state.apply_event("game.snapshot", Support.snapshot("rA", "mOld", 9, 99)), "foreign match rejected")
	check(state.apply_event("game.ended", {"match_id": "mA", "reason": "finished", "server_tick": 8, "snapshot": Support.snapshot("rA", null, 3, 3, ["sA", "sB"])}), "matching end accepted")
	check(not state.apply_event("game.started", {"snapshot": Support.snapshot("rA", "mA", 2, 2, ["sA", "sB"])}), "ended match never revived")
	state.clear_room()
	check(not state.apply_event("room.state", Support.snapshot("rA", "mA", 99, 99)), "leave fences room event")
	check(not state.apply_admission(Support.snapshot(), epoch), "leave fences old admission")
	state.new_connection(2)
	check(state.session_id.is_empty() and state.current_snapshot.is_empty(), "new connection clears identity and room")

func test_metadata_and_periodic_freshness() -> void:
	var state: RoomState = RoomState.new()
	state.new_connection(1)
	state.accept_guest("sB")
	state.apply_admission(Support.snapshot("rA", "mA", 2, 2, ["sA", "sB"]), state.begin_admission())
	check(state.apply_event("game.snapshot", Support.snapshot("rA", "mA", 4, 50, ["sA", "sB"])), "new periodic snapshot accepted")
	check(not state.apply_event("game.snapshot", Support.snapshot("rA", "mA", 4, 50, ["sA", "sB"])), "duplicate periodic rejected")
	check(state.apply_event("room.state", Support.snapshot("rA", "mA", 5, 49, ["sB"])), "host metadata independent from periodic capture filter")
	check(state.current_snapshot.get("host_id") == "sB", "host removal reflected")
	check(not state.apply_event("room.state", Support.snapshot("rA", "mA", 3, 55, ["sA", "sB"])), "stale metadata rejected")
	check(state.apply_event("room.state", Support.snapshot("rA", null, 6, 51, ["sB"])), "waiting transition retained")
	check(not state.apply_event("game.snapshot", Support.snapshot("rA", "mA", 99, 100, ["sB"])), "old playing cannot revive waiting state")

func test_sequence_lifetime() -> void:
	var client: Support.FakeClient = Support.ready_client(true)
	client.send_error = ERR_BUSY
	check(client.send_direction(Vector3.FORWARD).is_empty() and client.next_input_seq == 1, "unsent input never consumes sequence")
	client.send_error = OK
	var first: String = client.send_direction(Vector3.FORWARD)
	check(not first.is_empty() and client.next_input_seq == 2, "successful send consumes sequence")
	client._accept_text(Support.event("game.action", null, first, {"code": "SERVER_BUSY", "message": "busy"}), 1, 10)
	check(client.next_input_seq == 2, "server rejection never rewinds sent sequence")
	client.state.clear_room()
	client.state.apply_admission(Support.snapshot("rB", "mB"), client.state.begin_admission())
	client.send_direction(Vector3.ZERO)
	check(client.sent.size() == 2, "new room sends input")
	if client.sent.size() == 2:
		check(JSON.parse_string(client.sent[0]).payload.input_seq == 1 and JSON.parse_string(client.sent[1]).payload.input_seq == 2, "input sequence spans membership")
	check(client.send_direction(Vector3(INF, 0, 0)).is_empty(), "nonfinite input not sent")
	client.next_input_seq = WireCodec.MAX_SAFE_INTEGER + 1
	check(client.send_direction(Vector3.ZERO).is_empty(), "exhausted safe sequence not sent")
	client.disconnect_guest()
	check(client.next_input_seq == 1 and client.state.session_id.is_empty(), "disconnect resets sequence and identity")
	client.free()

func test_requests_and_timeouts() -> void:
	var client: Support.FakeClient = Support.ready_client()
	var failures_seen: Array = []
	client.request_failed.connect(func(op: String, code: String, message: String) -> void: failures_seen.append([op, code, message]))
	var id: String = client.create_room(6, "private", " abcd ")
	check(not id.is_empty(), "valid private create sent")
	client._accept_text(Support.event("room.join", {"snapshot": Support.snapshot()}, id), 1, 1)
	check(client.state.current_snapshot.is_empty(), "wrong op cannot satisfy request")
	client._accept_text(Support.event("room.create", {"snapshot": Support.snapshot()}, id), 0, 1)
	check(client.state.current_snapshot.is_empty(), "old generation callback ignored")
	client._expire_requests(10001)
	client._accept_text(Support.event("room.create", {"snapshot": Support.snapshot()}, id), 1, 10002)
	check(client.state.current_snapshot.is_empty() and client.state.session_id.is_empty(), "timed out create disconnects and ignores late success")
	check(not failures_seen.is_empty() and failures_seen[0][1] == "CLIENT_TIMEOUT", "timeout has local error code")
	client.free()
	client = Support.ready_client()
	id = client.create_room(6, "public")
	client._accept_text(Support.event("room.create", {"snapshot": Support.snapshot()}, id), 1, 1)
	check(not client.state.current_snapshot.is_empty(), "correlated create admits")
	var leave_id: String = client.leave_room()
	check(not leave_id.is_empty() and client.state.current_snapshot.is_empty(), "leave fences immediately after send")
	client._accept_text(Support.event("room.state", Support.snapshot()), 1, 2)
	check(client.state.current_snapshot.is_empty(), "post leave event never revives")
	client._accept_text(Support.event("room.create", {"snapshot": Support.snapshot()}, id), 1, 3)
	check(client.state.current_snapshot.is_empty(), "duplicate RPC success ignored")
	client.free()

func test_heartbeat_and_limits() -> void:
	var client: Support.FakeClient = Support.ready_client()
	var failure_codes: Array[String] = []
	client.request_failed.connect(func(_op: String, code: String, _message: String) -> void: failure_codes.append(code))
	for index: int in 64:
		check(not client.list_rooms().is_empty(), "pending request under limit accepted")
	check(client.list_rooms().is_empty() and not failure_codes.is_empty() and failure_codes.back() == "CLIENT_BUSY", "65th pending rejected")
	client._expire_requests(10001)
	check(not client.list_rooms().is_empty(), "expired list releases pending without disconnect")
	client.free()
	client = Support.ready_client()
	client.clock_ms = 29999
	client._process(0)
	check(client.sent.is_empty(), "heartbeat waits interval")
	client.clock_ms = 30000
	client._process(0)
	check(client.sent.size() == 1, "heartbeat sent at interval")
	if client.sent.size() == 1:
		var heartbeat: Dictionary = JSON.parse_string(client.sent[0])
		check(heartbeat.op == "room.list" and heartbeat.payload == {}, "heartbeat is valid room list")
	client.clock_ms = 30001
	client._process(0)
	check(client.sent.size() == 1, "heartbeat no frame spam")
	client.free()

func test_lifecycle_deadlines_and_list_isolation() -> void:
	for operation: String in ["room.join", "room.start", "room.leave"]:
		var client: Support.FakeClient = Support.ready_client()
		var id: String = ""
		if operation == "room.join":
			id = client.join_room("004271")
		else:
			client.state.apply_admission(Support.snapshot(), client.state.begin_admission())
			id = client.start_room() if operation == "room.start" else client.leave_room()
		check(not id.is_empty(), "lifecycle request sent")
		client._expire_requests(10000)
		check(client.state.session_id.is_empty() and client.state.current_snapshot.is_empty(), "lifecycle timeout disconnects")
		client.free()
	var client: Support.FakeClient = Support.ready_client(true)
	var before: Dictionary = client.state.current_snapshot.duplicate(true)
	var rooms: Array = []
	client.rooms_received.connect(func(value: Array) -> void: rooms.append_array(value))
	var id: String = client.list_rooms()
	client._accept_text(Support.event("room.list", {"rooms": [Support.snapshot("foreign")]}, id), 1, 1)
	check(rooms.size() == 1 and client.state.current_snapshot == before, "list results never become current membership")
	id = client.list_rooms()
	client._expire_requests(10001)
	client._accept_text(Support.event("room.list", {"rooms": []}, id), 1, 10002)
	check(not client.state.session_id.is_empty() and rooms.size() == 1, "late list ignored without disconnect")
	client.free()

func test_message_role_and_identity() -> void:
	check(not WireCodec.decode_server(Support.event("room.create", {"snapshot": Support.snapshot()})).ok, "admission must be correlated RPC response")
	check(not WireCodec.decode_server(Support.event("room.join", {"snapshot": Support.snapshot()})).ok, "join cannot be unsolicited event")
	check(not WireCodec.decode_server(Support.event("session.guest", {"session_id": "sA"}, "rpc")).ok, "guest must be event")
	check(not WireCodec.decode_server(Support.event("room.state", Support.snapshot(), "rpc")).ok, "room state must be event")
	check(not WireCodec.decode_server(Support.event("room.list", {}, "r1", {"code": "ERROR", "message": "invalid"})).ok, "error cannot include success payload")
	var malformed: Dictionary = Support.snapshot("rA", "mA")
	malformed.game_state.players[0].session_id = "outsider"
	check(not WireCodec.decode_server(Support.event("game.snapshot", malformed)).ok, "unknown player cannot enter authoritative snapshot")
	malformed = Support.snapshot()
	malformed.members.append({"session_id": "sA"})
	check(not WireCodec.decode_server(Support.event("room.state", malformed)).ok, "duplicate member rejected")
