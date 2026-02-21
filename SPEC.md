# Kwebbel — Package Specification

Technical specification in Go pkg style for Connection Manager, Mixing, Recording, and related components.

---

## Package Index

| Package | Purpose |
|---------|---------|
| [conn](#package-conn) | Connection lifecycle, stream management, dialing, listening |
| [record](#package-record) | Hardware capture, Opus encoding, broadcast channel |
| [mix](#package-mix) | Jitter buffers, software mixer, playback engine |
| [transport](#package-transport) | Length-prefix framing, protocol handling |

---

## Package conn

```
package conn
```

### Overview

Package conn provides connection lifecycle management for the libp2p voice mesh. It maintains active bidirectional streams to peers, handles dialing and listening for the `/app/voice/1.0.0` protocol, and exposes streams for the broadcaster to fan out encoded audio.

**Dependencies:** `github.com/libp2p/go-libp2p`, `github.com/libp2p/go-libp2p/core/network`, `github.com/libp2p/go-libp2p/core/peer`

---

### Constants

```go
const (
    VoiceProtocol = "/app/voice/1.0.0"
)
```

VoiceProtocol is the libp2p protocol ID for voice streams.

---

### Types

#### type ConnectionManager

```go
type ConnectionManager struct {
    // contains filtered or unexported fields
}
```

ConnectionManager maintains the map of active voice streams and coordinates dialing, listening, and keep-alive.

##### func NewConnectionManager

```go
func NewConnectionManager(host host.Host, protocolID protocol.ID) *ConnectionManager
```

NewConnectionManager creates a ConnectionManager bound to the given libp2p Host and protocol.

##### func (*ConnectionManager) ActiveStreams

```go
func (cm *ConnectionManager) ActiveStreams() map[peer.ID]network.Stream
```

ActiveStreams returns a snapshot of the current peer-to-stream map. Callers must not modify the returned map.

##### func (*ConnectionManager) DialPeers

```go
func (cm *ConnectionManager) DialPeers(ctx context.Context, peerIDs []peer.ID) error
```

DialPeers iterates over the target PeerIDs. For each peer without an existing stream, it opens a new stream with the configured protocol. Returns the first error encountered; partial success is possible.

##### func (*ConnectionManager) SetStreamHandler

```go
func (cm *ConnectionManager) SetStreamHandler(handler func(network.Stream))
```

SetStreamHandler registers the libp2p stream handler for incoming voice streams. New joiners will trigger this handler; the ConnectionManager adds accepted streams to activeStreams.

##### func (*ConnectionManager) RemovePeer

```go
func (cm *ConnectionManager) RemovePeer(id peer.ID)
```

RemovePeer closes the stream for the given peer and removes it from the active map.

##### func (*ConnectionManager) Close

```go
func (cm *ConnectionManager) Close() error
```

Close shuts down all active streams and unregisters the stream handler.

---

#### type Dialer

```go
type Dialer interface {
    Dial(ctx context.Context, id peer.ID) (network.Stream, error)
}
```

Dialer opens a new stream to a peer. Implementations typically wrap the libp2p Host's NewStream.

---

#### type Listener

```go
type Listener interface {
    SetHandler(handler func(network.Stream))
}
```

Listener accepts incoming streams. Implementations register a stream handler on the Host.

---

### Variables

```go
var (
    ErrStreamClosed = errors.New("conn: stream already closed")
    ErrDialTimeout  = errors.New("conn: dial timeout")
)
```

---

## Package record

```
package record
```

### Overview

Package record handles audio ingress: hardware capture via malgo, Opus encoding, and distribution to the mesh via a broadcast channel. The capture callback receives PCM from the microphone; the encoder produces Opus frames; the broadcaster fans out to all active streams.

**Dependencies:** `github.com/gen2brain/malgo`, `github.com/hraban/opus`, `github.com/kwebbelkorp/kwebbel/conn`

---

### Constants

```go
const (
    SampleRate     = 48000
    FrameSize      = 960   // 20ms at 48kHz
    OpusComplexity = 10
    OpusBitrate    = 24000 // ~24kbps voice-optimized
)
```

---

### Types

#### type Recorder

```go
type Recorder struct {
    // contains filtered or unexported fields
}
```

Recorder manages hardware capture, Opus encoding, and the broadcast channel to the ConnectionManager.

##### func NewRecorder

```go
func NewRecorder(deviceConfig *malgo.DeviceConfig, broadcaster Broadcaster) *Recorder
```

NewRecorder creates a Recorder with the given malgo device config and a broadcaster for distribution.

##### func (*Recorder) Start

```go
func (r *Recorder) Start(ctx context.Context) error
```

Start begins hardware capture. The capture callback receives PCM, encodes via Opus, and pushes to the broadcaster. Blocks until ctx is cancelled or an error occurs.

##### func (*Recorder) Stop

```go
func (r *Recorder) Stop() error
```

Stop stops the capture device and releases resources.

---

#### type Broadcaster

```go
type Broadcaster interface {
    Broadcast(data []byte)
    Streams() map[peer.ID]io.Writer
}
```

Broadcaster receives encoded Opus frames and fans them out to all active streams. Streams() returns the current writers (one per peer) for the fan-out goroutine.

---

#### type Encoder

```go
type Encoder interface {
    Encode(pcm []byte) ([]byte, error)
    Close() error
}
```

Encoder compresses PCM to Opus. Encode expects 960 samples (20ms) of S16 mono at 48kHz.

---

#### type BroadcastChannel

```go
type BroadcastChannel struct {
    // contains filtered or unexported fields
}
```

BroadcastChannel is a fan-out channel. One goroutine writes encoded frames; a dedicated goroutine reads and writes to all active streams.

##### func NewBroadcastChannel

```go
func NewBroadcastChannel(streams func() map[peer.ID]io.Writer) *BroadcastChannel
```

NewBroadcastChannel creates a channel that uses the given function to obtain the current stream map for each broadcast.

##### func (*BroadcastChannel) Send

```go
func (bc *BroadcastChannel) Send(data []byte)
```

Send pushes a copy of data to all current streams. Non-blocking; drops if a stream is slow.

##### func (*BroadcastChannel) Close

```go
func (bc *BroadcastChannel) Close() error
```

Close stops the broadcast goroutine and releases resources.

---

## Package mix

```
package mix
```

### Overview

Package mix handles audio egress: per-peer jitter buffers, Opus decoding, and software mixing. The playback engine is driven by the hardware speaker callback; the mixer pulls from jitter buffers, decodes, sums, clamps, and writes to the output buffer.

**Dependencies:** `github.com/hraban/opus`, `github.com/gen2brain/malgo`, `github.com/libp2p/go-libp2p/core/peer`

---

### Constants

```go
const (
    JitterBufferMinMS = 40   // Pre-buffering threshold
    JitterBufferMaxMS = 100  // Drop oldest if exceeded
    JitterBufferSizeMS = 60 // Ring buffer capacity
    SamplesPerFrame   = 960 // 20ms at 48kHz
)
```

---

### Types

#### type JitterBuffer

```go
type JitterBuffer struct {
    // contains filtered or unexported fields
}
```

JitterBuffer is a fixed-delay ring buffer holding ~60ms of Opus packets for one peer. Handles network jitter: pre-buffers below 40ms, drops oldest above 100ms.

##### func NewJitterBuffer

```go
func NewJitterBuffer(peerID peer.ID, maxPackets int) *JitterBuffer
```

NewJitterBuffer creates a jitter buffer for the given peer. maxPackets should hold ~60ms (e.g. 3 packets of 20ms).

##### func (*JitterBuffer) Push

```go
func (jb *JitterBuffer) Push(data []byte)
```

Push adds an Opus packet to the buffer. If buffer exceeds 100ms, drops the oldest packet.

##### func (*JitterBuffer) Pop

```go
func (jb *JitterBuffer) Pop() ([]byte, bool)
```

Pop returns the next 20ms of Opus data if available. The second return is false when pre-buffering (< 40ms) or buffer empty; in that case the first return is nil (caller should pass nil to Opus Decode for PLC).

##### func (*JitterBuffer) BufferedDuration

```go
func (jb *JitterBuffer) BufferedDuration() time.Duration
```

BufferedDuration returns the approximate buffered audio duration.

---

#### type Mixer

```go
type Mixer struct {
    // contains filtered or unexported fields
}
```

Mixer aggregates PCM from multiple peers, sums samples with clamping, and produces a single output buffer for the speaker callback.

##### func NewMixer

```go
func NewMixer(peerBuffers map[peer.ID]*JitterBuffer, decoders map[peer.ID]*opus.Decoder) *Mixer
```

NewMixer creates a Mixer with the given per-peer jitter buffers and Opus decoders.

##### func (*Mixer) Mix

```go
func (m *Mixer) Mix(output []int16) (n int, err error)
```

Mix fills output with 960 mixed samples. For each peer, pops from jitter buffer, decodes (or PLC if nil), and sums into output with clamping to prevent clipping. Returns the number of samples written.

##### func (*Mixer) AddPeer

```go
func (m *Mixer) AddPeer(id peer.ID, jb *JitterBuffer, dec *opus.Decoder)
```

AddPeer registers a new peer's jitter buffer and decoder.

##### func (*Mixer) RemovePeer

```go
func (m *Mixer) RemovePeer(id peer.ID)
```

RemovePeer unregisters a peer from the mix.

---

#### type PlaybackEngine

```go
type PlaybackEngine struct {
    // contains filtered or unexported fields
}
```

PlaybackEngine drives playback. The hardware speaker callback requests 960 samples every 20ms; the engine calls Mixer.Mix and writes to the device.

##### func NewPlaybackEngine

```go
func NewPlaybackEngine(mixer *Mixer, deviceConfig *malgo.DeviceConfig) *PlaybackEngine
```

NewPlaybackEngine creates a playback engine with the given mixer and malgo config.

##### func (*PlaybackEngine) OnSendFrames

```go
func (pe *PlaybackEngine) OnSendFrames(pSample, _ []byte, framecount uint32)
```

OnSendFrames is the malgo playback callback. It requests framecount*channels samples from the mixer and copies into pSample.

##### func (*PlaybackEngine) Start

```go
func (pe *PlaybackEngine) Start() error
```

Start initializes and starts the playback device.

##### func (*PlaybackEngine) Stop

```go
func (pe *PlaybackEngine) Stop() error
```

Stop stops the playback device.

---

## Package transport

```
package transport
```

### Overview

Package transport provides length-prefix framing for voice over UDP/QUIC. Raw bytes cannot be sent; each frame is [2 bytes length] + [N bytes Opus payload]. Handles encoding/decoding of frames for reliable packet boundaries.

**Dependencies:** `io`, `encoding/binary`

---

### Constants

```go
const (
    FrameHeaderSize = 2
    MaxFrameSize    = 4096
)
```

---

### Types

#### type FrameWriter

```go
type FrameWriter struct {
    w io.Writer
}
```

FrameWriter writes length-prefixed frames. Each Write call is framed as [2-byte big-endian length][payload].

##### func NewFrameWriter

```go
func NewFrameWriter(w io.Writer) *FrameWriter
```

##### func (*FrameWriter) WriteFrame

```go
func (fw *FrameWriter) WriteFrame(data []byte) (int, error)
```

WriteFrame writes data as a length-prefixed frame. Returns total bytes written (header + payload).

---

#### type FrameReader

```go
type FrameReader struct {
    r io.Reader
}
```

FrameReader reads length-prefixed frames. ReadFrame reads the 2-byte length, then the payload.

##### func NewFrameReader

```go
func NewFrameReader(r io.Reader) *FrameReader
```

##### func (*FrameReader) ReadFrame

```go
func (fr *FrameReader) ReadFrame() ([]byte, error)
```

ReadFrame reads one complete frame. Returns the payload (without the length header). Returns io.EOF when the stream ends cleanly.

---

## Package Dependency Graph

```
                    +----------+
                    |   main   |
                    +----+-----+
                         |
         +---------------+---------------+
         |               |               |
         v               v               v
    +--------+     +----------+     +--------+
    |  conn  |     |  record  |     |  mix   |
    +---+----+     +----+-----+     +---+----+
        |               |                |
        |               |                |
        v               v                v
    +--------+     +----------+     +----------+
    |transport|    |  conn    |    |  opus    |
    +--------+     +----------+     +----------+
```

---

## Data Flow Summary

| Component | Input | Output |
|-----------|-------|--------|
| **ConnectionManager** | peer.ID list from API | `map[peer.ID]network.Stream` |
| **Recorder** | PCM from mic (malgo) | Opus bytes → BroadcastChannel |
| **BroadcastChannel** | Opus bytes | Fan-out to all streams (FrameWriter) |
| **JitterBuffer** | Opus packets from network | Opus packets (time-aligned) |
| **Mixer** | Per-peer JitterBuffer + Decoder | Mixed PCM (960 samples) |
| **PlaybackEngine** | Mixer output | Speaker (malgo) |
| **FrameWriter** | Raw bytes | Length-prefixed frames on stream |
| **FrameReader** | Length-prefixed frames | Raw bytes → JitterBuffer |
