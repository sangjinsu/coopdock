extends Node
class_name InputController

const RETRY_MIN_MS: int = 100
const RETRY_MAX_MS: int = 1000
const TRANSIENT_ERRORS: Array[String] = ["SERVER_BUSY", "REQUEST_TIMEOUT", "CLIENT_TIMEOUT", "CLIENT_BUSY", "CLIENT_SEND_FAILED"]

var view_mode: String = "3d"
var _client: CoopClient
# This is enqueue history only, never proof of server processing.
var _last_sent_direction: Vector3 = Vector3.ZERO
var _movement := MovementInput.new()
var _focused: bool = true
var _text_focused: bool = false
var _pending_stop: bool = false
var _identity: String = ""
var _intent_direction: Vector3 = Vector3.ZERO
var _request_id: String = ""
var _input_seq: int = 0
var _confirmed: bool = true
var _blocked: bool = false
var _retry_at_ms: int = 0
var _retry_delay_ms: int = RETRY_MIN_MS
var _sending: bool = false
var _send_failure: String = ""

func _ready() -> void:
	MovementInput.ensure_actions()

func bind_client(client: CoopClient) -> void:
	if _client != null:
		_client.snapshot_changed.disconnect(_on_snapshot)
		_client.request_completed.disconnect(_on_completed)
		_client.request_failed.disconnect(_on_failure)
	_client = client
	_reset_tracking()
	_identity = _scope()
	if client != null:
		client.snapshot_changed.connect(_on_snapshot)
		client.request_completed.connect(_on_completed)
		client.request_failed.connect(_on_failure)

func _scope() -> String:
	if _client == null or _client.state.current_snapshot.get("phase") != "PLAYING":
		return ""
	var state := _client.state
	return JSON.stringify([state.connection_generation, state.operation_epoch, state.game_id,
		state.session_id, state.current_snapshot.room_id, state.current_snapshot.match_id])

func _reset_tracking() -> void:
	_movement.release_actions()
	_last_sent_direction = Vector3.ZERO
	_pending_stop = false
	_intent_direction = Vector3.ZERO
	_request_id = ""
	_input_seq = 0
	_confirmed = true
	_blocked = false
	_retry_at_ms = 0
	_retry_delay_ms = RETRY_MIN_MS

func _sync_scope() -> void:
	var identity := _scope()
	if identity != _identity:
		_reset_tracking()
		_identity = identity

func _on_snapshot(_snapshot: Dictionary) -> void:
	_sync_scope()
	if _identity.is_empty() or _input_seq == 0 or _confirmed:
		return
	var processed: Dictionary = _client.state.current_snapshot.get("last_processed_input_seq", {})
	if int(processed.get(_client.state.session_id, 0)) >= _input_seq:
		_confirm_intent()

func _confirm_intent() -> void:
	_confirmed = true
	_request_id = ""
	_blocked = false
	_retry_delay_ms = RETRY_MIN_MS
	if _intent_direction == Vector3.ZERO:
		_pending_stop = false

func _on_completed(op: String, id: String, payload: Dictionary, error: Dictionary) -> void:
	_sync_scope()
	if op != "game.action" or _identity.is_empty() or id != _request_id or _request_id.is_empty():
		return
	_request_id = ""
	if not error.is_empty():
		_schedule_failure(str(error.get("code", "")))
	elif payload.get("match_id") == _client.state.current_snapshot.match_id and int(payload.get("input_seq", 0)) == _input_seq:
		_confirm_intent()
	else:
		# A response for a different sequence does not prove this intent ran.
		_schedule_failure("CLIENT_TIMEOUT")

func _on_failure(op: String, code: String, _message: String) -> void:
	# Local send failures have no request ID; capture only the synchronous call.
	if _sending and op == "game.action":
		_send_failure = code

func _schedule_failure(code: String) -> void:
	_blocked = code not in TRANSIENT_ERRORS
	if not _blocked:
		_retry_at_ms = _client._now_msec() + _retry_delay_ms
		_retry_delay_ms = mini(_retry_delay_ms * 2, RETRY_MAX_MS)

func _process(_delta: float) -> void:
	# Reset held actions before sampling when a lifecycle changed without a signal.
	_sync_scope()
	submit_direction(_movement.sample(_focused, _text_focused, view_mode == "3d"))

func submit_direction(direction: Vector3) -> void:
	_sync_scope()
	if _identity.is_empty():
		return
	if view_mode == "2d":
		direction.y = 0.0
	if not _focused or _text_focused or _pending_stop:
		direction = Vector3.ZERO
	if direction != _intent_direction:
		_pending_stop = direction == Vector3.ZERO
		_intent_direction = direction
		_request_id = ""
		_input_seq = 0
		_confirmed = false
		_blocked = false
		_retry_at_ms = 0
		_retry_delay_ms = RETRY_MIN_MS
	if _confirmed or _blocked or not _request_id.is_empty() or _client._now_msec() < _retry_at_ms:
		return
	var sequence := _client.next_input_seq
	_sending = true
	_send_failure = ""
	var id := _client.send_direction(_intent_direction)
	_sending = false
	if id.is_empty():
		_schedule_failure(_send_failure)
	else:
		_request_id = id
		_input_seq = sequence
		_last_sent_direction = _intent_direction

func set_input_gate(focused: bool, text_input_focused: bool) -> void:
	if _focused == focused and _text_focused == text_input_focused:
		return
	_movement.release_actions()
	_focused = focused
	_text_focused = text_input_focused
	# Both transitions discard held keys; only a fresh sample can resume motion.
	submit_direction(Vector3.ZERO)

func set_view_mode(mode: String) -> void:
	if mode not in ["3d", "2d"] or mode == view_mode:
		return
	_sync_scope()
	_movement.release_actions()
	if not _pending_stop and _intent_direction == Vector3.ZERO:
		_confirmed = false
		_pending_stop = true
	submit_direction(Vector3.ZERO)
	view_mode = mode
