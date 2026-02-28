package rooms

type JoinRequest struct {
	PeerID string `json:"peer_id"`
	Ticket string `json:"ticket"`
}

type PeerInfo struct {
	PeerID    string `json:"peer_id"`
	RelayAddr string `json:"relay_addr"`
}

type PeerUpdate struct {
	Members []PeerInfo `json:"members"`
}
