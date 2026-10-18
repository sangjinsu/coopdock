extends Control
class_name LobbyPanel

var room_number_edit: LineEdit
var join_password_edit: LineEdit
var create_password_edit: LineEdit
var status_label: Label
var start_button: Button
var connect_button: Button
var disconnect_button: Button
var create_button: Button
var join_button: Button
var leave_button: Button
var refresh_button: Button
var capacity_select: OptionButton
var visibility_select: OptionButton
var game_id_edit: LineEdit
var game_label: Label
var url_edit: LineEdit
var room_label: Label
var members_label: Label
var rooms_list: ItemList
var _connection_label: Label
var _client: CoopClient
var _status: String = "disconnected"
var _snapshot: Dictionary = {}
var _room_numbers: Array[String] = []
var _operation_pending: bool = false
var _rooms_refresh_pending: bool = false

func _ready() -> void:
	_build_ui()
	if _client != null:
		_sync_client()
	else:
		_refresh_gate()

func bind_client(client: CoopClient) -> void:
	if _client == client:
		return
	if _client != null:
		_disconnect_signals()
	_client = client
	_rooms_refresh_pending = false
	if _client != null:
		_client.connection_changed.connect(_handle_connection)
		_client.snapshot_changed.connect(_handle_snapshot)
		_client.rooms_received.connect(_handle_rooms)
		_client.request_failed.connect(_handle_failure)
		_client.request_completed.connect(_handle_completed)
	if is_node_ready():
		_sync_client()

func _disconnect_signals() -> void:
	_client.connection_changed.disconnect(_handle_connection)
	_client.snapshot_changed.disconnect(_handle_snapshot)
	_client.rooms_received.disconnect(_handle_rooms)
	_client.request_failed.disconnect(_handle_failure)
	_client.request_completed.disconnect(_handle_completed)

func _sync_client() -> void:
	_operation_pending = false
	_handle_connection(_client.status if _client != null else "disconnected")
	_handle_snapshot(_client.state.current_snapshot if _client != null else {})

func _label(text: String, parent: Node, size: int = 16) -> Label:
	var label := Label.new()
	label.text = text
	label.add_theme_font_size_override("font_size", size)
	label.autowrap_mode = TextServer.AUTOWRAP_WORD_SMART
	parent.add_child(label)
	return label

func _button(text: String, parent: Node, action: Callable) -> Button:
	var button := Button.new()
	button.text = text
	button.custom_minimum_size.y = 36
	parent.add_child(button)
	button.pressed.connect(action)
	return button

func _edit(placeholder: String, parent: Node, secret: bool = false) -> LineEdit:
	var edit := LineEdit.new()
	edit.placeholder_text = placeholder
	edit.secret = secret
	edit.custom_minimum_size.y = 36
	edit.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	parent.add_child(edit)
	return edit

func _section(parent: Node) -> VBoxContainer:
	var panel := PanelContainer.new()
	panel.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	panel.size_flags_vertical = Control.SIZE_EXPAND_FILL
	parent.add_child(panel)
	var margin := MarginContainer.new()
	for side: String in ["left", "right", "top", "bottom"]:
		margin.add_theme_constant_override("margin_" + side, 16)
	panel.add_child(margin)
	var scroll := ScrollContainer.new()
	scroll.horizontal_scroll_mode = ScrollContainer.SCROLL_MODE_DISABLED
	margin.add_child(scroll)
	var column := VBoxContainer.new()
	column.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	column.add_theme_constant_override("separation", 10)
	scroll.add_child(column)
	return column

