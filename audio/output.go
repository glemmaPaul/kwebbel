package audio

import (
	"encoding/binary"
	"log"

	"github.com/gen2brain/malgo"
)

type AudioOutput struct {
	ctx    *malgo.AllocatedContext
	device *malgo.Device

	mixer *Mixer
}

// Update constructor to require the Mixer
func NewAudioOutput(m *Mixer) (*AudioOutput, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
		log.Printf("Malgo Log: %v", message)
	})
	if err != nil {
		return nil, err
	}
	return &AudioOutput{
		ctx:   ctx,
		mixer: m,
	}, nil
}

func (ao *AudioOutput) Start() error {
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatS16
	deviceConfig.Playback.Channels = 1
	deviceConfig.SampleRate = 48000
	deviceConfig.PeriodSizeInFrames = 960

	// The Pull Callback: Hardware requests data here
	onRecv := func(pOutput, pInput []byte, framecount uint32) {
		// 1. Calculate samples needed (usually 960)
		samplesNeeded := int(framecount)

		// 2. PULL from Mixer
		// The mixer handles the math, buffering, and silence generation
		mixedPCM := ao.mixer.GetMixedSamples(samplesNeeded)

		// 3. Convert Int16 -> Bytes
		// Malgo expects bytes, but our mixer works in math (int16)
		bytesData := Int16ToBytes(mixedPCM)

		// 4. Write to Hardware Buffer
		copy(pOutput, bytesData)
	}

	deviceCallbacks := malgo.DeviceCallbacks{
		Data: onRecv,
	}

	device, err := malgo.InitDevice(ao.ctx.Context, deviceConfig, deviceCallbacks)
	if err != nil {
		return err
	}
	ao.device = device
	return device.Start()
}

// Helper: Converts Opus output (Int16) to Malgo input (Bytes)
func Int16ToBytes(data []int16) []byte {
	out := make([]byte, len(data)*2)
	for i, v := range data {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(v))
	}
	return out
}
