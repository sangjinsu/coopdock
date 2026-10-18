extends Node
class_name InputController

var view_mode: String = "3d"
var _client: CoopClient
var _last_sent_direction: Vector3 = Vector3.ZERO
var _movement := MovementInput.new()
var _focused: bool = true
var _text_focused: bool = false
var _pending_stop: bool = false
var _identity: String = ""

func _ready() -> void:
	MovementInput.ensure_actions()

func bind_client(client: CoopClient) -> void:
	if _client != null and _client.snapshot_changed.is_connected(_on_snapshot):
		_client.snapshot_changed.disconnect(_on_snapshot)
	_client = client
	_on_snapshot(client.state.current_snapshot)
	client.snapshot_changed.connect(_on_snapshot)

func _on_snapshot(snapshot: Dictionary) -> void:
	var identity := str(snapshot.get("room_id", "")) + "/" + str(snapshot.get("match_id", ""))
	if snapshot.get("phase") != "PLAYING":
		identity = ""
	if identity != _identity:
		_movement.release_actions()
		_last_sent_direction = Vector3.ZERO
		_pending_stop = false
		_identity = identity

func _process(_delta: float) -> void:
	submit_direction(_movement.sample(_focused, _text_focused, view_mode == "3d"))

func submit_direction(direction: Vector3) -> void:
	if _client == null or _client.state.current_snapshot.get("phase") != "PLAYING":
		return
	# An unsent stop always takes precedence over a new movement request.
	if _pending_stop:
		if not _client.send_direction(Vector3.ZERO).is_empty():
			_last_sent_direction = Vector3.ZERO
			_pending_stop = false
		return
	if view_mode == "2d":
		direction.y = 0.0
	if not _focused or _text_focused:
		direction = Vector3.ZERO
	if direction == _last_sent_direction:
		return
	if not _client.send_direction(direction).is_empty():
		_last_sent_direction = direction
	elif direction == Vector3.ZERO:
		_pending_stop = true

func set_input_gate(focused: bool, text_input_focused: bool) -> void:
	if _focused == focused and _text_focused == text_input_focused:
		return
	_movement.release_actions()
	_focused = focused
	_text_focused = text_input_focused
	# Both transitions discard held keys; a fresh physical event resumes motion.
	# Preserve an unsent stop until the next submit, even on focus recovery.
	if not (_pending_stop and focused and not text_input_focused):
		submit_direction(Vector3.ZERO)

func set_view_mode(mode: String) -> void:
	if mode not in ["3d", "2d"] or mode == view_mode:
		return
	_movement.release_actions()
	_pending_stop = true
	submit_direction(Vector3.ZERO)
	view_mode = mode
