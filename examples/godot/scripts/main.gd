extends Node
class_name Main

var client: CoopClient

func _ready() -> void:
	client = CoopClient.new()
	client.name = "CoopClient"
	add_child(client)
