extends SceneTree

const Support = preload("res://tests/test_support.gd")
var checks: int = 0
var failures: int = 0

func check(condition: bool, label: String) -> void:
	checks += 1
	if not condition:
		failures += 1
		print("FAIL: ", label)

func _initialize() -> void:
	call_deferred("run")

func snapshot(game: String = "default", playing: bool = false) -> Dictionary:
	var value: Dictionary = Support.snapshot("rA", "mA" if playing else null)
	value["game_id"] = game
	return value

func accept(state: RoomState, game: String = "default") -> void:
	state.accept_guest("sA", game)

func connect_to(client: CoopClient, url: String, game: String = "default") -> Error:
	return client.connect_guest(url, game)

func ready(game: String = "default") -> Support.FakeClient:
	var client := Support.FakeClient.new()
	connect_to(client, "ws://127.0.0.1:1/ws", game)
	client._accept_text(Support.event("session.guest", {"session_id": "sA", "game_id": game}), client.state.connection_generation, 1)
	return client

func protocol_case(op: String, payload: Dictionary, request_op: String = "") -> void:
	var client := ready()
	var failures_seen: Array = []
	var successes: Array = []
	client.request_failed.connect(func(_op: String, code: String, _message: String) -> void: failures_seen.append(code))
	client.rooms_received.connect(func(rooms: Array) -> void: successes.append(rooms))
	client.request_completed.connect(func(_op: String, _id: String, _payload: Dictionary, error: Dictionary) -> void: successes.append(error))
	var id: Variant = null
	if request_op == "room.list":
		id = client.list_rooms()
	elif request_op == "room.create":
		id = client.create_room(6, "public")
	elif request_op == "room.start":
		client.state.apply_admission(snapshot(), client.state.begin_admission())
		id = client.start_room()
	else:
		client.state.apply_admission(snapshot(), client.state.begin_admission())
	client._accept_text(Support.event(op, payload, id), client.state.connection_generation, 2)
	check(client.status == "disconnected" and client.state.session_id.is_empty() and client.state.current_snapshot.is_empty(), op + " foreign binding closes and clears")
	check(failures_seen == ["CLIENT_PROTOCOL"] and successes.is_empty(), op + " emits protocol error without success")
	client.free()

func run() -> void:
	for url: String in ["ws://127.0.0.1:1/ws?game_id=other", "ws://127.0.0.1:1/ws?", "ws://127.0.0.1:1/ws#fragment", "ws://127.0.0.1:1/ws#", "http://127.0.0.1:1/ws", "ws://"]:
		var client := CoopClient.new()
		check(connect_to(client, url) == ERR_INVALID_PARAMETER and client.status == "disconnected", "invalid base rejected: " + url)
		client.free()
	for game: String in ["", " A", "game-a ", "A", "한글", "-a", "_a", "a/b", "a?b", "a.b", "a".repeat(65)]:
		var client := CoopClient.new()
		check(connect_to(client, "ws://127.0.0.1:1/ws", game) == ERR_INVALID_PARAMETER and client.status == "disconnected", "invalid game rejected without trimming: " + game)
		client.free()
	check(not WireCodec.decode_server(Support.event("session.guest", {"session_id": "sA"})).ok, "old guest missing game rejected")
	var old: Dictionary = Support.snapshot()
	old.erase("game_id")
	check(not WireCodec.valid_snapshot(old), "old snapshot missing game rejected")
	for game: String in ["default", "a", "0", "game-a_b", "a".repeat(64)]:
		check(WireCodec.decode_server(Support.event("session.guest", {"session_id": "sA", "game_id": game})).ok, "valid guest game accepted: " + game)
		check(WireCodec.valid_snapshot(snapshot(game)), "valid snapshot game accepted: " + game)
	var client := CoopClient.new()
	check(connect_to(client, "ws://127.0.0.1:1/ws", "game-a") == OK, "named game starts connection")
	var codes: Array = []
	client.request_failed.connect(func(_op: String, code: String, _message: String) -> void: codes.append(code))
	client._accept_text(Support.event("session.guest", {"session_id": "sA", "game_id": "game-b"}), client.state.connection_generation, 1)
	check(client.status == "disconnected" and codes == ["CLIENT_PROTOCOL"], "foreign guest never ready")
	client.free()
	for op: String in ["room.state", "game.snapshot"]:
		protocol_case(op, snapshot("game-b", op == "game.snapshot"))
	for op: String in ["game.started", "room.create", "room.start"]:
		protocol_case(op, {"snapshot": snapshot("game-b", op != "room.create")}, op if op.begins_with("room.") else "")
	protocol_case("game.ended", {"match_id": "mA", "reason": "finished", "server_tick": 8, "snapshot": snapshot("game-b")})
	protocol_case("room.list", {"rooms": [snapshot(), snapshot("game-b")]}, "room.list")
	var state := RoomState.new()
	state.new_connection(1)
	accept(state)
	check(state.apply_admission(snapshot(), state.begin_admission()), "same game admission accepted")
	state.clear_room()
	check(state.get("game_id") == "default" and state.session_id == "sA", "leave keeps game and guest")
	check(not state.apply_admission(snapshot("game-b"), state.begin_admission()), "state rejects cross game admission")
	accept(state, "game-b")
	check(state.get("game_id") == "default", "guest binding immutable")
	state.new_connection(2)
	check(state.get("game_id") == "" and state.session_id.is_empty(), "new connection clears game")
	accept(state, "game-b")
	check(state.apply_admission(snapshot("game-b"), state.begin_admission()), "reconnect permits new game")
	client = ready("game-a")
	check(client.status == "guest" and client.state.get("game_id") == "game-a", "matching guest ready")
	client.disconnect_guest()
	check(client.state.get("game_id") == "", "disconnect clears binding")
	client.free()
	var lobby := LobbyPanel.new()
	root.add_child(lobby)
	await process_frame
	var game_edit: Variant = lobby.get("game_id_edit")
	check(game_edit is LineEdit and game_edit.text == "default" and game_edit.editable, "lobby default game input editable")
	client = ready("game-a")
	lobby.bind_client(client)
	check(not lobby.game_id_edit.editable and not lobby.url_edit.editable and lobby.game_label.text == "연결 게임: game-a", "guest widgets lock and display bound game")
	client.state.apply_admission(snapshot("game-a"), client.state.begin_admission())
	client._emit_snapshot()
	check(client.status == "waiting" and not lobby.game_id_edit.editable and not lobby.url_edit.editable and lobby.game_label.text == "연결 게임: game-a", "waiting widgets lock and display bound game")
	var playing_snapshot: Dictionary = snapshot("game-a", true)
	playing_snapshot.revision = 2
	playing_snapshot.snapshot_seq = 2
	client.state.apply_event("game.started", {"snapshot": playing_snapshot})
	client._emit_snapshot()
	check(client.status == "playing" and not lobby.game_id_edit.editable and not lobby.url_edit.editable and lobby.game_label.text == "연결 게임: game-a", "playing widgets lock and display bound game")
	client.disconnect_guest()
	check(lobby.game_id_edit.editable and lobby.url_edit.editable and client.state.game_id.is_empty() and lobby.game_label.text == "연결 게임: 없음", "disconnect unlocks widgets and clears displayed binding")
	lobby.bind_client(null)
	lobby.free()
	client.free()
	print("GAME_SCOPE_UNIT checks=%d failures=%d" % [checks, failures])
	quit(1 if failures else 0)
