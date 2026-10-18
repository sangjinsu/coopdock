extends RefCounted
class_name TestDriver

var client: CoopClient
var replies: Dictionary = {}
var captures: Dictionary = {}
var capture_order: Array[String] = []
var failure: String = ""

func _init(value: CoopClient = null) -> void:
	if value != null:
		attach(value)

func attach(value: CoopClient) -> void:
	client = value
	client.request_completed.connect(_on_reply)
	client.snapshot_changed.connect(_on_snapshot)

static func capture_key(snapshot: Dictionary) -> String:
	return "%s/%s/%s/%s/%s/%s" % [snapshot.get("game_id"), snapshot.get("room_id"), snapshot.get("match_id"), snapshot.get("revision"), snapshot.get("server_tick"), snapshot.get("snapshot_seq")]

static func same_capture(states: Array) -> bool:
	if states.is_empty():
		return false
	var first: Dictionary = states[0]
	for state: Dictionary in states:
		if capture_key(state) != capture_key(first) or state != first:
			return false
	return true

func _fail(reason: String) -> void:
	failure = reason
	printerr("TestDriver failure: ", reason)
	client.get_tree().quit(1)

func _on_reply(op: String, id: String, payload: Dictionary, error: Dictionary) -> void:
	if replies.size() >= 128:
		_fail("reply queue overflow")
		return
	replies[id] = {"op": op, "payload": payload.duplicate(true), "error": error.duplicate(true)}

func _on_snapshot(snapshot: Dictionary) -> void:
	if snapshot.is_empty():
		return
	var key := capture_key(snapshot)
	if captures.has(key):
		if captures[key] != snapshot:
			_fail("same capture identity changed full snapshot")
		return
	captures[key] = snapshot.duplicate(true)
	capture_order.append(key)
	if capture_order.size() > 128:
		captures.erase(capture_order.pop_front())

func await_guest(timeout_ms: int = 5000) -> String:
	var deadline := Time.get_ticks_msec() + timeout_ms
	while client.state.session_id.is_empty() and Time.get_ticks_msec() < deadline:
		await client.get_tree().process_frame
	if client.state.session_id.is_empty():
		_fail("guest timeout")
	return client.state.session_id

func await_reply(id: String, timeout_ms: int = 10000) -> Dictionary:
	if id.is_empty():
		_fail("local request refused")
		return {"payload": {}, "error": {"code": "LOCAL_REFUSED"}}
	var deadline := Time.get_ticks_msec() + timeout_ms
	while not replies.has(id) and Time.get_ticks_msec() < deadline:
		await client.get_tree().process_frame
	if not replies.has(id):
		_fail("reply timeout")
		return {"payload": {}, "error": {"code": "TIMEOUT"}}
	var reply: Dictionary = replies[id]
	replies.erase(id)
	return reply

func await_snapshot(predicate: Callable, timeout_ms: int = 10000) -> Dictionary:
	var deadline := Time.get_ticks_msec() + timeout_ms
	while Time.get_ticks_msec() < deadline:
		for index: int in range(capture_order.size() - 1, -1, -1):
			var snapshot: Dictionary = captures[capture_order[index]]
			if predicate.call(snapshot):
				return snapshot.duplicate(true)
		await client.get_tree().process_frame
	_fail("snapshot predicate timeout")
	return {}

func close() -> void:
	if is_instance_valid(client):
		client.disconnect_guest()
		client.queue_free()
