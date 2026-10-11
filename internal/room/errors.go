package room

import (
	"context"
	"errors"
)

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

var messages = map[string]string{"NOT_HOST": "Session is not the host.", "GAME_NOT_STARTED": "Game is not started.", "STALE_MATCH": "Match is stale.", "INVALID_INPUT_SEQ": "Invalid input sequence.", "INPUT_CANCELED": "Input was canceled.", "GAME_ERROR": "Game handler failed.", "INVALID_MESSAGE": "Invalid message.", "ROOM_FULL": "Room is full.", "ROOM_NOT_FOUND": "Room was not found.", "ROOM_CLOSED": "Room is closed.", "ROOM_IN_PROGRESS": "Room is in progress.", "ALREADY_IN_ROOM": "Session is already in a room.", "NOT_IN_ROOM": "Session is not in a room.", "PASSWORD_REQUIRED": "Room password is required.", "PASSWORD_INVALID": "Room password is invalid.", "RATE_LIMITED": "Too many password failures.", "SERVER_BUSY": "Server is busy.", "SERVER_CLOSING": "Server is closing.", "SESSION_CLOSED": "Session is closed.", "REQUEST_TIMEOUT": "Request timed out."}

func failure(c string) error { return &Error{Code: c, Message: messages[c]} }
func CodeOf(e error) string {
	if e == nil {
		return ""
	}
	var v *Error
	if errors.As(e, &v) {
		return v.Code
	}
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return "REQUEST_TIMEOUT"
	}
	return "SERVER_BUSY"
}
