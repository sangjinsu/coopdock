extends SceneTree

var main: Main
var driver: TestDriver
var role: String
var prefix: String
var room_file: String
var failures: int = 0
var checks: int = 0
var proof: Dictionary = {}

func _initialize() -> void:
	_run.call_deferred()

func arg(name: String, fallback: String = "") -> String:
	var args := OS.get_cmdline_user_args()
	for index: int in args.size():
		if args[index].begins_with(name + "="):
			return args[index].substr(name.length() + 1)
		if args[index] == name and index + 1 < args.size():
			return args[index + 1]
	return fallback

func check(value: bool, label: String) -> void:
	checks += 1
	if not value:
		failures += 1
		printerr("FAIL native ", role, ": ", label)

func write_json(path: String, value: Variant) -> void:
	var file := FileAccess.open(path, FileAccess.WRITE)
	if file == null:
		check(false, "open output")
		return
	file.store_string(JSON.stringify(value))
	file.close()

func wait_file(path: String) -> bool:
	var deadline := Time.get_ticks_msec() + 20000
	while not FileAccess.file_exists(path) and Time.get_ticks_msec() < deadline:
		await process_frame
	check(FileAccess.file_exists(path), "bounded peer file " + path.get_file())
	return FileAccess.file_exists(path)

func key(pressed: bool) -> void:
	var event := InputEventKey.new()
	event.physical_keycode = KEY_D
	event.pressed = pressed
	Input.parse_input_event(event)

func processed(seq: int) -> Dictionary:
	var id := main.client.state.session_id
	return await driver.await_snapshot(func(s: Dictionary):
		return s.get("phase") == "PLAYING" and s.last_processed_input_seq.get(id, 0) >= seq)

func self_player(snapshot: Dictionary) -> Dictionary:
	for player: Dictionary in snapshot.game_state.players:
		if player.session_id == main.client.state.session_id:
			return player
	return {}

func movement_and_gates() -> void:
	var movement := MovementInput.new()
	main.controller.set_input_gate(true, false)
	key(true)
	await process_frame
	check(movement.sample(true, false) == Vector3.RIGHT, "physical Input.parse_input_event D")
	var move_seq := main.client.next_input_seq
	main.controller.submit_direction(movement.sample(true, false))
	var moving := await processed(move_seq)
	check(self_player(moving).velocity.x == 5, "physical event direction receives server movement")
	key(false)
	await process_frame
	var stop_seq := main.client.next_input_seq
	main.controller.submit_direction(movement.sample(true, false))
	var stopped := await processed(stop_seq)
	check(self_player(stopped).velocity.x == 0, "physical release receives server zero")
	proof["move_snapshot"] = moving
	proof["stop_snapshot"] = stopped
	key(true)
	await process_frame
	main.controller.submit_direction(movement.sample(true, false))
	await processed(main.client.next_input_seq - 1)
	main.lobby.room_number_edit.grab_focus()
	await process_frame
	check(main._text_focused(), "root Window gui focus detects LineEdit")
	var text_stop := main.client.next_input_seq
	main.controller.set_input_gate(true, main._text_focused())
	var text_snapshot := await processed(text_stop)
	check(self_player(text_snapshot).velocity.x == 0, "LineEdit focus sends zero")
	main.lobby.room_number_edit.release_focus()
	main.controller.set_input_gate(true, false)
	check(movement.sample(true, false) == Vector3.ZERO, "focus recovery discards old held direction")
	key(false)
	await process_frame
	key(true)
	await process_frame
	main.controller.submit_direction(movement.sample(true, false))
	await processed(main.client.next_input_seq - 1)
	var window_stop := main.client.next_input_seq
	root.focus_exited.emit()
	var window_snapshot := await processed(window_stop)
	check(self_player(window_snapshot).velocity.x == 0, "Window focus_exited signal invokes stop gate")
	root.focus_entered.emit()
	check(movement.sample(true, false) == Vector3.ZERO, "Window focus recovery requires fresh key")
	key(false)
	await process_frame
	proof["input_kind"] = "synthetic engine physical InputEventKey; explicit Window focus signal; not manual OS keyboard/focus"
	proof["os_focus_observed"] = root.has_focus()
	# Exercise Main's real root-Window polling path independently from emitted signals.
	main._process(0.0)
	check(main.controller._focused == root.has_focus(), "Main polls actual root Window has_focus")
	check(main.controller._text_focused == main._text_focused(), "Main polls actual root Window text focus owner")
	proof["root_window_polling_verified"] = true

