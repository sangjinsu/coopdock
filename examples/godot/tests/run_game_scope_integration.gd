extends SceneTree

const WAIT_MS: int = 5000
const MAX_REPLIES: int = 64
var endpoint: String = "ws://127.0.0.1:18080/ws"
var game_a: String = "game-a"
var game_b: String = "game-b"
var clients: Array[CoopClient] = []
var replies: Dictionary = {}
var expected_games: Dictionary = {}
var checks: int = 0
var failures: int = 0
var finished: bool = false
var deadline_ms: int = 0

func _initialize() -> void:
	for argument: String in OS.get_cmdline_user_args():
		if argument.begins_with("--url="):
			endpoint = argument.substr(6)
		elif argument.begins_with("--game-a="):
			game_a = argument.substr(9)
		elif argument.begins_with("--game-b="):
			game_b = argument.substr(9)
		else:
			print("FAIL: unknown argument ", argument)
			quit(1)
			return
	deadline_ms = Time.get_ticks_msec() + 30000
	call_deferred("run")

func _process(_delta: float) -> bool:
	if not finished and Time.get_ticks_msec() >= deadline_ms:
		fail("integration global deadline exceeded")
	return false

func fail(label: String) -> void:
	if finished:
		return
	failures += 1
	print("FAIL: ", label)
	finish()

func check(condition: bool, label: String) -> bool:
	checks += 1
	if not condition:
		fail(label)
	return condition

func finish() -> void:
	if finished:
		return
	finished = true
	for client: CoopClient in clients:
		client.disconnect_guest()
		client.queue_free()
	print("GAME_SCOPE_INTEGRATION checks=%d failures=%d url=%s games=%s,%s" % [checks, failures, endpoint, game_a, game_b])
	quit(1 if failures else 0)

func make_client(game: String) -> CoopClient:
	var client := CoopClient.new()
	clients.append(client)
	replies[client] = {}
	expected_games[client] = game
	client.request_completed.connect(func(_op: String, id: String, payload: Dictionary, error: Dictionary) -> void:
		if replies[client].size() >= MAX_REPLIES:
			fail("bounded reply queue overflow")
			return
		replies[client][id] = {"payload": payload.duplicate(true), "error": error.duplicate(true)})
	client.request_failed.connect(func(op: String, code: String, message: String) -> void:
		if code != "ROOM_NOT_FOUND":
			fail("unexpected request failure %s %s %s" % [op, code, message]))
	client.snapshot_changed.connect(func(snapshot: Dictionary) -> void:
		if not snapshot.is_empty() and snapshot.game_id != expected_games[client]:
			fail("foreign game snapshot reached consumer"))
	root.add_child(client)
	if not check(client.connect_guest(endpoint, game) == OK, "connection begins: " + game):
		return client
	return client

func wait_until(condition: Callable, label: String) -> bool:
	var until_ms: int = Time.get_ticks_msec() + WAIT_MS
	while not finished and not condition.call():
		if Time.get_ticks_msec() >= until_ms:
			fail("timeout: " + label)
			return false
		await process_frame
	return not finished and check(bool(condition.call()), label)

func reply(client: CoopClient, id: String, label: String, expected_error: String = "") -> Dictionary:
	if not check(not id.is_empty(), label + " request sent"):
		return {}
	if not await wait_until(func() -> bool: return replies[client].has(id), label + " reply"):
		return {}
	var value: Dictionary = replies[client][id]
	replies[client].erase(id)
	if not check(value.error.get("code", "") == expected_error, label + " expected result " + expected_error):
		return {}
	return value

func listed(client: CoopClient, room_id: String, label: String) -> bool:
	var value: Dictionary = await reply(client, client.list_rooms(), label)
	if finished:
		return false
	var rooms: Array = value.payload.rooms
	return check(rooms.size() == 1 and rooms[0].room_id == room_id and rooms[0].game_id == expected_games[client], label + " exact own game room")

func player(client: CoopClient, id: String) -> Dictionary:
	for value: Dictionary in client.state.current_snapshot.get("game_state", {}).get("players", []):
		if value.session_id == id:
			return value
	return {}

func motion_observed(client: CoopClient, id: String, sequence: int, moving: bool) -> bool:
	var snapshot: Dictionary = client.state.current_snapshot
	if snapshot.get("phase") != "PLAYING" or int(snapshot.last_processed_input_seq.get(id, 0)) < sequence:
		return false
	var value: Dictionary = player(client, id)
	return not value.is_empty() and (absf(float(value.velocity.x)) > 0.01 if moving else absf(float(value.velocity.x)) < 0.001)

