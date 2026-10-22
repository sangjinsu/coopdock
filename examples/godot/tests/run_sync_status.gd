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

func sync_status(client: CoopClient) -> Dictionary:
	if client.has_method("get_sync_status"):
		return client.call("get_sync_status")
	return {"input_ack_rtt_ms": null, "snapshot_age_ms": null, "server_tick": null, "snapshot_seq": null}

func check_unmeasured(client: CoopClient, context: String) -> void:
	var value := sync_status(client)
	check(value.input_ack_rtt_ms == null and value.snapshot_age_ms == null and value.server_tick == null and value.snapshot_seq == null, context + " exposes unmeasured values")

func request(client: Support.FakeClient) -> Dictionary:
	return JSON.parse_string(client.sent.back())

func acknowledge(client: Support.FakeClient, packet: Dictionary, received_ms: int, match_id: String = "", sequence: int = -1, generation: int = -1) -> void:
	client.clock_ms = received_ms
	client._accept_text(Support.event("game.action", {"match_id": packet.payload.match_id if match_id.is_empty() else match_id,
		"input_seq": packet.payload.input_seq if sequence < 0 else sequence, "server_tick": 11}, packet.request_id),
		client.state.connection_generation if generation < 0 else generation, received_ms)

func periodic(client: CoopClient, received_ms: int, seq: int = 2, tick: int = 11, revision: int = 2) -> void:
	var snapshot := Support.snapshot("rA", "mA", revision, seq)
	snapshot.server_tick = tick
	client._accept_text(Support.event("game.snapshot", snapshot), client.state.connection_generation, received_ms)

func measure(client: Support.FakeClient) -> void:
	client.clock_ms = 100
	client.send_direction(Vector3.RIGHT)
	acknowledge(client, request(client), 146)
	client.clock_ms = 200
	periodic(client, 200)

func test_guest_initial_list() -> void:
	var client := Support.FakeClient.new()
	client.state.new_connection(1)
	client._expected_game_id = "default"
	client.status = "connecting"
	client.clock_ms = 25
	check(client.state.session_id.is_empty() and client.sent.is_empty(), "connection remains unready before guest approval")
	var guest := Support.event("session.guest", {"session_id": "sA", "game_id": "default"})
	client._accept_text(guest, 1, 25)
	check(client.status == "guest" and client.state.session_id == "sA", "valid guest approval makes connection ready")
	check(client.sent.size() == 1, "valid guest requests initial public room list exactly once")
	if client.sent.size() == 1:
		var initial := request(client)
		check(initial.op == "room.list" and initial.payload == {}, "initial list uses ordinary room.list wire request")
		var rooms: Array = []
		client.rooms_received.connect(func(value: Array): rooms.append_array(value))
		client._accept_text(Support.event("room.list", {"rooms": [Support.snapshot()]}, initial.request_id), 1, 30)
		check(rooms.size() == 1, "initial room list reaches existing public signal")
	client._accept_text(guest, 1, 35)
	client._accept_text(guest, 0, 35)
	check(client.sent.size() == 1, "duplicate and old-generation guest cannot repeat initial list")
	client.clock_ms = 30024
	client._process(0)
	check(client.sent.size() == 1, "initial list preserves heartbeat interval from guest approval")
	client.clock_ms = 30025
	client._process(0)
	check(client.sent.size() == 2 and request(client).op == "room.list", "heartbeat remains a room.list data frame")
	client.free()
	client = Support.FakeClient.new()
	client.state.new_connection(1)
	client._expected_game_id = "game-a"
	client.status = "connecting"
	client._accept_text(guest, 1, 25)
	check(client.status == "disconnected" and client.sent.is_empty(), "foreign guest never triggers initial list")
	client.free()

func test_close_guidance() -> void:
	for manual: bool in [false, true]:
		var client := Support.ready_client(true)
		client.status = "playing"
		client._peer = WebSocketPeer.new()
		var lobby := LobbyPanel.new()
		lobby.bind_client(client)
		root.add_child(lobby)
		await process_frame
		measure(client)
		lobby.create_password_edit.text = "private-secret"
		lobby.join_password_edit.text = "private-secret"
		var errors: Array = []
		client.request_failed.connect(func(op: String, code: String, message: String): errors.append([op, code, message]))
		if manual:
			lobby._disconnect_clicked()
		client._process(0)
		check(client.status == "disconnected" and client.state.current_snapshot.is_empty() and client.state.session_id.is_empty(), "socket close clears membership")
		check_unmeasured(client, "closed socket")
		check(not lobby.connect_button.disabled and lobby.create_password_edit.text.is_empty() and lobby.join_password_edit.text.is_empty(), "closed socket enables manual reconnect and clears secrets")
		if manual:
			check(errors.is_empty(), "manual disconnect never emits unexpected-close error")
		else:
			check(errors.size() == 1 and errors[0][1] == "CLIENT_DISCONNECTED", "unexpected socket close emits explicit error once")
			check("CLIENT_DISCONNECTED" in lobby.status_label.text and "새 guest" in lobby.status_label.text and "연결" in lobby.status_label.text, "unexpected close guides manual new-guest reconnect")
		client._process(0)
		check(errors.size() == (0 if manual else 1), "closed peer cannot repeat disconnect failure each frame")
		lobby.free()
		client.free()

