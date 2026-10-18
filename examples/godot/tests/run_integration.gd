extends SceneTree

var checks: int = 0
var failures: int = 0
var drivers: Array[TestDriver] = []
var url: String = ""

func _initialize() -> void:
	_run.call_deferred()

func check(value: bool, message: String) -> void:
	checks += 1
	if not value:
		failures += 1
		printerr("FAIL: ", message)

func argument(name: String, fallback: String = "") -> String:
	var args := OS.get_cmdline_user_args()
	for index: int in args.size():
		if args[index].begins_with(name + "="):
			return args[index].substr(name.length() + 1)
		if args[index] == name and index + 1 < args.size():
			return args[index + 1]
	return fallback

func _run() -> void:
	url = argument("--url", "ws://127.0.0.1:8080/ws")
	if "--heartbeat-only" in OS.get_cmdline_user_args():
		await test_heartbeat()
	else:
		await test_private_entry()
		await test_six_clients()
	for driver: TestDriver in drivers:
		driver.close()
	await process_frame
	print("integration checks=", checks, " failures=", failures)
	quit(0 if failures == 0 else 1)

func new_driver() -> TestDriver:
	var client := CoopClient.new()
	root.add_child(client)
	var driver := TestDriver.new(client)
	drivers.append(driver)
	check(client.connect_guest(url, "default") == OK, "connect namespace default")
	check(not (await driver.await_guest()).is_empty(), "real guest received")
	check(client.state.game_id == "default", "real immutable namespace binding")
	return driver

func test_private_entry() -> void:
	var host := await new_driver()
	var entrant := await new_driver()
	var made := await host.await_reply(host.client.create_room(1, "private", " abcd "))
	check(made.error.is_empty(), "private room created")
	var number: String = made.payload.snapshot.room_number
	var missing := await entrant.await_reply(entrant.client.join_room(number))
	check(missing.error.get("code") == "PASSWORD_REQUIRED", "private missing password")
	var wrong := await entrant.await_reply(entrant.client.join_room(number, "abcd"))
	check(wrong.error.get("code") == "PASSWORD_INVALID", "password whitespace preserved")
	var full := await entrant.await_reply(entrant.client.join_room(number, " abcd "))
	check(full.error.get("code") == "ROOM_FULL", "correct password capacity one full")
	var listing := await entrant.await_reply(entrant.client.list_rooms())
	for room: Dictionary in listing.payload.rooms:
		check(room.room_number != number, "private room hidden")
	check((await host.await_reply(host.client.start_room())).error.is_empty(), "private start")
	var playing := await entrant.await_reply(entrant.client.join_room(number, " abcd "))
	check(playing.error.get("code") == "ROOM_IN_PROGRESS", "private playing rejects entry")
	await host.await_reply(host.client.leave_room())
	made = await host.await_reply(host.client.create_room(2, "private", " abcd "))
	var admitted := await entrant.await_reply(entrant.client.join_room(made.payload.snapshot.room_number, " abcd "))
	check(admitted.error.is_empty() and admitted.payload.snapshot.members.size() == 2, "correct password capacity two succeeds")
	await entrant.await_reply(entrant.client.leave_room())
	await host.await_reply(host.client.leave_room())

func common_capture(group: Array, predicate: Callable) -> Dictionary:
	var deadline := Time.get_ticks_msec() + 10000
	var first: TestDriver = group[0]
	while Time.get_ticks_msec() < deadline:
		for key: String in first.capture_order:
			var snapshot: Dictionary = first.captures[key]
			if not predicate.call(snapshot):
				continue
			var states: Array = [snapshot]
			for driver: TestDriver in group.slice(1):
				if driver.captures.has(key):
					states.append(driver.captures[key])
			if states.size() == group.size():
				check(TestDriver.same_capture(states), "common identity full snapshot deep equality")
				return snapshot
		await process_frame
	check(false, "bounded common capture timeout")
	return {}

func player(snapshot: Dictionary, id: String) -> Dictionary:
	for value: Dictionary in snapshot.get("game_state", {}).get("players", []):
		if value.session_id == id:
			return value
	return {}

func require_common_capture(group: Array, self_id: String, min_seq: int) -> Dictionary:
	return await common_capture(group, func(s: Dictionary):
		return s.get("phase") == "PLAYING" and s.last_processed_input_seq.get(self_id, 0) >= min_seq)