func move_and_stop(host: CoopClient, peer: CoopClient, label: String) -> bool:
	var sequence: int = host.next_input_seq
	var moved: Dictionary = await reply(host, host.send_direction(Vector3.RIGHT), label + " move")
	if finished:
		return false
	if not check(int(moved.payload.input_seq) == sequence and moved.payload.match_id == host.state.current_snapshot.match_id, label + " move ACK identity"):
		return false
	if not await wait_until(func() -> bool: return motion_observed(host, host.state.session_id, sequence, true) and motion_observed(peer, host.state.session_id, sequence, true), label + " move ACK reaches both snapshots"):
		return false
	if not check(float(player(peer, host.state.session_id).position.x) > 0.0, label + " authoritative position moved"):
		return false
	sequence = host.next_input_seq
	var stopped: Dictionary = await reply(host, host.send_direction(Vector3.ZERO), label + " stop")
	if finished:
		return false
	if not check(int(stopped.payload.input_seq) == sequence, label + " stop ACK identity"):
		return false
	if not await wait_until(func() -> bool: return motion_observed(host, host.state.session_id, sequence, false) and motion_observed(peer, host.state.session_id, sequence, false), label + " stop ACK reaches both snapshots"):
		return false
	var position: Dictionary = player(peer, host.state.session_id).position.duplicate(true)
	var tick: int = int(peer.state.current_snapshot.server_tick)
	if not await wait_until(func() -> bool: return int(peer.state.current_snapshot.server_tick) >= tick + 3, label + " later periodic stop snapshot"):
		return false
	return check(player(peer, host.state.session_id).position == position, label + " position stays stopped")

func run() -> void:
	if not check(game_a != game_b and WireCodec.valid_game_id(game_a) and WireCodec.valid_game_id(game_b), "distinct valid games"):
		return
	var ah: CoopClient = make_client(game_a)
	var ap: CoopClient = make_client(game_a)
	var bh: CoopClient = make_client(game_b)
	var bp: CoopClient = make_client(game_b)
	if not await wait_until(func() -> bool: return clients.all(func(client: CoopClient) -> bool: return client.status == "guest"), "four actual socket guests"):
		return
	for client: CoopClient in clients:
		if not check(client.state.game_id == expected_games[client] and not client.state.session_id.is_empty(), "known game ID query accepted by real server"):
			return
	await reply(ah, ah.create_room(6, "public"), "A create")
	if finished:
		return
	var a_room: Dictionary = ah.state.current_snapshot.duplicate(true)
	var empty_b: Dictionary = await reply(bp, bp.list_rooms(), "B list before own create")
	if finished or not check(empty_b.payload.rooms.is_empty(), "B list hides A room"):
		return
	await reply(bp, bp.join_room(a_room.room_number), "B cannot join A", "ROOM_NOT_FOUND")
	if finished:
		return
	await reply(bh, bh.create_room(6, "public"), "B create")
	if finished:
		return
	# Avoid an accidental same-number collision when checking a foreign-only number.
	for attempt: int in 3:
		if bh.state.current_snapshot.room_number != a_room.room_number:
			break
		await reply(bh, bh.leave_room(), "B replace colliding room leave")
		await reply(bh, bh.create_room(6, "public"), "B replace colliding room create")
		if finished:
			return
	var b_room: Dictionary = bh.state.current_snapshot.duplicate(true)
	if not check(a_room.room_number != b_room.room_number, "foreign-only room numbers available"):
		return
	await reply(ap, ap.join_room(b_room.room_number), "A cannot join B", "ROOM_NOT_FOUND")
	if finished or not await listed(ap, a_room.room_id, "A list") or not await listed(bp, b_room.room_id, "B list"):
		return
	await reply(ap, ap.join_room(a_room.room_number), "A same game join")
	await reply(bp, bp.join_room(b_room.room_number), "B same game join")
	if finished:
		return
	await reply(ah, ah.start_room(), "A start")
	await reply(bh, bh.start_room(), "B start")
	if not await wait_until(func() -> bool: return clients.all(func(client: CoopClient) -> bool: return client.status == "playing"), "all four receive start snapshots"):
		return
	if not await move_and_stop(ah, ap, "A") or not await move_and_stop(bh, bp, "B"):
		return
	var b_before: Dictionary = bh.state.current_snapshot.duplicate(true)
	await reply(ah, ah.leave_room(), "A host leave")
	if finished or not check(ah.state.game_id == game_a and ah.state.current_snapshot.is_empty(), "A leave keeps connection game"):
		return
	if not await wait_until(func() -> bool: return ap.state.current_snapshot.get("host_id") == ap.state.session_id, "A peer host succession"):
		return
	if not await wait_until(func() -> bool: return int(bp.state.current_snapshot.server_tick) > int(b_before.server_tick) + 3, "B continues after A leave"):
		return
	for client: CoopClient in [bh, bp]:
		var value: Dictionary = client.state.current_snapshot
		if not check(value.room_id == b_before.room_id and value.game_id == game_b and value.match_id == b_before.match_id and value.phase == "PLAYING" and value.host_id == b_before.host_id and value.members == b_before.members and value.game_state == b_before.game_state, "B membership match and stopped positions unchanged"):
			return
	ah.disconnect_guest()
	if not check(ah.state.game_id.is_empty(), "disconnect clears game before reconnect"):
		return
	expected_games[ah] = game_b
	if not check(ah.connect_guest(endpoint, game_b) == OK, "reconnect selects B"):
		return
	if not await wait_until(func() -> bool: return ah.status == "guest" and ah.state.game_id == game_b, "new B guest established"):
		return
	if not await listed(ah, b_room.room_id, "reconnected A client lists B"):
		return
	print("PROOF: four real CoopClient WebSocketPeer guests, list/join isolation, two-game start/move/stop ACK and snapshots, A leave/B stable, reconnect game change")
	finish()
