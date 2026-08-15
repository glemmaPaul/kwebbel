package rooms

type JoinRequest struct {
	PeerID string `json:"peer_id"`
	Ticket string `json:"ticket"`
}

type JoinResponse struct {
	Allowed bool       `json:"allowed"`
	Members []PeerInfo `json:"members"`
}

type PeerInfo struct {
	PeerID         string `json:"peer_id"`
	ConnectionAddr string `json:"connection_addr"`
}

type PeerUpdate struct {
	Members []PeerInfo `json:"members"`
}
