package core

import (
	"fmt"

	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/kwebbelkorp/kwebbel/identity"
	"github.com/libp2p/go-libp2p/core/host"
)

type Group struct {
	id    string
	peers []string
}

type Kwebbelaar struct {
	host        *host.Host
	identity    *identity.IdentityManager
	audioEgress *audio.AudioEgress
	group       *Group
}

func NewKwebbelaar(host *host.Host, identity *identity.IdentityManager) *Kwebbelaar {
	return &Kwebbelaar{
		host:     host,
		identity: identity,
	}
}

func (k *Kwebbelaar) Start() error {
	return nil
}

func (k *Kwebbelaar) StartAudioOutput(mixer *audio.Mixer) error {
	audioOutput, err := audio.NewAudioOutput(mixer)
	if err != nil {
		return fmt.Errorf("failed to create audio output: %w", err)
	}
	err = audioOutput.Start()
	if err != nil {
		return fmt.Errorf("failed to start audio output: %w", err)
	}
	return nil
}

func (k *Kwebbelaar) StartAudioEgress() (*audio.AudioEgress, error) {
	// 2. Start Audio Input (Mic)
	mic, err := audio.NewAudioInput()
	if err != nil {
		return nil, fmt.Errorf("failed to create audio input: %w", err)
	}

	egress := audio.NewAudioEgress(mic.OutputChan)
	egress.Start()
	err = mic.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start audio input: %w", err)
	}
	k.audioEgress = egress
	return egress, nil
}