func _build_ui() -> void:
	set_anchors_and_offsets_preset(Control.PRESET_FULL_RECT)
	var ui_theme := Theme.new()
	var font := SystemFont.new()
	font.font_names = PackedStringArray(["Apple SD Gothic Neo", "Malgun Gothic", "Noto Sans CJK KR", "sans-serif"])
	ui_theme.default_font = font
	ui_theme.default_font_size = 16
	theme = ui_theme
	var background := ColorRect.new()
	background.color = Color("142338")
	background.mouse_filter = Control.MOUSE_FILTER_IGNORE
	background.set_anchors_and_offsets_preset(Control.PRESET_FULL_RECT)
	add_child(background)
	var margin := MarginContainer.new()
	margin.set_anchors_and_offsets_preset(Control.PRESET_FULL_RECT)
	for side: String in ["left", "right", "top", "bottom"]:
		margin.add_theme_constant_override("margin_" + side, 18)
	add_child(margin)
	var page := VBoxContainer.new()
	page.add_theme_constant_override("separation", 12)
	margin.add_child(page)
	var title := HBoxContainer.new()
	page.add_child(title)
	var brand: Label = _label("CoopDock  /  협동게임 로비", title, 24)
	brand.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	_connection_label = _label("연결 안 됨", title)
	_connection_label.autowrap_mode = TextServer.AUTOWRAP_OFF
	var connection := HBoxContainer.new()
	page.add_child(connection)
	url_edit = _edit("서버 WebSocket 주소", connection)
	url_edit.text = "ws://127.0.0.1:8080/ws"
	connect_button = _button("연결", connection, _connect_clicked)
	disconnect_button = _button("연결 끊기", connection, _disconnect_clicked)
	var game_row := HBoxContainer.new()
	page.add_child(game_row)
	_label("개발용 게임 ID", game_row)
	game_id_edit = _edit("등록된 게임 ID", game_row)
	game_id_edit.text = "default"
	game_label = _label("연결 게임: 없음", game_row)
	_label("이 예제의 개발 설정입니다. 배포 게임은 자신의 공개 ID를 코드나 설정에 지정하세요.", page, 14)
	var columns := HBoxContainer.new()
	columns.size_flags_vertical = Control.SIZE_EXPAND_FILL
	columns.add_theme_constant_override("separation", 14)
	page.add_child(columns)
	var entry: VBoxContainer = _section(columns)
	_label("방 찾기", entry, 20)
	var join_row := HBoxContainer.new()
	entry.add_child(join_row)
	room_number_edit = _edit("6자리 룸 번호 · 예: 004271", join_row)
	join_button = _button("입장", join_row, _join_clicked)
	join_password_edit = _edit("입장 비밀번호 (4~64자)", entry, true)
	join_password_edit.hide()
	room_number_edit.text_changed.connect(_room_number_changed)
	room_number_edit.text_submitted.connect(func(_text: String) -> void: _join_clicked())
	join_password_edit.text_submitted.connect(func(_text: String) -> void: _join_clicked())
	refresh_button = _button("공개 방 새로고침", entry, _refresh_clicked)
	rooms_list = ItemList.new()
	rooms_list.custom_minimum_size.y = 120
	rooms_list.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	entry.add_child(rooms_list)
	rooms_list.item_selected.connect(_room_selected)
	_label("공개 방을 선택한 뒤 입장하세요. 비공개 방은 번호로 입장합니다.", entry, 14)
	entry.add_child(HSeparator.new())
	_label("새 방 만들기", entry, 20)
	var options := HBoxContainer.new()
	entry.add_child(options)
	var capacity_label: Label = _label("정원", options)
	capacity_label.autowrap_mode = TextServer.AUTOWRAP_OFF
	capacity_select = OptionButton.new()
	for capacity: int in range(1, 7):
		capacity_select.add_item("%d명" % capacity, capacity)
	capacity_select.select(5)
	options.add_child(capacity_select)
	visibility_select = OptionButton.new()
	visibility_select.add_item("공개", 0)
	visibility_select.add_item("비공개", 1)
	options.add_child(visibility_select)
	visibility_select.item_selected.connect(_visibility_changed)
	create_password_edit = _edit("새 방 비밀번호 (4~64자)", entry, true)
	create_password_edit.hide()
	create_button = _button("방 만들기", entry, _create_clicked)
	var current: VBoxContainer = _section(columns)
	_label("현재 방", current, 20)
	room_label = _label("입장한 방이 없습니다", current, 30)
	members_label = _label("서버에 연결한 뒤 방을 만들거나 입장하세요.", current)
	var actions := HBoxContainer.new()
	current.add_child(actions)
	start_button = _button("게임 시작", actions, _start_clicked)
	leave_button = _button("방 나가기", actions, _leave_clicked)
	_label("대기 중인 방장만 게임을 시작할 수 있습니다.", current, 14)
	_label("방 번호를 함께할 사람에게 알려 주세요.\n비밀번호의 공백도 그대로 사용됩니다.", current, 14)
	status_label = _label("서버 주소를 확인하고 연결하세요.", page)
	status_label.custom_minimum_size.y = 44
	status_label.add_theme_color_override("font_color", Color("bcd8f3"))

