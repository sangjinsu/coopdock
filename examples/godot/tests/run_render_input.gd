extends "res://tests/run_render_qa.gd"

const Support = preload("res://tests/test_support.gd")
var fake: Support.FakeClient
var stop_ack_released: bool = false
var processed_calls: int = 0

func release_delayed_stop() -> void:
	await process_frame
	await process_frame
	check(fake.sent.size() == 4 and fake.next_input_seq == 5, "native input waits without sending D before delayed stop ACK")
	Support.acknowledge_last(fake)
	stop_ack_released = true
	check(not main.controller._pending_stop and fake.sent.size() == 4, "stop ACK clears barrier without replaying an earlier physical sample")

func processed(seq: int) -> Dictionary:
	processed_calls += 1
	if processed_calls > 1:
		# Simulate a failed snapshot wait at the harness boundary. The real driver
		# reports its timeout; the caller must return without dereferencing {}.
		return {}
	check(stop_ack_released, "native movement waits for the preceding stop confirmation")
	check(seq == 5 and fake.next_input_seq == 6 and fake.sent.size() == 5, "native movement awaits the actual freshly transmitted sequence five")
	var latest: Dictionary = JSON.parse_string(fake.sent.back())
	check(latest.payload.input_seq == 5 and latest.payload.action.direction == {"x": 1.0, "y": 0.0, "z": 0.0}, "fresh physical D reaches native wire path after stop ACK")
	if failures:
		# End the original broken path before its next wait. This makes RED an
		# assertion failure about an unsent seq, rather than a parse or timeout error.
		finish.call_deferred()
	else:
		Support.acknowledge_last(fake)
	var snapshot := Support.snapshot("rA", "mA", 2, 2)
	snapshot.last_processed_input_seq.sA = seq if failures == 0 else 4
	snapshot.game_state.players[0].velocity.x = 5 if failures == 0 else 0
	return snapshot

func finish() -> void:
	key(false)
	main.free()
	fake.free()
	print("render-input checks=", checks, " failures=", failures)
	quit(0 if failures == 0 else 1)

func _run() -> void:
	role = "headless-input-regression"
	MovementInput.ensure_actions()
	main = Main.new()
	root.add_child(main)
	main.set_process(false)
	main.controller.set_process(false)
	main.client.set_process(false)
	fake = Support.ready_client(true)
	root.add_child(fake)
	fake.set_process(false)
	main.client = fake
	main.controller.bind_client(fake)
	driver = TestDriver.new(fake)
	# Let the initial root-Window focus notification settle before injecting keys.
	await process_frame
	await process_frame
	main.controller.set_input_gate(true, false)
	# Match the peer's real mixed-native prelude: mode stop, vertical seeding,
	# explicit zero, then a 2D mode stop whose ACK has not arrived yet.
	main.controller.view_mode = "2d"
	main.controller.set_view_mode("3d")
	Support.acknowledge_last(fake)
	fake.send_direction(Vector3.UP)
	Support.acknowledge_last(fake)
	fake.send_direction(Vector3.ZERO)
	Support.acknowledge_last(fake)
	main.controller.set_view_mode("2d")
	check(fake.sent.size() == 4 and fake.next_input_seq == 5 and main.controller._pending_stop, "mixed-native fixture has pending zero sequence four")
	release_delayed_stop.call_deferred()
	var result: Variant = await movement_and_gates()
	check(processed_calls == 2 and result == false, "native harness fails fast on an empty processed snapshot")
	finish()
