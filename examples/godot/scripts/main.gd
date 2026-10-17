extends Node
class_name Main

var client: CoopClient
var lobby: LobbyPanel

func _ready() -> void:
	client = CoopClient.new()
	client.name = "CoopClient"
	add_child(client)
	var hud := CanvasLayer.new()
	hud.name = "HUD"
	add_child(hud)
	lobby = LobbyPanel.new()
	lobby.name = "Lobby"
	hud.add_child(lobby)
	lobby.bind_client(client)
