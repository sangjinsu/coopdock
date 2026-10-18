extends Node
class_name Main

var client: CoopClient
var lobby: LobbyPanel
var world: GameWorld
var world_2d: GameWorld2D
var view_select: OptionButton
var _hint: Label
var controller: InputController
var _summary: Label
var _lobby_toggle: Button
var _playing: bool = false
var _expanded: bool = false

func _ready() -> void:
	client = CoopClient.new()
	client.name = "CoopClient"
	add_child(client)
	world = GameWorld.new()
	add_child(world)
	world.bind_client(client)
	world_2d = GameWorld2D.new()
	add_child(world_2d)
	world_2d.bind_client(client)
	world_2d.visible = false
	controller = InputController.new()
	add_child(controller)
	controller.bind_client(client)
	var hud := CanvasLayer.new()
	hud.name = "HUD"
	add_child(hud)
	var page := VBoxContainer.new()
	page.set_anchors_and_offsets_preset(Control.PRESET_FULL_RECT)
	hud.add_child(page)
	var theme := Theme.new()
	var font := SystemFont.new()
	font.font_names = PackedStringArray(["Apple SD Gothic Neo", "Malgun Gothic", "Noto Sans CJK KR", "sans-serif"])
	theme.default_font = font
	theme.default_font_size = 16
	page.theme = theme
	var panel := PanelContainer.new()
	page.add_child(panel)
	var bar := HBoxContainer.new()
	bar.add_theme_constant_override("separation", 16)
	panel.add_child(bar)
	_summary = Label.new()
	_summary.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	_summary.clip_text = true
	_summary.mouse_filter = Control.MOUSE_FILTER_PASS
	_summary.text_overrun_behavior = TextServer.OVERRUN_TRIM_ELLIPSIS
	bar.add_child(_summary)
	view_select = OptionButton.new()
	view_select.add_item("3D 공간")
	view_select.add_item("2D XZ 평면")
	view_select.item_selected.connect(func(index: int): _set_view_mode("3d" if index == 0 else "2d"))
	bar.add_child(view_select)
	_lobby_toggle = Button.new()
	_lobby_toggle.text = "로비 열기"
	_lobby_toggle.pressed.connect(func():
		_expanded = not _expanded
		_update_lobby())
	bar.add_child(_lobby_toggle)
	var leave := Button.new()
	leave.text = "방 나가기"
	leave.pressed.connect(func():
		controller.submit_direction(Vector3.ZERO)
		client.leave_room())
	bar.add_child(leave)
	_hint = Label.new()
	_hint.text = "WASD / 방향키 이동 · Q/E 높이 · 청록색: 나 · 주황색: 참가자 · 서버 snapshot 위치"
	page.add_child(_hint)
	lobby = LobbyPanel.new()
	lobby.name = "Lobby"
	lobby.size_flags_vertical = Control.SIZE_EXPAND_FILL
	page.add_child(lobby)
	lobby.bind_client(client)
	client.snapshot_changed.connect(_on_snapshot)
	client.connection_changed.connect(func(_status: String): _on_snapshot(client.state.current_snapshot))
	get_window().focus_exited.connect(func(): controller.set_input_gate(false, _text_focused()))
	get_window().focus_entered.connect(func(): controller.set_input_gate(true, _text_focused()))
	_on_snapshot(client.state.current_snapshot)

func _text_focused() -> bool:
	var owner := get_window().gui_get_focus_owner()
	return owner is LineEdit or owner is TextEdit

func _process(_delta: float) -> void:
	controller.set_input_gate(get_window().has_focus(), _text_focused())

func _on_snapshot(snapshot: Dictionary) -> void:
	_playing = snapshot.get("phase") == "PLAYING"
	_summary.text = "CoopDock · 게임 %s · 방 %s · %s · %d명" % [client.state.game_id, snapshot.get("room_number", "—"), snapshot.get("phase", client.status), snapshot.get("members", []).size()]
	_summary.tooltip_text = _summary.text
	_update_lobby()

func _update_lobby() -> void:
	lobby.visible = not _playing or _expanded
	_lobby_toggle.visible = _playing
	_lobby_toggle.text = "로비 접기" if _expanded else "로비 열기"

func _set_view_mode(mode: String) -> void:
	if mode not in ["3d", "2d"] or mode == controller.view_mode:
		return
	controller.set_view_mode(mode)
	var received_ms := Time.get_ticks_msec()
	world.reset_timeline(client.state.current_snapshot, received_ms)
	world_2d.reset_timeline(client.state.current_snapshot, received_ms)
	world.visible = mode == "3d"
	world_2d.visible = mode == "2d"
	view_select.select(0 if mode == "3d" else 1)
	_hint.text = "WASD / 방향키 이동 · %s · 청록색: 나 · 주황색: 참가자 · 서버 snapshot 위치" % ("Q/E 높이" if mode == "3d" else "XZ 평면 · 32px/unit · Q/E 비활성")
