extends RefCounted
class_name SnapshotBuffer

var duration_ms: int = 16
var _latest_received_ms: int = 0
var _latest_positions: Dictionary = {}
var _previous_positions: Dictionary = {}
var _room_id: String = ""
var _match_id: String = ""
var _snapshot_seq: int = -1

func clear() -> void:
	_latest_positions.clear()
	_previous_positions.clear()
	_room_id = ""
	_match_id = ""
	_snapshot_seq = -1
	_latest_received_ms = 0
	duration_ms = 16

func retain_members(ids: Dictionary) -> void:
	for id: String in _latest_positions.keys():
		if not ids.has(id):
			_latest_positions.erase(id)
			_previous_positions.erase(id)

func push_snapshot(snapshot: Dictionary, received_ms: int) -> bool:
	if snapshot.get("phase") != "PLAYING":
		clear()
		return false
	var room_id: String = snapshot.room_id
	var match_id: String = snapshot.match_id
	if room_id != _room_id or match_id != _match_id:
		clear()
		_room_id = room_id
		_match_id = match_id
	var members: Dictionary = {}
	for member: Dictionary in snapshot.members:
		members[member.session_id] = true
	retain_members(members)
	var seq: int = int(snapshot.snapshot_seq)
	if seq <= _snapshot_seq:
		return false
	var positions: Dictionary = {}
	for player: Dictionary in snapshot.game_state.players:
		if members.has(player.session_id):
			var p: Dictionary = player.position
			positions[player.session_id] = Vector3(p.x, p.y, p.z)
	var gap: int = received_ms - _latest_received_ms
	var snap: bool = _snapshot_seq < 0 or gap > 500
	duration_ms = clampi(gap, 16, 200)
	_previous_positions.clear()
	for id: String in positions:
		_previous_positions[id] = positions[id] if snap else _latest_positions.get(id, positions[id])
	_latest_positions = positions
	_latest_received_ms = received_ms
	_snapshot_seq = seq
	return true

func sample_positions(now_ms: int) -> Dictionary:
	var result: Dictionary = {}
	var alpha := clampf(float(now_ms - _latest_received_ms) / float(duration_ms), 0.0, 1.0)
	for id: String in _latest_positions:
		var latest: Vector3 = _latest_positions[id]
		var previous: Vector3 = _previous_positions.get(id, latest)
		result[id] = previous.lerp(latest, alpha)
	return result