func _refresh_gate() -> void:
	if not is_node_ready():
		return
	var connected: bool = _client != null and _status in ["guest", "waiting", "playing"]
	var can_enter: bool = connected and _status == "guest" and _snapshot.is_empty() and not _operation_pending
	connect_button.disabled = _status != "disconnected"
	disconnect_button.disabled = _status == "disconnected" or _status == "closing"
	url_edit.editable = _status == "disconnected"
	game_id_edit.editable = _status == "disconnected"
	game_label.text = "연결 게임: " + (_client.state.game_id if _client != null and not _client.state.game_id.is_empty() else (game_id_edit.text + " (연결 중)" if _status == "connecting" else "없음"))
	create_button.disabled = not can_enter
	join_button.disabled = not can_enter
	room_number_edit.editable = can_enter
	join_password_edit.editable = can_enter
	create_password_edit.editable = can_enter
	capacity_select.disabled = not can_enter
	visibility_select.disabled = not can_enter
	refresh_button.disabled = not connected
	rooms_list.mouse_filter = Control.MOUSE_FILTER_STOP if can_enter else Control.MOUSE_FILTER_IGNORE
	start_button.disabled = not connected or _operation_pending or _snapshot.get("phase") != "WAITING" or _snapshot.get("host_id", "") != _client.state.session_id
	leave_button.disabled = not connected or _operation_pending or _snapshot.is_empty()

func _track_request(id: String) -> void:
	if not id.is_empty():
		_operation_pending = true
		status_label.text = "서버 응답을 기다리는 중입니다."
	_refresh_gate()

func _connect_clicked() -> void:
	if _client == null or connect_button.disabled:
		return
	var result: Error = _client.connect_guest(url_edit.text, game_id_edit.text)
	if result != OK:
		_handle_failure("session.guest", "CLIENT_CONNECT_FAILED", "WebSocket connection failed (%d)." % result)

func _disconnect_clicked() -> void:
	if _client != null:
		_client.disconnect_guest()

func _join_clicked() -> void:
	if _client == null or join_button.disabled:
		return
	var number: String = room_number_edit.text
	if not WireCodec.valid_room_number(number):
		status_label.text = "룸 번호는 반각 숫자 6자리로 입력하세요."
		return
	var password: Variant = null
	if join_password_edit.visible:
		password = join_password_edit.text
		if not WireCodec.valid_password(password):
			status_label.text = "비밀번호는 4~64자로 입력하세요."
			return
	_track_request(_client.join_room(number, password))

func _create_clicked() -> void:
	if _client == null or create_button.disabled:
		return
	var visibility: String = "private" if visibility_select.selected == 1 else "public"
	var password: String = create_password_edit.text if visibility == "private" else ""
	if not WireCodec.valid_create(capacity_select.get_selected_id(), visibility, password):
		status_label.text = "비공개 방 비밀번호는 4~64자로 입력하세요."
		return
	_track_request(_client.create_room(capacity_select.get_selected_id(), visibility, password))

func _start_clicked() -> void:
	if _client != null and not start_button.disabled:
		_track_request(_client.start_room())

func _leave_clicked() -> void:
	if _client != null and not leave_button.disabled:
		_clear_secrets()
		_track_request(_client.leave_room())

func _refresh_clicked() -> void:
	if _client != null and not refresh_button.disabled:
		_client.list_rooms()

func _visibility_changed(index: int) -> void:
	create_password_edit.visible = index == 1
	if index == 0:
		create_password_edit.clear()

func _room_number_changed(_number: String) -> void:
	join_password_edit.clear()
	join_password_edit.hide()

func _room_selected(index: int) -> void:
	if not join_button.disabled and index >= 0 and index < _room_numbers.size():
		room_number_edit.text = _room_numbers[index]
		_room_number_changed(room_number_edit.text)

func _clear_secrets() -> void:
	join_password_edit.clear()
	create_password_edit.clear()
	join_password_edit.hide()

