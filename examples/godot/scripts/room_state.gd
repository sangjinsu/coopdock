extends RefCounted
class_name RoomState

var session_id: String = ""
var game_id: String = ""
var current_snapshot: Dictionary = {}
var connection_generation: int = 0
var operation_epoch: int = 0
var _metadata_revision: int = 0
var _periodic_seq: int = 0
var _retired_matches: Dictionary = {}

func new_connection(generation: int) -> void:
	connection_generation = generation
	session_id = ""
	game_id = ""
	clear_room()

func accept_guest(id: String, game: String) -> void:
	if session_id.is_empty() and not id.is_empty() and WireCodec.valid_game_id(game):
		session_id = id
		game_id = game

func begin_admission() -> int:
	operation_epoch += 1
	return operation_epoch

func _includes_self(snapshot: Dictionary) -> bool:
	for member: Dictionary in snapshot.members:
		if member.session_id == session_id:
			return true
	return false

func apply_admission(snapshot: Dictionary, epoch: int) -> bool:
	if epoch != operation_epoch or session_id.is_empty() or not current_snapshot.is_empty() or not WireCodec.valid_snapshot(snapshot) or snapshot.game_id != game_id or not _includes_self(snapshot):
		return false
	current_snapshot = snapshot.duplicate(true)
	_metadata_revision = int(snapshot.revision)
	_periodic_seq = int(snapshot.snapshot_seq)
	_retired_matches.clear()
	return true

func _retire_current_match() -> void:
	var match_id: Variant = current_snapshot.get("match_id")
	if match_id is String:
		_retired_matches[match_id] = true

func apply_event(op: String, payload: Dictionary) -> bool:
	if current_snapshot.is_empty() or session_id.is_empty():
		return false
	var incoming: Variant = payload.get("snapshot", {}) if op in ["game.started", "game.ended"] else payload
	if not WireCodec.valid_snapshot(incoming) or incoming.game_id != game_id:
		return false
	var snapshot: Dictionary = incoming
	if snapshot.room_id != current_snapshot.room_id:
		return false
	if op == "game.ended" and payload.get("match_id") != current_snapshot.match_id:
		return false
	var incoming_match: Variant = snapshot.match_id
	var current_match: Variant = current_snapshot.match_id
	if incoming_match is String and _retired_matches.has(incoming_match):
		return false
	if op == "game.snapshot":
		if current_match == null or incoming_match != current_match or int(snapshot.snapshot_seq) <= _periodic_seq or int(snapshot.revision) < _metadata_revision:
			return false
	elif op in ["room.state", "game.started", "game.ended"]:
		if int(snapshot.revision) < _metadata_revision:
			return false
		if int(snapshot.revision) == _metadata_revision and (incoming_match != current_match or int(snapshot.snapshot_seq) <= int(current_snapshot.snapshot_seq)):
			return false
	else:
		return false
	if not _includes_self(snapshot):
		clear_room()
		return true
	if incoming_match != current_match:
		_retire_current_match()
		_periodic_seq = 0
	var accepted: Dictionary = snapshot.duplicate(true)
	# Metadata may arrive after a newer periodic capture. Preserve newer positions,
	# while removing members immediately rather than reviving them from old buffers.
	if op == "room.state" and incoming_match != null and incoming_match == current_match and int(snapshot.snapshot_seq) < _periodic_seq:
		var kept: Array = []
		var active_ids: Dictionary = {}
		for member: Dictionary in snapshot.members:
			active_ids[member.session_id] = true
		for player: Dictionary in current_snapshot.game_state.players:
			if active_ids.has(player.session_id):
				kept.append(player.duplicate(true))
		accepted.game_state.players = kept
		accepted.server_tick = current_snapshot.server_tick
		accepted.snapshot_seq = current_snapshot.snapshot_seq
		var processed: Dictionary = {}
		for id: String in active_ids:
			processed[id] = current_snapshot.last_processed_input_seq.get(id, snapshot.last_processed_input_seq[id])
		accepted.last_processed_input_seq = processed
	_metadata_revision = maxi(_metadata_revision, int(snapshot.revision))
	_periodic_seq = maxi(_periodic_seq, int(snapshot.snapshot_seq))
	current_snapshot = accepted
	return true

func clear_room() -> void:
	operation_epoch += 1
	current_snapshot = {}
	_metadata_revision = 0
	_periodic_seq = 0
	_retired_matches.clear()
