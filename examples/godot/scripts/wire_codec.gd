extends RefCounted
class_name WireCodec

const MAX_SAFE_INTEGER: int = 9007199254740991
const MAX_MESSAGE_BYTES: int = 1048576

static func valid_game_id(game_id: String) -> bool:
	if game_id.is_empty() or game_id.length() > 64:
		return false
	for index: int in game_id.length():
		var code: int = game_id.unicode_at(index)
		var alphanumeric: bool = (code >= 97 and code <= 122) or (code >= 48 and code <= 57)
		if not alphanumeric and (index == 0 or code not in [45, 95]):
			return false
	return true

static func valid_room_number(number: String) -> bool:
	if number.length() != 6:
		return false
	for index: int in number.length():
		var code: int = number.unicode_at(index)
		if code < 48 or code > 57:
			return false
	return true

static func valid_password(password: String) -> bool:
	return password.length() >= 4 and password.length() <= 64

static func valid_create(capacity: int, visibility: String, password: String) -> bool:
	return capacity >= 1 and capacity <= 6 and (visibility == "public" or (visibility == "private" and valid_password(password)))

static func create_payload(capacity: int, visibility: String, password: String) -> Dictionary:
	var payload: Dictionary = {"capacity": capacity, "visibility": visibility}
	if visibility == "private":
		payload["password"] = password
	return payload

static func join_payload(number: String, password: Variant = null) -> Dictionary:
	var payload: Dictionary = {"room_number": number}
	if password != null:
		payload["password"] = password
	return payload

static func encode_request(op: String, id: String, payload: Dictionary) -> String:
	return JSON.stringify({"version": 1, "op": op, "request_id": id, "payload": payload, "error": null})

static func safe_integer(value: Variant, minimum: int = 0) -> bool:
	if typeof(value) != TYPE_INT and typeof(value) != TYPE_FLOAT:
		return false
	return is_finite(float(value)) and value >= minimum and value <= MAX_SAFE_INTEGER and floor(float(value)) == float(value)

static func _nonempty(value: Variant) -> bool:
	return value is String and not value.is_empty()

static func _keys(value: Dictionary, keys: Array) -> bool:
	if value.size() != keys.size():
		return false
	for key: String in keys:
		if not value.has(key):
			return false
	return true

static func _vector(value: Variant) -> bool:
	if not value is Dictionary or not _keys(value, ["x", "y", "z"]):
		return false
	for axis: String in ["x", "y", "z"]:
		var number: Variant = value[axis]
		if typeof(number) != TYPE_INT and typeof(number) != TYPE_FLOAT:
			return false
		if not is_finite(float(number)) or abs(float(number)) > 3.402823466e38:
			return false
	return true

static func valid_snapshot(value: Variant) -> bool:
	if not value is Dictionary:
		return false
	var required: Array = ["game_id", "room_id", "room_number", "visibility", "capacity", "match_id", "phase", "host_id", "members", "revision", "game_state", "server_tick", "snapshot_seq", "last_processed_input_seq"]
	if not _keys(value, required):
		return false
	if not value.game_id is String or not valid_game_id(value.game_id):
		return false
	if not _nonempty(value.room_id) or not value.room_number is String or not valid_room_number(value.room_number):
		return false
	if value.visibility not in ["public", "private"] or not safe_integer(value.capacity, 1) or value.capacity > 6:
		return false
	if not safe_integer(value.revision, 1) or not safe_integer(value.snapshot_seq, 1) or not safe_integer(value.server_tick):
		return false
	if not value.members is Array or value.members.is_empty() or value.members.size() > value.capacity:
		return false
	var ids: Dictionary = {}
	for member: Variant in value.members:
		if not member is Dictionary or not _keys(member, ["session_id"]) or not _nonempty(member.session_id) or ids.has(member.session_id):
			return false
		ids[member.session_id] = true
	if not value.host_id is String or not ids.has(value.host_id):
		return false
	if not value.last_processed_input_seq is Dictionary or value.last_processed_input_seq.size() != ids.size():
		return false
	for id: String in ids:
		if not value.last_processed_input_seq.has(id) or not safe_integer(value.last_processed_input_seq[id]):
			return false
	if value.phase == "WAITING":
		return value.match_id == null and value.game_state == null and value.server_tick == 0
	if value.phase != "PLAYING" or not _nonempty(value.match_id) or not value.game_state is Dictionary or not _keys(value.game_state, ["players"]):
		return false
	if not value.game_state.players is Array or value.game_state.players.size() != ids.size():
		return false
	var player_ids: Dictionary = {}
	for player: Variant in value.game_state.players:
		if not player is Dictionary or not _keys(player, ["session_id", "position", "velocity"]):
			return false
		if not player.session_id is String or not ids.has(player.session_id) or player_ids.has(player.session_id) or not _vector(player.position) or not _vector(player.velocity):
			return false
		player_ids[player.session_id] = true
	return true

