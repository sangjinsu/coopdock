extends Node3D
class_name GameWorld

var avatars: Dictionary = {}
var buffer := SnapshotBuffer.new()
var _client: CoopClient

func _ready() -> void:
	var ground := MeshInstance3D.new()
	var plane := PlaneMesh.new()
	plane.size = Vector2(40, 40)
	ground.mesh = plane
	var material := StandardMaterial3D.new()
	material.albedo_color = Color("233646")
	ground.material_override = material
	add_child(ground)
	var light := DirectionalLight3D.new()
	light.rotation_degrees = Vector3(-55, -25, 0)
	light.light_energy = 1.5
	add_child(light)
	var camera := Camera3D.new()
	camera.position = Vector3(12, 15, 20)
	camera.current = true
	add_child(camera)
	camera.look_at(Vector3(2, 0, 0), Vector3.UP)

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
	for id: String in avatars.keys():
		if not present.has(id):
			avatars[id].queue_free()
			avatars.erase(id)
	if snapshot.get("phase") != "PLAYING":
		buffer.clear()
		return
	buffer.retain_members(present)
	buffer.push_snapshot(snapshot, received_ms)
	for id: String in present:
		if not avatars.has(id):
			var avatar := AvatarView.new()
			add_child(avatar)
			avatars[id] = avatar
		avatars[id].set_avatar_identity(id, id == _client.state.session_id, id == snapshot.host_id)
	_sample(received_ms)

func _sample(now_ms: int) -> void:
	var positions: Dictionary = buffer.sample_positions(now_ms)
	for id: String in positions:
		if avatars.has(id):
			avatars[id].set_authoritative_position(positions[id])

func _process(_delta: float) -> void:
	_sample(Time.get_ticks_msec())
