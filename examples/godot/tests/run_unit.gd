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
	_run.call_deferred()

func _run() -> void:
	test_wire_payloads()
	test_decode_envelope_and_numbers()
	test_message_role_and_identity()
	test_state_fences()
	test_metadata_and_periodic_freshness()
	test_sequence_lifetime()
	test_requests_and_timeouts()
	test_lifecycle_deadlines_and_list_isolation()
	test_heartbeat_and_limits()
	await test_lobby_behaviors()
	await test_lobby_pre_ready_rooms()
	test_snapshot_buffer()
	await test_movement_keys()
	await test_direction_retry()
	await test_world_lifecycle()
	test_same_capture()
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
	check(WireCodec.decode_server(Support.event("session.guest", {"session_id": "sA", "game_id": "default"})).ok, "guest decoded")
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
	state.accept_guest("sB", "default")
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
	state.accept_guest("sB", "default")
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
	check(not WireCodec.decode_server(Support.event("session.guest", {"session_id": "sA", "game_id": "default"}, "rpc")).ok, "guest must be event")
	check(not WireCodec.decode_server(Support.event("room.state", Support.snapshot(), "rpc")).ok, "room state must be event")
	check(not WireCodec.decode_server(Support.event("room.list", {}, "r1", {"code": "ERROR", "message": "invalid"})).ok, "error cannot include success payload")
	var malformed: Dictionary = Support.snapshot("rA", "mA")
	malformed.game_state.players[0].session_id = "outsider"
	check(not WireCodec.decode_server(Support.event("game.snapshot", malformed)).ok, "unknown player cannot enter authoritative snapshot")
	malformed = Support.snapshot()
	malformed.members.append({"session_id": "sA"})
	check(not WireCodec.decode_server(Support.event("room.state", malformed)).ok, "duplicate member rejected")