static func _valid_payload(op: String, payload: Dictionary, event: bool) -> bool:
	match op:
		"session.guest":
			return event and _keys(payload, ["session_id", "game_id"]) and _nonempty(payload.session_id) and payload.game_id is String and valid_game_id(payload.game_id)
		"room.create", "room.join", "room.start", "game.started":
			return _keys(payload, ["snapshot"]) and valid_snapshot(payload.snapshot) and (event if op == "game.started" else not event) and (op not in ["room.start", "game.started"] or payload.snapshot.phase == "PLAYING")
		"room.state", "game.snapshot":
			return event and valid_snapshot(payload) and (op != "game.snapshot" or payload.phase == "PLAYING")
		"room.list":
			if event or not _keys(payload, ["rooms"]) or not payload.rooms is Array:
				return false
			for snapshot: Variant in payload.rooms:
				if not valid_snapshot(snapshot):
					return false
			return true
		"room.leave":
			return not event and payload.is_empty()
		"game.action":
			return not event and _keys(payload, ["match_id", "input_seq", "server_tick"]) and _nonempty(payload.match_id) and safe_integer(payload.input_seq, 1) and safe_integer(payload.server_tick)
		"game.ended":
			return event and _keys(payload, ["match_id", "reason", "server_tick", "snapshot"]) and _nonempty(payload.match_id) and payload.reason is String and safe_integer(payload.server_tick) and valid_snapshot(payload.snapshot) and payload.snapshot.phase == "WAITING"
		"game.event":
			return event and _keys(payload, ["event_id", "match_id", "server_tick", "type", "session_id"]) and _nonempty(payload.event_id) and _nonempty(payload.match_id) and safe_integer(payload.server_tick) and _nonempty(payload.type) and _nonempty(payload.session_id)
	return false

static func decode_server(text: String) -> Dictionary:
	var invalid: Dictionary = {"ok": false, "message": {}, "error": "Invalid server message."}
	if text.to_utf8_buffer().size() > MAX_MESSAGE_BYTES:
		return invalid
	var parser: JSON = JSON.new()
	if parser.parse(text) != OK or not parser.data is Dictionary:
		return invalid
	var message: Dictionary = parser.data
	if not _keys(message, ["version", "op", "request_id", "payload", "error"]):
		return invalid
	if not safe_integer(message.version, 1) or message.version != 1 or not message.op is String or message.op.to_utf8_buffer().size() > 64:
		return invalid
	if message.request_id != null and (not _nonempty(message.request_id) or message.request_id.to_utf8_buffer().size() > 128):
		return invalid
	if message.error != null:
		if message.payload != null or not message.error is Dictionary or not _keys(message.error, ["code", "message"]) or not _nonempty(message.error.code) or not message.error.message is String:
			return invalid
	else:
		if not message.payload is Dictionary or not _valid_payload(message.op, message.payload, message.request_id == null):
			return invalid
	return {"ok": true, "message": message, "error": ""}