func test_six_clients() -> void:
	var group: Array[TestDriver] = []
	for index: int in 7:
		group.append(await new_driver())
	var made := await group[0].await_reply(group[0].client.create_room(6, "public"))
	check(made.error.is_empty(), "public capacity six created")
	var number: String = made.payload.snapshot.room_number
	for index: int in range(1, 6):
		check((await group[index].await_reply(group[index].client.join_room(number))).error.is_empty(), "six real Godot clients admitted")
	var rejected := await group[6].await_reply(group[6].client.join_room(number))
	check(rejected.error.get("code") == "ROOM_FULL", "seventh real Godot client rejected")
	var started := await group[0].await_reply(group[0].client.start_room())
	var match_id: String = started.payload.snapshot.match_id
	for index: int in 6:
		await group[index].await_snapshot(func(s: Dictionary): return s.get("match_id") == match_id)
	var self_id: String = group[0].client.state.session_id
	var spawn: Dictionary = player(started.payload.snapshot, self_id).position
	var moved := await group[0].await_reply(group[0].client.send_direction(Vector3(0, 0, 1)))
	check(moved.error.is_empty() and moved.payload.input_seq == 1, "move ACK seq one")
	check(not moved.payload.has("position"), "ACK carries no rendered position")
	var moving := await common_capture(group.slice(0, 6), func(s: Dictionary):
		if s.get("phase") != "PLAYING":
			return false
		var p := player(s, self_id)
		return not p.is_empty() and s.last_processed_input_seq.get(self_id, 0) >= 1 and p.position.z > spawn.z)
	if moving.is_empty():
		return
	var moving_player := player(moving, self_id)
	check(moving.server_tick >= moved.payload.server_tick, "authoritative capture follows move ACK tick")
	check(moving_player.velocity == {"x": 0.0, "y": 0.0, "z": 5.0}, "server move velocity z five")
	check(moving_player.position.x == spawn.x and moving_player.position.y == spawn.y, "move preserves x y")
	var stopped := await group[0].await_reply(group[0].client.send_direction(Vector3.ZERO))
	check(stopped.error.is_empty() and stopped.payload.input_seq == 2, "stop ACK seq two")
	var stop := await require_common_capture(group.slice(0, 6), self_id, 2)
	if stop.is_empty():
		return
	var stopped_player := player(stop, self_id)
	check(stopped_player.velocity == {"x": 0.0, "y": 0.0, "z": 0.0}, "authoritative stop zero velocity")
	var previous: Dictionary = stop
	for index: int in 2:
		var after := await common_capture(group.slice(0, 6), func(s: Dictionary):
			return s.get("match_id") == match_id and s.server_tick > previous.server_tick and s.snapshot_seq > previous.snapshot_seq)
		check(player(after, self_id).get("position") == stopped_player.position, "two subsequent common captures hold stopped position")
		previous = after
	await group[0].await_reply(group[0].client.leave_room())
	var promoted_id: String = group[1].client.state.session_id
	var promoted := await group[1].await_snapshot(func(s: Dictionary): return s.host_id == promoted_id and s.members.size() == 5)
	check(player(promoted, self_id).is_empty(), "leaving host removed from authoritative players")
	check(group[0].client.state.current_snapshot.is_empty(), "same socket leave clears membership")
	var closed_id: String = group[5].client.state.session_id
	group[5].client.disconnect_guest()
	var removed := await group[1].await_snapshot(func(s: Dictionary):
		return s.get("match_id") == match_id and s.members.size() == 4)
	check(player(removed, closed_id).is_empty(), "PLAYING socket close removes member and player")
	var next := await group[0].await_reply(group[0].client.create_room(2, "public"))
	var next_number: String = next.payload.snapshot.room_number
	await group[6].await_reply(group[6].client.join_room(next_number))
	await group[6].await_reply(group[6].client.leave_room())
	check((await group[6].await_reply(group[6].client.join_room(next_number))).error.is_empty(), "same socket leave and rejoin succeeds")
	await group[6].await_reply(group[6].client.leave_room())
	await group[0].await_reply(group[0].client.start_room())
	var again := await group[0].await_reply(group[0].client.send_direction(Vector3.ZERO))
	check(again.payload.input_seq == 3, "same socket new room and match preserves sequence three")
	await group[0].await_reply(group[0].client.leave_room())
	group[0].client.disconnect_guest()
	check(group[0].client.connect_guest(url, "default") == OK, "reconnect default")
	var fresh_id := await group[0].await_guest()
	check(fresh_id != self_id, "reconnect receives fresh session")
	await group[0].await_reply(group[0].client.create_room(1, "public"))
	await group[0].await_reply(group[0].client.start_room())
	var fresh := await group[0].await_reply(group[0].client.send_direction(Vector3.ZERO))
	check(fresh.payload.input_seq == 1, "fresh socket sequence resets to one")
	await group[0].await_reply(group[0].client.leave_room())
	for index: int in range(1, 5):
		await group[index].await_reply(group[index].client.leave_room())
	print("six-client evidence match=", match_id, " moving=", moving_player, " stopped=", stopped_player, " host_migration=", promoted_id)

func test_heartbeat() -> void:
	var driver := await new_driver()
	var original_id := driver.client.state.session_id
	driver.client.heartbeat_interval_ms = 1000
	var replies := [0]
	driver.client.request_completed.connect(func(op: String, _id: String, _payload: Dictionary, error: Dictionary):
		if op == "room.list" and error.is_empty():
			replies[0] += 1)
	var deadline := Time.get_ticks_msec() + 6000
	while Time.get_ticks_msec() < deadline:
		await process_frame
	check(driver.client.state.session_id == original_id, "3s read timeout server retains original guest after six seconds")
	check(replies[0] >= 4, "heartbeat real room.list receives at least four replies")
	print("heartbeat evidence replies=", replies[0], " same_guest=", driver.client.state.session_id == original_id)