func test_lobby_behaviors() -> void:
	var lobby_script: Variant = load("res://scripts/lobby.gd")
	check(lobby_script != null, "native lobby implementation exists")
	if lobby_script == null:
		return
	var client: Support.FakeClient = Support.ready_client()
	client.status = "guest"
	var lobby: Variant = lobby_script.new()
	# Bind before ready and bind again: neither duplicates signal-driven actions.
	lobby.bind_client(client)
	root.add_child(lobby)
	await process_frame
	lobby.bind_client(client)
	check(lobby._connection_label.size.x > lobby._connection_label.size.y, "connection status remains a horizontal Korean label")
	check(lobby.capacity_select.item_count == 6, "capacity covers 1 through 6")
	for index: int in 6:
		check(lobby.capacity_select.get_item_id(index) == index + 1, "capacity sends literal size")
	check(not lobby.create_button.disabled and not lobby.join_button.disabled and lobby.start_button.disabled, "guest can enter but cannot start")
	for bad: String in ["12345", "1234567", "１２３４５６"]:
		lobby.room_number_edit.text = bad
		lobby._join_clicked()
	check(client.sent.is_empty(), "invalid native room numbers never sent")
	lobby.room_number_edit.text = "004271"
	lobby.join_button.pressed.emit()
	check(client.sent.size() == 1, "repeated bind sends one join on click")
	lobby.bind_client(client)
	check(lobby.join_button.disabled, "rebinding same client preserves pending admission gate")
	var request: Dictionary = JSON.parse_string(client.sent.back())
	check(request.payload == {"room_number": "004271"}, "first join preserves zeros and omits password")
	client._accept_text(Support.event("room.join", null, request.request_id, {"code": "PASSWORD_REQUIRED", "message": "Room password is required."}), 1, 1)
	check(lobby.join_password_edit.visible and lobby.join_password_edit.secret, "server challenge reveals masked join field")
	check(lobby.status_label.text.contains("Room password is required.") and lobby.status_label.text.contains("비밀번호"), "challenge preserves original error and Korean guidance")
	lobby.join_password_edit.text = " abcd "
	lobby.join_button.pressed.emit()
	request = JSON.parse_string(client.sent.back())
	check(request.payload.get("password", "") == " abcd ", "join sends exact password spaces")
	client._accept_text(Support.event("room.join", null, request.request_id, {"code": "PASSWORD_INVALID", "message": "Room password is invalid."}), 1, 2)
	check(client.sent.size() == 2 and lobby.status_label.text.contains("Room password is invalid."), "wrong password shows error without auto retry")
	lobby.create_password_edit.text = "secret"
	lobby._handle_snapshot(Support.snapshot())
	check(lobby.join_password_edit.text.is_empty() and lobby.create_password_edit.text.is_empty(), "successful admission clears both secrets")
	check(not lobby.start_button.disabled and lobby.create_button.disabled and lobby.join_button.disabled and not lobby.leave_button.disabled, "waiting host gates admission and enables start/leave")
	lobby._handle_snapshot(Support.snapshot("rA", null, 2, 2, ["sB", "sA"]))
	check(lobby.start_button.disabled, "waiting member cannot start")
	lobby._handle_snapshot(Support.snapshot("rA", "mA", 3, 3))
	check(lobby.start_button.disabled and not lobby.leave_button.disabled, "playing gates start but permits leave")
	client.state.session_id = "same-prefix-self"
	lobby._handle_snapshot(Support.snapshot("rA", null, 4, 4, ["same-prefix-host", "same-prefix-self"]))
	check(lobby.start_button.disabled, "full IDs distinguish host from self")
	check(lobby.members_label.text.count("(나)") == 1 and lobby.members_label.text.count("(방장)") == 1, "only matching full identity gets self or host marker")
	lobby.join_password_edit.text = "secret"
	lobby._handle_connection("disconnected")
	check(lobby.join_password_edit.text.is_empty() and not lobby.join_password_edit.visible, "disconnect clears secret and challenge")
	for status: String in ["connecting", "closing", "disconnected"]:
		lobby._handle_connection(status)
		check(lobby.create_button.disabled and lobby.join_button.disabled and lobby.refresh_button.disabled and lobby.start_button.disabled, "unready state disables room actions")
	lobby.queue_free()
	client.free()
	await process_frame
	client = Support.ready_client()
	client.status = "guest"
	lobby = lobby_script.new()
	root.add_child(lobby)
	await process_frame
	lobby.bind_client(client)
	lobby.visibility_select.select(1)
	lobby.visibility_select.item_selected.emit(1)
	check(lobby.create_password_edit.visible and lobby.create_password_edit.secret, "private create shows secret field")
	lobby.capacity_select.select(0)
	lobby.create_password_edit.text = " 가나ab "
	lobby.create_button.pressed.emit()
	request = JSON.parse_string(client.sent.back())
	check(request.payload.get("capacity") == 1 and request.payload.get("visibility") == "private" and request.payload.get("password") == " 가나ab ", "private create keeps exact Unicode password and capacity")
	client._accept_text(Support.event("room.create", null, request.request_id, {"code": "SERVER_BUSY", "message": "busy"}), 1, 1)
	lobby.visibility_select.select(0)
	lobby.visibility_select.item_selected.emit(0)
	check(lobby.create_password_edit.text.is_empty() and not lobby.create_password_edit.visible, "public switch clears hidden private secret")
	lobby.create_button.pressed.emit()
	request = JSON.parse_string(client.sent.back())
	check(request.payload.get("capacity") == 1 and request.payload.get("visibility") == "public" and not request.payload.has("password"), "public create omits password")
	client._accept_text(Support.event("room.create", {"snapshot": Support.snapshot()}, request.request_id), 1, 2)
	check(lobby.room_label.text.contains("004271"), "successful create displays leading-zero room number")
	lobby.create_password_edit.text = "secret"
	lobby.leave_button.pressed.emit()
	check(lobby.create_password_edit.text.is_empty() and lobby.start_button.disabled, "leave clears secret and membership controls")
	lobby.queue_free()
	client.free()
	await process_frame

func test_lobby_pre_ready_rooms() -> void:
	var client: Support.FakeClient = Support.ready_client()
	client.status = "guest"
	var lobby := LobbyPanel.new()
	lobby.bind_client(client)
	# Events can arrive after binding but before widgets exist.
	client.rooms_received.emit([])
	client.rooms_received.emit([Support.snapshot()])
	root.add_child(lobby)
	await process_frame
	check(client.sent.size() == 1, "pre-ready room events recover with one fresh list request")
	if client.sent.size() == 1:
		var request: Dictionary = JSON.parse_string(client.sent[0])
		check(request.op == "room.list" and request.payload == {}, "recovery list request uses valid empty payload")
		client._accept_text(Support.event("room.list", {"rooms": [Support.snapshot()]}, request.request_id), 1, 1)
	check(lobby.rooms_list.item_count == 1, "fresh rooms render after pre-ready event")
	if lobby.rooms_list.item_count == 1:
		lobby.rooms_list.item_selected.emit(0)
		check(lobby.room_number_edit.text == "004271", "recovered list selection preserves leading zeros")
	lobby.bind_client(client)
	check(client.sent.size() == 1, "rebind does not repeat deferred list refresh")
	lobby.queue_free()
	client.free()
	await process_frame
	client = Support.ready_client()
	client.status = "guest"
	lobby = LobbyPanel.new()
	root.add_child(lobby)
	await process_frame
	lobby.bind_client(client)
	client.rooms_received.emit([Support.snapshot()])
	check(lobby.rooms_list.item_count == 1, "ready-before-bind list event renders normally")
	lobby.queue_free()
	client.free()
	await process_frame