func test_ack_rtt_and_pending_bounds() -> void:
	var client := Support.ready_client(true)
	check(client.has_method("get_sync_status"), "client exposes common synchronization status accessor")
	check_unmeasured(client, "new membership")
	client.clock_ms = 100
	client.send_direction(Vector3.RIGHT)
	var action := request(client)
	var pending: Dictionary = client._pending[action.request_id]
	check(pending.get("sent_ms") == 100 and pending.get("match_id") == "mA" and pending.get("input_seq") == 1, "pending tracks monotonic send time and action identity")
	for value: Variant in pending.values():
		check(not value is Dictionary and not value is Array, "pending retains scalar correlation fields only")
	acknowledge(client, action, 146)
	check(sync_status(client).input_ack_rtt_ms == 46, "current successful action ACK measures 46 ms round trip")
	check(sync_status(client).snapshot_age_ms == null, "action ACK cannot fabricate a snapshot measurement")
	client.clock_ms = 170
	var list_id := client.list_rooms()
	client._accept_text(Support.event("room.list", {"rooms": []}, list_id), 1, 900)
	check(sync_status(client).input_ack_rtt_ms == 46, "room.list completion never updates input ACK RTT")
	client.max_pending = 2
	client.send_direction(Vector3.ZERO)
	client.send_direction(Vector3.LEFT)
	check(client.send_direction(Vector3.RIGHT).is_empty() and client._pending.size() == 2, "metric correlation remains under existing pending limit")
	client.free()
	client = Support.ready_client()
	client.create_room(6, "private", "private-secret")
	check(not JSON.stringify(client._pending).contains("private-secret") and not client._pending.values()[0].has("payload"), "correlation never retains passwords or original payload")
	client.free()

func test_invalid_ack_cannot_measure() -> void:
	for kind: String in ["sequence", "match", "unknown", "error", "expired", "old-generation"]:
		var client := Support.ready_client(true)
		measure(client)
		client.clock_ms = 250
		client.send_direction(Vector3.ZERO)
		var action := request(client)
		if kind == "sequence":
			acknowledge(client, action, 300, "", 999)
		elif kind == "match":
			acknowledge(client, action, 300, "other")
		elif kind == "unknown":
			action.request_id = "unknown"
			acknowledge(client, action, 300)
		elif kind == "error":
			client._accept_text(Support.event("game.action", null, action.request_id, {"code": "SERVER_BUSY", "message": "Controlled failure."}), 1, 300)
		elif kind == "expired":
			acknowledge(client, action, 10250)
		else:
			acknowledge(client, action, 300, "", -1, 0)
		check(sync_status(client).input_ack_rtt_ms == 46, kind + " ACK cannot overwrite measured RTT")
		client.free()

func test_snapshot_acceptance() -> void:
	var client := Support.ready_client(true)
	client.clock_ms = 250
	periodic(client, 250, 5, 17)
	var value := sync_status(client)
	check(value.snapshot_age_ms == 0 and value.server_tick == 17 and value.snapshot_seq == 5, "accepted periodic capture measures age and matching tick/seq")
	for kind: String in ["duplicate", "stale-seq", "stale-revision", "room", "match", "generation"]:
		client.clock_ms += 100
		var snapshot := Support.snapshot("rA", "mA", 2, 5)
		snapshot.server_tick = 99
		if kind == "stale-seq": snapshot.snapshot_seq = 4
		if kind == "stale-revision":
			snapshot.revision = 1
			snapshot.snapshot_seq = 6
		if kind == "room": snapshot.room_id = "other"
		if kind == "match": snapshot.match_id = "other"
		client._accept_text(Support.event("game.snapshot", snapshot), 0 if kind == "generation" else 1, client.clock_ms)
		value = sync_status(client)
		check(value.snapshot_age_ms == client.clock_ms - 250 and value.server_tick == 17 and value.snapshot_seq == 5, kind + " snapshot cannot refresh age or capture identity")
	client.clock_ms = 950
	client._accept_text(Support.event("room.state", Support.snapshot("rA", "mA", 3, 4)), 1, 950)
	value = sync_status(client)
	check(value.snapshot_age_ms == 700 and value.server_tick == 17 and value.snapshot_seq == 5, "accepted metadata preserves periodic capture clock and identity")
	client.clock_ms = 1000
	periodic(client, 1000, 6, 20, 3)
	value = sync_status(client)
	check(value.snapshot_age_ms == 0 and value.server_tick == 20 and value.snapshot_seq == 6, "new accepted periodic capture replaces all snapshot fields together")
	client.clock_ms = 1015
	check(sync_status(client).snapshot_age_ms == 15, "snapshot age advances on local monotonic clock without new messages")
	client._accept_text(Support.event("room.state", Support.snapshot("rA", "mA", 4, 7, ["sB"])), 1, 1015)
	check_unmeasured(client, "self removal")
	client.free()

