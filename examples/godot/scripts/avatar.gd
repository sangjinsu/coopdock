extends Node3D
class_name AvatarView

var _body: MeshInstance3D
var _label: Label3D

func _ready() -> void:
	_body = MeshInstance3D.new()
	_body.mesh = CapsuleMesh.new()
	_body.position.y = 1.0
	add_child(_body)
	_label = Label3D.new()
	_label.position.y = 2.5
	_label.font_size = 160
	var font := SystemFont.new()
	font.font_names = PackedStringArray(["Apple SD Gothic Neo", "Malgun Gothic", "Noto Sans CJK KR", "sans-serif"])
	_label.font = font
	_label.billboard = BaseMaterial3D.BILLBOARD_ENABLED
	add_child(_label)

func set_avatar_identity(id: String, is_self: bool, is_host: bool) -> void:
	var material := StandardMaterial3D.new()
	material.albedo_color = Color("42dac2") if is_self else Color("ffac66")
	_body.material_override = material
	_label.position.y = 4.2 if is_self else 2.6
	_label.text = ("나" if is_self else "참가자") + (" · 방장" if is_host else "") + "\n" + id.left(5)

func set_authoritative_position(value: Vector3) -> void:
	position = value