func test_snapshot_buffer() -> void:
	var buffer := SnapshotBuffer.new()
	var a := Support.snapshot("rA", "mA", 2, 2, ["sA", "sB"])
	check(buffer.push_snapshot(a, 1000), "buffer accepts first authoritative sample")
	check(buffer.sample_positions(1000).get("sA") == Vector3.ZERO, "first position available immediately")
	var b: Dictionary = a.duplicate(true)
	b.snapshot_seq = 3
	b.game_state.players[0].position.x = 10
	check(buffer.push_snapshot(b, 1100), "buffer accepts advancing sample")
	check(buffer.sample_positions(1150).get("sA") == Vector3(5, 0, 0), "receive-clock interpolation midpoint")
	check(buffer.sample_positions(2000).get("sA") == Vector3(10, 0, 0), "buffer never extrapolates")
	check(not buffer.push_snapshot(a, 2001), "stale capture rejected")
	check(not buffer.push_snapshot(b, 2002), "duplicate capture rejected")
	var removed := Support.snapshot("rA", "mA", 3, 4, ["sA"])
	check(buffer.push_snapshot(removed, 3000), "long-gap snapshot accepted")
	check(not buffer.sample_positions(3000).has("sB"), "removed player cannot resurrect")
	check(buffer.sample_positions(3000).get("sA") == Vector3.ZERO, "gap over 500ms snaps immediately")
	check(buffer.duration_ms <= 200, "duration bounded at 200ms")
	removed.snapshot_seq = 5
	removed.game_state.players[0].position.x = 20
	buffer.push_snapshot(removed, 3001)
	check(buffer.duration_ms == 16, "burst minimum duration 16ms")
	removed.snapshot_seq = 6
	buffer.push_snapshot(removed, 3401)
	check(buffer.duration_ms == 200, "400ms receive gap capped at 200ms")
	check(buffer.push_snapshot(Support.snapshot("rB", "mB", 1, 1), 3402), "identity change resets capture sequence")
	check(buffer.sample_positions(3402).get("sA") == Vector3.ZERO, "new identity snaps without old timeline")
	buffer.clear()
	check(buffer.sample_positions(4000).is_empty(), "clear drops timeline")

func inject_key(code: Key, pressed: bool) -> void:
	var event := InputEventKey.new()
	event.physical_keycode = code
	event.pressed = pressed
	Input.parse_input_event(event)

func test_movement_keys() -> void:
	MovementInput.ensure_actions()
	var movement := MovementInput.new()
	inject_key(KEY_W, true)
	await process_frame
	check(movement.sample(true, false) == Vector3.FORWARD, "physical W maps forward")
	check(movement.sample(false, false) == Vector3.ZERO, "window focus blocks sampling")
	check(movement.sample(true, true) == Vector3.ZERO, "text focus blocks sampling")
	inject_key(KEY_D, true)
	await process_frame
	check(movement.sample(true, false).is_equal_approx(Vector3(1, 0, -1).normalized()), "diagonal normalized")
	movement.release_actions()
	check(movement.sample(true, false) == Vector3.ZERO, "release clears held movement")
	inject_key(KEY_W, false)
	inject_key(KEY_D, false)
	inject_key(KEY_UP, true)
	inject_key(KEY_E, true)
	await process_frame
	check(movement.sample(true, false).is_equal_approx(Vector3(0, 1, -1).normalized()), "arrow and E map physical axes")
	check(movement.sample(true, false, false) == Vector3.FORWARD, "y disabled preserves XZ direction")
	movement.release_actions()
	inject_key(KEY_UP, false)
	inject_key(KEY_E, false)
	inject_key(KEY_Q, true)
	await process_frame
	check(movement.sample(true, false) == Vector3.DOWN, "Q maps negative y")
	movement.release_actions()
	inject_key(KEY_Q, false)
	await process_frame

