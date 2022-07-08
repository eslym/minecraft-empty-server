package main

import "go/types"

type PingPlayers struct {
	Max    int         `json:"max"`
	Online int         `json:"online"`
	Sample []types.Nil `json:"sample"`
}

type ServerVersion struct {
	Name     string `json:"name"`
	Protocol int    `json:"protocol"`
}

type PingResponse struct {
	Version     ServerVersion `json:"version"`
	Players     PingPlayers   `json:"players"`
	Description Message       `json:"description"`
	FavIcon     string        `json:"favicon,omitempty"`
}

type Message struct {
	Text string `json:"text"`
}
