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

func controller_for(client: CoopClient) -> InputController:
	var controller := InputController.new()
	controller.bind_client(client)
	return controller

func packet(client: Support.FakeClient, index: int = -1) -> Dictionary:
	return JSON.parse_string(client.sent[index])

func reply(client: Support.FakeClient, index: int = -1, code: String = "") -> void:
	var request := packet(client, index)
	var payload: Variant = {"match_id": request.payload.match_id, "input_seq": request.payload.input_seq, "server_tick": 1}
	var error: Variant = null
	if not code.is_empty():
		payload = null
		error = {"code": code, "message": "Controlled test response."}
	client._accept_text(Support.event("game.action", payload, request.request_id, error), client.state.connection_generation, client.clock_ms)

func initial_move(client: Support.FakeClient, controller: InputController) -> void:
	controller.submit_direction(Vector3.FORWARD)
	reply(client)

func test_async_retry(direction: Vector3, code: String) -> void:
	var client := Support.ready_client(true)
	var controller := controller_for(client)
	initial_move(client, controller)
	controller.submit_direction(direction)
	var failed_id: String = packet(client).request_id
	var completions: Array = []
	client.request_completed.connect(func(op: String, id: String, _payload: Dictionary, error: Dictionary):
		if op == "game.action": completions.append({"id": id, "error": error}))
	if code == "CLIENT_TIMEOUT":
		client.clock_ms = 10000
		client._expire_requests(client.clock_ms)
		check(completions.size() == 1 and completions[0].id == failed_id and completions[0].error.code == code, "timeout identifies the failed action request")
	else:
		reply(client, -1, code)
	var failed_at := client.clock_ms
	for step: int in range(100):
		client.clock_ms = failed_at + step
		controller.submit_direction(direction)
	check(client.sent.size() == 2, code + " has no frame-rate retry")
	client.clock_ms = failed_at + 100
	controller.submit_direction(direction)
	check(client.sent.size() == 3, code + " retries current " + str(direction) + " after backoff")
	if client.sent.size() == 3:
		check(packet(client).payload.input_seq == 3, "retry consumes new monotonic sequence")
		check(packet(client).payload.action.direction == {"x": direction.x, "y": direction.y, "z": direction.z}, "retry retains latest intended direction")
		reply(client)
		for step: int in range(10):
			client.clock_ms += 1000
			controller.submit_direction(direction)
		check(client.sent.size() == 3, "acknowledged retry deduplicates")
	controller.free()
	client.free()

func test_stop_barrier_and_stale_results() -> void:
	for old_code: String in ["", "SERVER_BUSY"]:
		var client := Support.ready_client(true)
		var controller := controller_for(client)
		controller.submit_direction(Vector3.FORWARD)
		controller.submit_direction(Vector3.ZERO)
		reply(client, 0, old_code)
		controller.submit_direction(Vector3.RIGHT)
		check(client.sent.size() == 2, "old ACK/error cannot release a newer unconfirmed stop")
		reply(client, 1, "SERVER_BUSY")
		client.clock_ms = 100
		controller.submit_direction(Vector3.RIGHT)
		check(client.sent.size() == 3 and packet(client).payload.action.direction.x == 0, "failed stop precedes later motion")
		if client.sent.size() == 3:
			reply(client)
			check(client.sent.size() == 3, "ACK callback never replays queued direction")
			controller.submit_direction(Vector3.RIGHT)
			check(client.sent.size() == 4 and packet(client).payload.input_seq == 4 and packet(client).payload.action.direction.x == 1, "fresh current sample moves only after stop confirmation")
		controller.free()
		client.free()
	var client := Support.ready_client(true)
	var controller := controller_for(client)
	controller.submit_direction(Vector3.FORWARD)
	controller.submit_direction(Vector3.RIGHT)
	reply(client, 1)
	client.clock_ms = 10000
	client._expire_requests(client.clock_ms)
	client.clock_ms += 1000
	controller.submit_direction(Vector3.RIGHT)
	check(client.sent.size() == 2, "timeout after later ACK cannot revive superseded intent")
	controller.free()
	client.free()

func test_nonzero_stale_results() -> void:
	for code: String in ["", "SERVER_BUSY"]:
		var client := Support.ready_client(true)
		var controller := controller_for(client)
		controller.submit_direction(Vector3.FORWARD)
		controller.submit_direction(Vector3.RIGHT)
		reply(client, 0, code)
		reply(client, 1, "SERVER_BUSY")
		client.clock_ms = 100
		controller.submit_direction(Vector3.RIGHT)
		check(client.sent.size() == 3 and packet(client).payload.action.direction.x == 1, "old nonzero outcome cannot overwrite newer intent")
		controller.free()
		client.free()

