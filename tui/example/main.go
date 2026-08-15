package main

import (
	"log"
	"sync/atomic"
	"time"

	"github.com/kwebbelkorp/kwebbel/tui"
)

func main() {
	var ui *tui.Program
	var muted atomic.Bool

	ui = tui.New(tui.Signals{
		OnConnect: func(peerID string) {
			go func() {
				ui.SetStatus("Connecting to " + peerID + "...")
				time.Sleep(400 * time.Millisecond)
				ui.EnterCall([]tui.Caller{
					{ID: "local", Label: "You", Local: true},
					{ID: peerID, Label: "Peer"},
				})
			}()
		},
		OnMute: func() {
			go func() {
				muted.Store(!muted.Load())
				ui.SetMuted(muted.Load())
			}()
		},
		OnDisconnect: func() {
			go func() {
				muted.Store(false)
				ui.LeaveCall()
				ui.SetStatus("Disconnected")
			}()
		},
	})

	if err := ui.Run(); err != nil {
		log.Fatal(err)
	}
}
