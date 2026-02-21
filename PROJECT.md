# IDEA

Technical Specification: Distributed VoIP via Libp2p (Golang)
1. System Architecture Overview

The system utilizes a Full Mesh topology for media transport. A central HTTP/Libp2p server handles signaling (peer discovery and authorization) and acts as a Circuit Relay v2 fallback for NAT traversal. The client is a local Go binary serving a Svelte frontend via local WebSocket/HTTP.

Topology: Full Mesh (Each client streams audio directly to every other client).

Transport: libp2p / QUIC (UDP) / Protocol ID: /app/voice/1.0.0.

Codec: Opus (48kHz, 20ms frame size).

Latency Target: < 200ms glass-to-glass.

2. Module A: Peering & Transport Layer

Goal: Establish low-latency, resilient bidirectional streams between authenticated peers.

2.1 Transport Configuration

We utilize QUIC (RFC 9000) for its native UDP support, 0-RTT handshakes, and lack of head-of-line blocking.

Host Construction:

Listen on 0.0.0.0/udp/0/quic-v1.

Enable EnableHolePunching() (AutoNAT).

Enable CircuitRelay client (fallback if P2P fails).

2.2 Connection Lifecycle

Signaling (API): The Go client polls the Central Server API to receive a list of active PeerIDs for the target room.

Connection Manager:

Maintains a map activeStreams map[peer.ID]network.Stream.

Dialer: Iterates target PeerIDs. If a connection does not exist, opens a new stream with protocol /app/voice/1.0.0.

Listener: Implements SetStreamHandler to accept incoming voice streams from new joiners.

Keep-Alive: QUIC handles this natively, but application-level pings may be required to keep NAT mappings open during silence.

2.3 The "Gap" Strategy (Network Reliability)

Since we are using UDP (QUIC), packets will be lost or arrive out of order.

Framing: We cannot send raw bytes. We must use Length-Prefix framing:
[2 bytes Length Header] + [N bytes Opus Payload]

Handling Drops: If a packet is dropped, we do not request retransmission (latency is too high). We allow the audio decoder (Opus) to handle it via PLC (Packet Loss Concealment).

3. Module B: Recording (Audio Ingress)

Goal: Capture hardware audio, compress it, and distribute it to the mesh.

3.1 Hardware Capture

Library: github.com/gen2brain/malgo (MiniAudio wrapper).

Config:

Format: FormatS16 (Signed 16-bit integers).

Channels: 1 (Mono).

Sample Rate: 48,000Hz (Opus native rate).

Buffer Size: 960 samples (exactly 20ms of audio).

3.2 Encoding Pipeline

Capture Callback: Receive []byte (PCM) from hardware.

Opus Encoder:

Library: github.com/hraban/opus.

Complexity: 10 (High quality).

Bitrate: ~24kbps (Voice optimized).

Distribution:

The encoded byte slice is pushed to a BroadcastChannel.

A dedicated Goroutine fans out this slice to all activeStreams (The Peering Module).

4. Module C: Audio Broadcasting & Mixing (Audio Egress)

Goal: Receive concurrent streams, handle network jitter, mix audio, and render to speakers.

Critical Challenge: You will receive packets from Peer A and Peer B at slightly different times due to network jitter. You cannot write them directly to the speakers.

4.1 The Jitter Buffer (Per-Peer)

We must implement a fixed-delay Jitter Buffer for each connected peer.

Structure: A ring buffer holding ~60ms of audio packets.

Logic:

Packet arrives -> Push to buffer.

If buffer size < 40ms -> Wait (Pre-buffering).

If buffer size > 100ms -> Drop oldest (Catch up).

4.2 The Software Mixer

The playback engine pulls data, the network pushes data. They are asynchronous.

The "Ticker": The hardware speaker callback drives the clock. It requests 960 samples (20ms) every 20ms.

Mixing Logic (Inside Speaker Callback):

code
Go
download
content_copy
expand_less
// Pseudo-code for mixing loop
outputBuffer := make([]int16, 960)

for peerID, jitterBuffer := range activePeers {
    // Pop 20ms of Opus data from this peer's buffer
    opusData := jitterBuffer.Pop() 
    
    // Decode to PCM
    pcmData := peerDecoder.Decode(opusData)

    // Summation (Mixing)
    for i := 0; i < 960; i++ {
        sum := outputBuffer[i] + pcmData[i]
        // CLAMPING IS MANDATORY to prevent clipping artifacts
        if sum > 32767 { sum = 32767 }
        if sum < -32768 { sum = -32768 }
        outputBuffer[i] = int16(sum)
    }
}
// Write outputBuffer to hardware
5. Review Checklist for Implementation
Component	Task	Complexity	Key Library
Frontend	WebSocket connection to Local Go Backend	Low	Gorilla WebSocket
Backend	Libp2p Host Setup (QUIC + Relay)	Medium	go-libp2p
Backend	Audio Capture (Mic -> Opus)	Medium	malgo, opus
Backend	Network Transport (Framing/Writing)	Low	Standard io.Writer
Backend	Jitter Buffer & Mixing	High	Custom Implementation
6. Diagram of Data Flow
code
Text
download
content_copy
expand_less
[ MICROPHONE ]
      | (PCM)
      v
[ ENCODER (Opus) ]
      | (Encoded Bytes)
      v
[ BROADCASTER ] ===> [ Stream Peer B ] ===> (Network)
                ===> [ Stream Peer C ] ===> (Network)

---------------------------------------------------------

(Network) ===> [ Stream Peer B ] 
                  |
                  v
           [ JITTER BUFFER B ] --> [ DECODER ] 
                                      | (PCM)
                                      v
(Network) ===> [ Stream Peer C ]   [ MIXER ] --> [ SPEAKERS ]
                  |                   ^
                  v                   |
           [ JITTER BUFFER C ] --> [ DECODER ]
Recommendation for "Misgaps"

To handle the "misgaps" (packet loss) you mentioned:

Opus PLC: Ensure your Opus decoder is configured with Decode(data, frameSize, fec=0). If a packet is missing from the Jitter Buffer, pass nil as data. Opus will synthetically generate audio to bridge the gap seamlessly.

UDP Headroom: Ensure your UDP buffer sizes in the OS are sufficient (SetReadBuffer in Go) to prevent local kernel drops.