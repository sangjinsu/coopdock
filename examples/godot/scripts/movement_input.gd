extends RefCounted
class_name MovementInput

const KEYS := {
	"move_left": [KEY_A, KEY_LEFT], "move_right": [KEY_D, KEY_RIGHT],
	"move_forward": [KEY_W, KEY_UP], "move_back": [KEY_S, KEY_DOWN],
	"move_down": [KEY_Q], "move_up": [KEY_E]}

static func ensure_actions() -> void:
	for action: String in KEYS:
		if not InputMap.has_action(action):
			InputMap.add_action(action)
		for code: int in KEYS[action]:
			var event := InputEventKey.new()
			event.physical_keycode = code
			if not InputMap.action_has_event(action, event):
				InputMap.action_add_event(action, event)

func sample(focused: bool, text_input_focused: bool, allow_y: bool = true) -> Vector3:
	if not focused or text_input_focused:
		return Vector3.ZERO
	var direction := Vector3(Input.get_axis("move_left", "move_right"),
		Input.get_axis("move_down", "move_up") if allow_y else 0.0,
		Input.get_axis("move_forward", "move_back"))
	return direction.normalized() if direction.length_squared() > 1.0 else direction

func release_actions() -> void:
	for action: String in KEYS:
		Input.action_release(action)
