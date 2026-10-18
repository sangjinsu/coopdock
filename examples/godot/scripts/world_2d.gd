extends Node2D
class_name GameWorld2D

var positions: Dictionary = {}
var buffer := SnapshotBuffer.new()
var pixel_scale: float = 32.0
var labels: Dictionary = {}
var _client: CoopClient
var _host_id: String = ""

static func project_position(server_position: Vector3) -> Vector2:
	return Vector2(server_position.x, server_position.z)

func _ready() -> void:
	var theme := Theme.new()
	var font := SystemFont.new()
	font.font_names = PackedStringArray(["Apple SD Gothic Neo", "Malgun Gothic", "Noto Sans CJK KR", "sans-serif"])
	theme.default_font = font
	theme.default_font_size = 13
	# Label children use a shared font so Korean role names remain readable.
	set_meta("label_theme", theme)

func bind_client(client: CoopClient) -> void:
	if _client != null and _client.snapshot_changed.is_connected(_on_snapshot):
		_client.snapshot_changed.disconnect(_on_snapshot)
	_client = client
	client.snapshot_changed.connect(_on_snapshot)
	reset_timeline(client.state.current_snapshot, Time.get_ticks_msec())

func reset_timeline(snapshot: Dictionary, received_ms: int) -> void:
	buffer.clear()
	_apply_snapshot(snapshot, received_ms)

func _on_snapshot(snapshot: Dictionary) -> void:
	_apply_snapshot(snapshot, Time.get_ticks_msec())

func _apply_snapshot(snapshot: Dictionary, received_ms: int) -> void:
	var present: Dictionary = {}
	if snapshot.get("phase") == "PLAYING":
		var members: Dictionary = {}
		for member: Dictionary in snapshot.members:
			members[member.session_id] = true
		for player: Dictionary in snapshot.game_state.players:
			if members.has(player.session_id):
				present[player.session_id] = true
	for id: String in positions.keys():
		if not present.has(id):
			positions.erase(id)
			labels[id].queue_free()
			labels.erase(id)
	if snapshot.get("phase") != "PLAYING":
		buffer.clear()
		queue_redraw()
		return
	_host_id = snapshot.host_id
	buffer.retain_members(present)
	buffer.push_snapshot(snapshot, received_ms)
	for id: String in present:
		if not labels.has(id):
			var label := Label.new()
			label.theme = get_meta("label_theme")
			label.mouse_filter = Control.MOUSE_FILTER_IGNORE
			label.add_theme_color_override("font_shadow_color", Color.BLACK)
			label.add_theme_constant_override("shadow_offset_x", 1)
			label.add_theme_constant_override("shadow_offset_y", 1)
			add_child(label)
			labels[id] = label
		var role := "나" if id == _client.state.session_id else "참가자"
		labels[id].text = role + (" · 방장" if id == _host_id else "") + "\n" + id.substr(0, 16) + "\n" + id.substr(16)
	_sample(received_ms)

func _sample(now_ms: int) -> void:
	var sampled := buffer.sample_positions(now_ms)
	for id: String in sampled:
		positions[id] = project_position(sampled[id])
	var viewport_size := get_viewport_rect().size
	position = Vector2(viewport_size.x / 2.0, (viewport_size.y + 100.0) / 2.0)
	for id: String in labels:
		labels[id].position = positions.get(id, Vector2.ZERO) * pixel_scale + Vector2(-90, 18)
	queue_redraw()

func _process(_delta: float) -> void:
	_sample(Time.get_ticks_msec())

func _draw() -> void:
	var extent := get_viewport_rect().size
	for x: int in range(-30, 31):
		draw_line(Vector2(x * pixel_scale, -extent.y), Vector2(x * pixel_scale, extent.y), Color("263b4b"))
	for z: int in range(-30, 31):
		draw_line(Vector2(-extent.x, z * pixel_scale), Vector2(extent.x, z * pixel_scale), Color("263b4b"))
	draw_line(Vector2(-extent.x, 0), Vector2(extent.x, 0), Color("506b80"), 2)
	draw_line(Vector2(0, -extent.y), Vector2(0, extent.y), Color("506b80"), 2)
	for id: String in positions:
		var color := Color("45d5db") if _client != null and id == _client.state.session_id else Color("f1a35e")
		draw_circle(positions[id] * pixel_scale, 12.0, color)