func test_backoff_and_permanent_failure() -> void:
	var client := Support.ready_client(true)
	var controller := controller_for(client)
	controller.submit_direction(Vector3.RIGHT)
	var delays := [100, 200, 400, 800, 1000, 1000]
	for delay: int in delays:
		reply(client, -1, "SERVER_BUSY")
		var before := client.sent.size()
		for elapsed: int in range(delay):
			controller.submit_direction(Vector3.RIGHT)
			client.clock_ms += 1
		check(client.sent.size() == before, "backoff blocks every earlier frame")
		controller.submit_direction(Vector3.RIGHT)
		check(client.sent.size() == before + 1, "retry at bounded backoff deadline")
		check(packet(client).payload.input_seq == client.sent.size(), "no sequence rewind on rejection")
	reply(client, -1, "INVALID_PHASE")
	var count := client.sent.size()
	for step: int in range(20):
		client.clock_ms += 10000
		controller.submit_direction(Vector3.RIGHT)
	check(client.sent.size() == count, "permanent error never retries the same intent")
	controller.submit_direction(Vector3.ZERO)
	reply(client, -1, "INVALID_PHASE")
	count = client.sent.size()
	for step: int in range(20):
		client.clock_ms += 10000
		controller.submit_direction(Vector3.LEFT)
	check(client.sent.size() == count, "permanent failed stop remains a motion barrier without endless retries")
	controller.free()
	client.free()

func test_processed_snapshot_and_ack_identity() -> void:
	var client := Support.ready_client(true)
	var controller := controller_for(client)
	initial_move(client, controller)
	controller.submit_direction(Vector3.ZERO)
	var request := packet(client)
	client._accept_text(Support.event("game.action", {"match_id": "mA", "input_seq": 1, "server_tick": 1}, request.request_id), 1, 0)
	controller.submit_direction(Vector3.RIGHT)
	check(client.sent.size() == 2, "mismatched ACK sequence does not confirm stop")
	var snapshot := Support.snapshot("rA", "mA", 2, 2)
	snapshot.last_processed_input_seq.sA = 2
	client._accept_text(Support.event("game.snapshot", snapshot), 1, 0)
	controller.submit_direction(Vector3.RIGHT)
	check(client.sent.size() == 3, "authoritative processed sequence confirms the stop")
	controller.free()
	client.free()

func test_scope_fences() -> void:
	for transition: String in ["reconnect", "rejoin", "match", "rebind"]:
		var client := Support.ready_client(true)
		var controller := controller_for(client)
		initial_move(client, controller)
		controller.submit_direction(Vector3.ZERO)
		reply(client, -1, "SERVER_BUSY")
		var old_id: String = packet(client).request_id
		var old_client := client
		if transition == "reconnect":
			client.disconnect_guest()
			client.state.accept_guest("sA", "default")
			client.state.apply_admission(Support.snapshot("rA", "mA"), client.state.begin_admission())
		elif transition == "rejoin":
			client.leave_room()
			client.state.apply_admission(Support.snapshot("rA", "mA"), client.state.begin_admission())
		elif transition == "match":
			client._accept_text(Support.event("game.ended", {"match_id": "mA", "reason": "test", "server_tick": 1, "snapshot": Support.snapshot("rA", null, 2, 2)}), 1, 0)
			client._accept_text(Support.event("game.started", {"snapshot": Support.snapshot("rA", "mB", 3, 3)}), 1, 0)
		else:
			client = Support.ready_client(true)
			controller.bind_client(client)
		client.snapshot_changed.emit(client.state.current_snapshot)
		var before := client.sent.size()
		old_client.request_completed.emit("game.action", old_id, {}, {"code": "SERVER_BUSY", "message": "Late failure."})
		client.clock_ms = 1000
		controller.submit_direction(Vector3.ZERO)
		check(client.sent.size() == before, transition + " drops old retry without replay")
		controller.submit_direction(Vector3.RIGHT)
		check(client.sent.size() == before + 1 and packet(client).payload.action.direction.x == 1, transition + " accepts fresh intent without stale stop")
		check(packet(client).payload.input_seq == (1 if transition in ["reconnect", "rebind"] else 3), transition + " preserves connection sequence lifetime")
		controller.free()
		if client != old_client: old_client.free()
		client.free()