func _handle_connection(status: String) -> void:
	_status = status
	if not is_node_ready():
		return
	var names: Dictionary = {"disconnected": "연결 안 됨", "connecting": "연결 중", "guest": "연결됨", "waiting": "대기 중", "playing": "게임 중", "closing": "연결 종료 중"}
	_connection_label.text = names.get(status, status)
	if status in ["disconnected", "closing"]:
		_clear_secrets()
		_snapshot = {}
		_operation_pending = false
		_rooms_refresh_pending = false
		_room_numbers.clear()
		rooms_list.clear()
		room_label.text = "입장한 방이 없습니다"
		members_label.text = "서버에 연결한 뒤 방을 만들거나 입장하세요."
	_refresh_gate()
	if _rooms_refresh_pending and _client != null and status in ["guest", "waiting", "playing"]:
		_rooms_refresh_pending = false
		_client.list_rooms()

func _handle_snapshot(snapshot: Dictionary) -> void:
	_snapshot = snapshot.duplicate(true)
	if not is_node_ready():
		return
	_clear_secrets()
	_operation_pending = false
	if snapshot.is_empty():
		room_label.text = "입장한 방이 없습니다"
		members_label.text = "방을 만들거나 번호로 입장하세요."
	else:
		_status = "playing" if snapshot.phase == "PLAYING" else "waiting"
		room_label.text = snapshot.room_number
		var lines: PackedStringArray = []
		lines.append("%s / %s / %d명 정원" % ["게임 중" if snapshot.phase == "PLAYING" else "대기 중", "비공개" if snapshot.visibility == "private" else "공개", snapshot.capacity])
		for member: Dictionary in snapshot.members:
			var id: String = member.session_id
			var marks: String = " (나)" if _client != null and id == _client.state.session_id else ""
			if id == snapshot.host_id:
				marks += " (방장)"
			lines.append(id + marks)
		members_label.text = "\n".join(lines)
		status_label.text = "방에 입장했습니다." if snapshot.phase == "WAITING" else "게임이 진행 중입니다."
	_refresh_gate()

func _handle_rooms(rooms: Array) -> void:
	if not is_node_ready():
		# Discard pre-ready wire data and refresh once widgets can consume it.
		_rooms_refresh_pending = true
		return
	rooms_list.clear()
	_room_numbers.clear()
	for room: Dictionary in rooms:
		_room_numbers.append(room.room_number)
		rooms_list.add_item("%s   %d/%d명   %s" % [room.room_number, room.members.size(), room.capacity, "대기 중" if room.phase == "WAITING" else "게임 중"])
	if rooms.is_empty():
		status_label.text = "공개 방이 없습니다. 새 방을 만들어 보세요."

func _handle_failure(op: String, code: String, message: String) -> void:
	if not is_node_ready():
		return
	var guidance: Dictionary = {"PASSWORD_REQUIRED": "비공개 방입니다. 비밀번호를 입력하세요.", "PASSWORD_INVALID": "비밀번호를 확인하고 다시 입장하세요.", "ROOM_NOT_FOUND": "룸 번호를 확인하세요.", "ROOM_FULL": "방이 가득 찼습니다. 다른 방을 선택하세요.", "NOT_HOST": "방장만 게임을 시작할 수 있습니다.", "INVALID_PHASE": "현재 방 상태에서는 이 작업을 할 수 없습니다.", "CLIENT_TIMEOUT": "서버 응답 시간이 초과되었습니다. 연결 상태를 확인하세요.", "CLIENT_BUSY": "요청이 많습니다. 잠시 뒤 다시 시도하세요.", "CLIENT_SEND_FAILED": "요청을 보내지 못했습니다. 연결을 확인하세요.", "CLIENT_CONNECT_FAILED": "서버 주소와 등록된 게임 ID를 확인하세요.", "CLIENT_PROTOCOL": "서버의 게임 연결 정보가 올바르지 않습니다. 서버 설정을 확인하세요."}
	status_label.text = "%s\n[%s] %s" % [guidance.get(code, "요청을 처리하지 못했습니다. 상태를 확인하고 다시 시도하세요."), code, message]
	if op == "room.join" and code in ["PASSWORD_REQUIRED", "PASSWORD_INVALID"]:
		join_password_edit.show()
		join_password_edit.grab_focus()
	_refresh_gate()

func _handle_completed(op: String, _id: String, _payload: Dictionary, error: Dictionary) -> void:
	if op in ["room.create", "room.join", "room.start", "room.leave"]:
		_operation_pending = false
		if error.is_empty() and op == "room.leave":
			status_label.text = "방에서 나왔습니다."
		_refresh_gate()