func test_direction_retry() -> void:
	var client := Support.FailOnceClient.new()
	client.state.new_connection(1)
	client.state.accept_guest("sA", "default")
	client.state.apply_admission(Support.snapshot("rA", "mA"), client.state.begin_admission())
	var controller := InputController.new()
	root.add_child(controller)
	controller.set_process(false)
	controller.bind_client(client)
	controller.submit_direction(Vector3.FORWARD)
	check(client.sent.is_empty(), "rejected direction remains unsent")
	controller.submit_direction(Vector3.FORWARD)
	check(client.sent.size() == 1, "same direction retries after send failure")
	controller.submit_direction(Vector3.FORWARD)
	check(client.sent.size() == 1, "successful direction deduplicates")
	controller.set_input_gate(false, false)
	check(client.sent.size() == 2, "focus loss sends stop")
	if client.sent.size() >= 2:
		check(JSON.parse_string(client.sent.back()).payload.action.direction == {"x": 0.0, "y": 0.0, "z": 0.0}, "focus gate sends zero")
	controller.set_input_gate(true, false)
	client.state.clear_room()
	client.snapshot_changed.emit({})
	client.state.apply_admission(Support.snapshot("rB", "mB"), client.state.begin_admission())
	client.snapshot_changed.emit(client.state.current_snapshot)
	controller.submit_direction(Vector3.FORWARD)
	check(client.sent.size() == 3, "new match cannot dedup first direction against old match")
	client.send_error = ERR_BUSY
	# The subclass only fails once, so use pending saturation for stop retry.
	client.max_pending = client._pending.size()
	controller.set_input_gate(false, false)
	check(controller._last_sent_direction == Vector3.FORWARD, "failed stop never updates successful direction")
	client.max_pending = 64
	controller.set_input_gate(true, false)
	controller.submit_direction(Vector3.RIGHT)
	check(client.sent.size() >= 4 and JSON.parse_string(client.sent.back()).payload.action.direction == {"x": 0.0, "y": 0.0, "z": 0.0}, "stop retry precedes newly requested movement")
	controller.queue_free()
	client.free()
	await process_frame

func test_world_lifecycle() -> void:
	var client := Support.ready_client(true)
	var world := GameWorld.new()
	root.add_child(world)
	world.bind_client(client)
	await process_frame
	check(world.avatars.has("sA"), "new self avatar visible from authoritative sample")
	var snapshot := Support.snapshot("rA", "mA", 2, 2, ["sA", "sB"])
	client.snapshot_changed.emit(snapshot)
	check(world.avatars.has("sB"), "new peer appears immediately")
	if world.avatars.has("sA"):
		var before: Vector3 = world.avatars.sA.position
		client.input_acknowledged.emit({"match_id": "mA", "input_seq": 5, "server_tick": 9})
		check(world.avatars.sA.position == before, "ACK never writes avatar position")
	# Metadata can advance while the capture sequence remains unchanged.
	var metadata := Support.snapshot("rA", "mA", 3, 2, ["sA"])
	client.snapshot_changed.emit(metadata)
	check(not world.avatars.has("sB"), "metadata removal takes effect in same frame")
	client.snapshot_changed.emit(Support.snapshot("rA", null, 4, 3))
	check(world.avatars.is_empty() and world.buffer.sample_positions(9999).is_empty(), "WAITING clears world and interpolation")
	client.state.clear_room()
	client.state.apply_admission(Support.snapshot("rEnded", "mEnded", 1, 1), client.state.begin_admission())
	client.snapshot_changed.emit(client.state.current_snapshot)
	var ended := Support.snapshot("rEnded", null, 2, 2)
	check(client.state.apply_event("game.ended", {"match_id": "mEnded", "snapshot": ended}), "synthetic game.ended returns state to WAITING")
	client.snapshot_changed.emit(client.state.current_snapshot)
	check(world.avatars.is_empty() and world.buffer.sample_positions(9999).is_empty(), "synthetic game.ended clears renderer timeline")
	client.snapshot_changed.emit({})
	world.queue_free()
	client.free()
	await process_frame

func test_same_capture() -> void:
	var a := Support.snapshot("rA", "mA")
	var b: Dictionary = a.duplicate(true)
	check(TestDriver.same_capture([a, b]), "equal full capture agrees")
	b.game_state.players[0].position.x = 1
	check(not TestDriver.same_capture([a, b]), "matching capture identity with different position fails")
	b = a.duplicate(true)
	b.snapshot_seq += 1
	check(not TestDriver.same_capture([a, b]), "different capture identity cannot pass")