func test_gates_and_local_failure() -> void:
	var client := Support.ready_client(true)
	var controller := controller_for(client)
	initial_move(client, controller)
	controller.set_input_gate(false, false)
	reply(client, -1, "SERVER_BUSY")
	controller.set_input_gate(true, false)
	controller.set_view_mode("2d")
	client.clock_ms = 100
	controller.submit_direction(Vector3.RIGHT)
	check(client.sent.size() == 3 and packet(client).payload.action.direction.x == 0, "focus/mode changes preserve failed zero and backoff")
	reply(client)
	controller.set_input_gate(true, true)
	controller.submit_direction(Vector3.RIGHT)
	check(client.sent.size() == 3, "text focus and repeated zero do not send duplicates")
	controller.set_input_gate(true, false)
	controller.submit_direction(Vector3(1, 1, 0))
	check(client.sent.size() == 4 and packet(client).payload.action.direction.y == 0, "2D fresh sample keeps y zero")
	client.send_error = ERR_BUSY
	controller.submit_direction(Vector3.ZERO)
	var count := client.sent.size()
	client.send_error = OK
	controller.submit_direction(Vector3.RIGHT)
	check(client.sent.size() == count, "local send failure is rate limited too")
	client.clock_ms += 100
	controller.submit_direction(Vector3.RIGHT)
	check(client.sent.size() == count + 1 and packet(client).payload.action.direction.x == 0 and packet(client).payload.input_seq == 5, "unsent stop retries before motion without consuming a failed local sequence")
	controller.free()
	client.free()

func test_lobby_pending() -> void:
	var client := Support.ready_client()
	client.state.apply_admission(Support.snapshot(), client.state.begin_admission())
	client.status = "waiting"
	var lobby := LobbyPanel.new()
	lobby.bind_client(client)
	root.add_child(lobby)
	await process_frame
	lobby._start_clicked()
	var start: Dictionary = JSON.parse_string(client.sent.back())
	client._accept_text(Support.event("room.state", Support.snapshot("rA", null, 2, 2, ["sA", "sB"])), 1, 0)
	check(lobby.start_button.disabled and lobby.leave_button.disabled, "intervening WAITING snapshot keeps lifecycle controls disabled")
	client.request_completed.emit("room.start", "unrelated", {}, {})
	check(lobby.start_button.disabled, "unmatched lifecycle completion cannot clear pending")
	lobby._start_clicked()
	check(client.sent.size() == 1, "delayed Start plus snapshot emits exactly one wire request")
	client._accept_text(Support.event("room.start", null, start.request_id, {"code": "SERVER_BUSY", "message": "Controlled start failure."}), 1, 0)
	check(not lobby.start_button.disabled, "matching failed Start unlocks lifecycle controls")
	lobby._start_clicked()
	client.disconnect_guest()
	check(not lobby.connect_button.disabled and not lobby._operation_pending, "disconnect clears lifecycle pending")
	lobby.free()
	client.free()

func test_tooltip_target() -> void:
	var main := Main.new()
	root.add_child(main)
	main.set_process(false)
	main.controller.set_process(false)
	main.client.state.game_id = "w".repeat(64)
	main._on_snapshot({"phase": "PLAYING", "room_number": "123456", "members": [1]})
	check(main._summary.mouse_filter != Control.MOUSE_FILTER_IGNORE and "w".repeat(64) in main._summary.tooltip_text, "clipped summary is a hoverable full-identity tooltip target")
	main.free()

func _run() -> void:
	MovementInput.ensure_actions()
	test_async_retry(Vector3.ZERO, "SERVER_BUSY")
	test_async_retry(Vector3.RIGHT, "SERVER_BUSY")
	test_async_retry(Vector3.ZERO, "CLIENT_TIMEOUT")
	test_async_retry(Vector3.ZERO, "REQUEST_TIMEOUT")
	test_stop_barrier_and_stale_results()
	test_nonzero_stale_results()
	test_backoff_and_permanent_failure()
	test_processed_snapshot_and_ack_identity()
	test_scope_fences()
	test_gates_and_local_failure()
	await test_lobby_pending()
	test_tooltip_target()
	print("final-fix checks=", checks, " failures=", failures)
	quit(0 if failures == 0 else 1)
