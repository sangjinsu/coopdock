extends Node
class_name CoopClient

signal connection_changed(status: String)
signal snapshot_changed(snapshot: Dictionary)
signal rooms_received(rooms: Array)
signal request_failed(op: String, code: String, message: String)
signal input_acknowledged(payload: Dictionary)
signal request_completed(op: String, request_id: String, payload: Dictionary, error: Dictionary)

var state: RoomState = RoomState.new()
var next_input_seq: int = 1
var heartbeat_interval_ms: int = 30000
var request_timeout_ms: int = 10000
var max_pending: int = 64
var status: String = "disconnected"
var _expected_game_id: String = ""
var _peer: WebSocketPeer
var _pending: Dictionary = {}
var _request_counter: int = 0
var _last_heartbeat_ms: int = 0
var _connecting_since_ms: int = 0

func _set_status(value: String) -> void:
	if status != value:
		status = value
		connection_changed.emit(status)

func connect_guest(base_url: String, game_id: String = "default") -> Error:
	if not WireCodec.valid_game_id(game_id) or not _valid_base_url(base_url):
		return ERR_INVALID_PARAMETER
	disconnect_guest()
	_expected_game_id = game_id
	if _peer != null:
		_peer.close(-1)
	_peer = WebSocketPeer.new()
	_peer.inbound_buffer_size = 1048576
	_peer.outbound_buffer_size = 1048576
	_peer.max_queued_packets = 256
	var result: Error = _peer.connect_to_url(base_url + "?game_id=" + game_id)
	if result != OK:
		_expected_game_id = ""
		_peer = null
		return result
	_connecting_since_ms = _now_msec()
	_set_status("connecting")
	return OK

func _valid_base_url(url: String) -> bool:
	if url.contains("?") or url.contains("#") or url != url.strip_edges():
		return false
	var scheme_length: int = 5 if url.begins_with("ws://") else (6 if url.begins_with("wss://") else 0)
	if scheme_length == 0:
		return false
	var authority: String = url.substr(scheme_length).get_slice("/", 0)
	return not authority.is_empty() and not authority.contains(" ") and not authority.contains("\t") and not authority.contains("\n")

func disconnect_guest() -> void:
	_expected_game_id = ""
	if _peer != null and _peer.get_ready_state() != WebSocketPeer.STATE_CLOSED:
		_peer.close(1000, "Client disconnect")
	_pending.clear()
	state.new_connection(state.connection_generation + 1)
	next_input_seq = 1
	_request_counter = 0
	_last_heartbeat_ms = _now_msec()
	snapshot_changed.emit({})
	_set_status("disconnected")

func _exit_tree() -> void:
	disconnect_guest()
	if _peer != null:
		_peer.close(-1)

func _invalid(op: String, message: String = "Invalid local input.") -> String:
	request_failed.emit(op, "CLIENT_INVALID_INPUT", message)
	return ""

func _lifecycle_busy() -> bool:
	for pending: Dictionary in _pending.values():
		if pending.op in ["room.create", "room.join", "room.start", "room.leave"]:
			return true
	return false

func create_room(capacity: int, visibility: String, password: String = "") -> String:
	if not WireCodec.valid_create(capacity, visibility, password):
		return _invalid("room.create")
	if not state.current_snapshot.is_empty() or _lifecycle_busy():
		return _invalid("room.create", "Room operation is already active.")
	state.begin_admission()
	return _request("room.create", WireCodec.create_payload(capacity, visibility, password))

func join_room(number: String, password: Variant = null) -> String:
	if not WireCodec.valid_room_number(number) or (password != null and (not password is String or not WireCodec.valid_password(password))):
		return _invalid("room.join")
	if not state.current_snapshot.is_empty() or _lifecycle_busy():
		return _invalid("room.join", "Room operation is already active.")
	state.begin_admission()
	return _request("room.join", WireCodec.join_payload(number, password))

func list_rooms() -> String:
	return _request("room.list", {})

func leave_room() -> String:
	if state.current_snapshot.is_empty() or _lifecycle_busy():
		return _invalid("room.leave", "No available room membership.")
	var id: String = _request("room.leave", {})
	if not id.is_empty():
		state.clear_room()
		_emit_snapshot()
	return id

func start_room() -> String:
	if state.current_snapshot.get("phase") != "WAITING" or _lifecycle_busy():
		return _invalid("room.start", "Room is not waiting.")
	return _request("room.start", {})

func send_direction(direction: Vector3) -> String:
	if state.current_snapshot.get("phase") != "PLAYING":
		return ""
	if not direction.is_finite() or absf(direction.x) > 1.0 or absf(direction.y) > 1.0 or absf(direction.z) > 1.0 or not WireCodec.safe_integer(next_input_seq, 1):
		return _invalid("game.action")
	var payload: Dictionary = {"match_id": state.current_snapshot.match_id,
		"input_seq": next_input_seq, "action": {"type": "move",
		"direction": {"x": direction.x, "y": direction.y, "z": direction.z}}}
	var id: String = _request("game.action", payload)
	if not id.is_empty():
		next_input_seq += 1
	return id