func test_scope_reset_and_late_ack() -> void:
	for kind: String in ["leave", "epoch", "match", "reconnect"]:
		var client := Support.ready_client(true)
		measure(client)
		client.clock_ms = 210
		client.send_direction(Vector3.ZERO)
		var old_action := request(client)
		if kind == "leave":
			client.leave_room()
		elif kind == "epoch":
			client.state.clear_room()
			client.state.apply_admission(Support.snapshot("rA", "mA"), client.state.begin_admission())
			client._emit_snapshot()
		elif kind == "match":
			client._accept_text(Support.event("game.ended", {"match_id": "mA", "reason": "test", "server_tick": 11, "snapshot": Support.snapshot("rA", null, 3, 3)}), 1, 220)
			check_unmeasured(client, "ended match")
			client._accept_text(Support.event("game.started", {"snapshot": Support.snapshot("rA", "mB", 4, 4)}), 1, 225)
		else:
			client.disconnect_guest()
			client.state.accept_guest("sA", "default")
			client.state.apply_admission(Support.snapshot("rA", "mA"), client.state.begin_admission())
			client._emit_snapshot()
		check_unmeasured(client, kind + " reset")
		check(client.next_input_seq == (1 if kind == "reconnect" else 3), kind + " preserves socket input sequence lifetime")
		acknowledge(client, old_action, 240, "mB" if kind == "match" else "", -1, 1)
		check_unmeasured(client, kind + " late ACK")
		if kind != "leave":
			client.clock_ms = 250
			client.send_direction(Vector3.RIGHT)
			acknowledge(client, request(client), 263)
			check(sync_status(client).input_ack_rtt_ms == 13, kind + " accepts fresh current action after reset")
		client.free()

func test_shared_hud_small_window() -> void:
	var original_size := root.size
	root.size = Vector2i(700, 720)
	var main := Main.new()
	root.add_child(main)
	main.set_process(false)
	main.controller.set_process(false)
	main.client.state.game_id = "w".repeat(64)
	main._on_snapshot({"phase": "PLAYING", "room_number": "123456", "members": [1, 2, 3, 4, 5, 6]})
	for mode: String in ["3d", "2d"]:
		main._set_view_mode(mode)
		for frame: int in 4:
			await process_frame
		var label: Variant = main.get("_sync_status_label")
		check(label is Label and label.visible, mode + " exposes the shared sync HUD")
		if label is Label:
			check("입력 ACK RTT" in label.text and "Snapshot 경과" in label.text and "Tick" in label.text and "seq" in label.text and label.text.count("—") == 4 and not "0 ms" in label.text, mode + " HUD distinguishes unmeasured values from zero")
			check("왕복" in label.tooltip_text and "game.action" in label.tooltip_text and "화면" in label.tooltip_text, mode + " HUD explains input ACK round trip and its limits")
			var rect: Rect2 = label.get_global_rect()
			check(rect.position.x >= 0 and rect.end.x <= root.get_visible_rect().size.x + 0.5 and rect.end.y < 200, mode + " metrics fit 700px width and preserve gameplay area")
		check("w".repeat(64) in main._summary.tooltip_text, mode + " retains complete long-game tooltip")
		for item: Control in main.view_select.get_parent().get_children():
			var rect := item.get_global_rect()
			check(rect.position.x >= 0 and rect.end.x <= root.get_visible_rect().size.x + 0.5, mode + " HUD actions remain inside small viewport")
	main.free()
	root.size = original_size

func _run() -> void:
	MovementInput.ensure_actions()
	test_guest_initial_list()
	await test_close_guidance()
	test_ack_rtt_and_pending_bounds()
	test_invalid_ack_cannot_measure()
	test_snapshot_acceptance()
	test_scope_reset_and_late_ack()
	await test_shared_hud_small_window()
	print("sync-status checks=", checks, " failures=", failures)
	quit(0 if failures == 0 else 1)