func save_png(path: String) -> void:
	await process_frame
	await RenderingServer.frame_post_draw
	var image := root.get_texture().get_image()
	check(not image.is_empty(), "native viewport has pixels")
	check(image.save_png(path) == OK, "native PNG saved after frame_post_draw")
	print("PNG ", path)

func _run() -> void:
	role = arg("--mode", "host")
	prefix = arg("--output-prefix")
	room_file = arg("--room-file")
	main = Main.new()
	root.add_child(main)
	main.set_process(false)
	main.controller.set_process(false)
	driver = TestDriver.new(main.client)
	check(main.client.connect_guest(arg("--url"), "default") == OK, "native connects default namespace")
	await driver.await_guest()
	if role == "host":
		var made := await driver.await_reply(main.client.create_room(2, "private", " abcd "))
		check(made.error.is_empty(), "native private capacity two created")
		write_json(room_file, {"number": made.payload.snapshot.room_number})
		await driver.await_snapshot(func(s: Dictionary): return s.members.size() == 2)
		await driver.await_reply(main.client.start_room())
	else:
		if not await wait_file(room_file):
			quit(1)
			return
		var room: Dictionary = JSON.parse_string(FileAccess.get_file_as_string(room_file))
		check((await driver.await_reply(main.client.join_room(room.number, " abcd "))).error.is_empty(), "native correct private password admitted")
	await driver.await_snapshot(func(s: Dictionary): return s.get("phase") == "PLAYING")
	# Keep an actual visible text widget available while the compact lobby shows gameplay.
	var focus_edit := main.lobby.room_number_edit
	focus_edit.reparent(main.get_node("HUD"))
	focus_edit.position = Vector2(16, 90)
	focus_edit.size = Vector2(190, 36)
	focus_edit.placeholder_text = "입력 focus QA"
	if role == "host":
		await movement_and_gates()
		write_json(room_file + ".host-input", {"done": true})
		await wait_file(room_file + ".peer-input")
	else:
		await wait_file(room_file + ".host-input")
		await movement_and_gates()
		write_json(room_file + ".peer-input", {"done": true})
	await process_frame
	check(main.world.avatars.size() == 2, "two independent native avatar views")
	proof["self_id"] = main.client.state.session_id
	proof["snapshot"] = main.client.state.current_snapshot.duplicate(true)
	proof["render_positions"] = {}
	for id: String in main.world.avatars:
		var p: Vector3 = main.world.avatars[id].position
		proof.render_positions[id] = {"x": p.x, "y": p.y, "z": p.z}
	await save_png(prefix + ".png")
	write_json(prefix + "-captures.json", driver.captures)
	write_json(room_file + "." + role + "-captured", {"done": true})
	if role == "host":
		await wait_file(room_file + ".peer-captured")
		await wait_file(room_file + ".peer-left")
		await driver.await_snapshot(func(s: Dictionary): return s.get("phase") == "PLAYING" and s.members.size() == 1)
		await process_frame
		check(main.world.avatars.size() == 1, "departed avatar disappears in native world")
		await save_png(prefix + "-removed.png")
		await driver.await_reply(main.client.leave_room())
	else:
		await wait_file(room_file + ".host-captured")
		await driver.await_reply(main.client.leave_room())
		write_json(room_file + ".peer-left", {"done": true})
	proof["checks"] = checks
	proof["failures"] = failures
	write_json(prefix + "-report.json", proof)
	main.client.disconnect_guest()
	main.queue_free()
	await process_frame
	print("native ", role, " checks=", checks, " failures=", failures)
	quit(0 if failures == 0 else 1)