func _request(op: String, payload: Dictionary) -> String:
	if not _socket_open() or state.session_id.is_empty():
		return _invalid(op, "Guest connection is not ready.")
	if _pending.size() >= max_pending:
		request_failed.emit(op, "CLIENT_BUSY", "Too many pending requests.")
		return ""
	_request_counter += 1
	var id: String = "c%d-r%d" % [state.connection_generation, _request_counter]
	# Never retain request payloads or passwords in correlation records.
	_pending[id] = {"op": op, "epoch": state.operation_epoch,
		"generation": state.connection_generation, "deadline": _now_msec() + request_timeout_ms}
	var result: Error = _send_text(WireCodec.encode_request(op, id, payload))
	if result != OK:
		_pending.erase(id)
		request_failed.emit(op, "CLIENT_SEND_FAILED", "WebSocket send failed (%d)." % result)
		return ""
	return id

func _socket_open() -> bool:
	return _peer != null and _peer.get_ready_state() == WebSocketPeer.STATE_OPEN

func _send_text(text: String) -> Error:
	return _peer.send_text(text) if _peer != null else ERR_UNAVAILABLE

func _now_msec() -> int:
	return Time.get_ticks_msec()

func _emit_snapshot() -> void:
	var phase: String = state.current_snapshot.get("phase", "")
	_set_status("playing" if phase == "PLAYING" else ("waiting" if phase == "WAITING" else "guest"))
	snapshot_changed.emit(state.current_snapshot.duplicate(true))

func _accept_text(text: String, generation: int, received_ms: int) -> void:
	if generation != state.connection_generation:
		return
	var decoded: Dictionary = WireCodec.decode_server(text)
	if not decoded.ok:
		request_failed.emit("", "CLIENT_PROTOCOL", decoded.error)
		disconnect_guest()
		return
	var message: Dictionary = decoded.message
	var op: String = message.op
	if message.error == null and not _payload_matches_game(op, message.payload):
		request_failed.emit(op, "CLIENT_PROTOCOL", "Server game binding does not match the connection.")
		disconnect_guest()
		return
	if message.request_id == null:
		if message.error != null:
			request_failed.emit(op, message.error.code, message.error.message)
		elif op == "session.guest":
			if not state.session_id.is_empty() or status == "disconnected":
				return
			state.accept_guest(message.payload.session_id, message.payload.game_id)
			_last_heartbeat_ms = received_ms
			_set_status("guest")
		elif state.apply_event(op, message.payload):
			_emit_snapshot()
		return
	var id: String = message.request_id
	if not _pending.has(id):
		return
	var pending: Dictionary = _pending[id]
	if pending.generation != generation or pending.op != op:
		return
	if received_ms >= int(pending.deadline):
		_expire_requests(received_ms)
		return
	_pending.erase(id)
	var error: Dictionary = message.error if message.error != null else {}
	var payload: Dictionary = message.payload if message.payload != null else {}
	if not error.is_empty():
		request_failed.emit(op, error.code, error.message)
	else:
		match op:
			"room.create", "room.join":
				if state.apply_admission(payload.snapshot, int(pending.epoch)):
					_emit_snapshot()
			"room.start":
				if pending.epoch == state.operation_epoch and state.apply_event("game.started", payload):
					_emit_snapshot()
			"room.list":
				rooms_received.emit(payload.rooms.duplicate(true))
			"game.action":
				if payload.match_id == state.current_snapshot.get("match_id"):
					input_acknowledged.emit(payload.duplicate(true))
	request_completed.emit(op, id, payload, error)

func _payload_matches_game(op: String, payload: Dictionary) -> bool:
	var expected: String = _expected_game_id if not _expected_game_id.is_empty() else state.game_id
	match op:
		"session.guest":
			return payload.game_id == expected
		"room.create", "room.join", "room.start", "game.started", "game.ended":
			return payload.snapshot.game_id == expected
		"room.state", "game.snapshot":
			return payload.game_id == expected
		"room.list":
			for room: Dictionary in payload.rooms:
				if room.game_id != expected:
					return false
	return true

func _expire_requests(now_ms: int) -> void:
	var lifecycle_expired: bool = false
	for id: String in _pending.keys():
		var pending: Dictionary = _pending[id]
		if now_ms < int(pending.deadline):
			continue
		_pending.erase(id)
		request_failed.emit(pending.op, "CLIENT_TIMEOUT", "Local request deadline exceeded.")
		if pending.op in ["room.create", "room.join", "room.start", "room.leave"]:
			lifecycle_expired = true
	if lifecycle_expired:
		disconnect_guest()

func _process(_delta: float) -> void:
	var now_ms: int = _now_msec()
	if _peer != null:
		# Poll closing peers too, so the close handshake can finish.
		_peer.poll()
		var ready_state: int = _peer.get_ready_state()
		if ready_state == WebSocketPeer.STATE_OPEN:
			var generation: int = state.connection_generation
			for index: int in mini(64, _peer.get_available_packet_count()):
				var packet: PackedByteArray = _peer.get_packet()
				if not _peer.was_string_packet() or packet.size() > WireCodec.MAX_MESSAGE_BYTES:
					request_failed.emit("", "CLIENT_PROTOCOL", "Invalid WebSocket packet.")
					disconnect_guest()
					break
				_accept_text(packet.get_string_from_utf8(), generation, now_ms)
				if generation != state.connection_generation:
					break
		elif ready_state == WebSocketPeer.STATE_CLOSED and status != "disconnected":
			disconnect_guest()
		if status == "connecting" and now_ms - _connecting_since_ms >= request_timeout_ms:
			request_failed.emit("session.guest", "CLIENT_TIMEOUT", "Guest connection deadline exceeded.")
			disconnect_guest()
	_expire_requests(now_ms)
	if _socket_open() and not state.session_id.is_empty() and now_ms - _last_heartbeat_ms >= heartbeat_interval_ms:
		_last_heartbeat_ms = now_ms
		list_rooms()
