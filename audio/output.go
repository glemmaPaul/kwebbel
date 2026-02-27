package audio

import (
	"encoding/binary"

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
		//log.Printf("Malgo Log: %v", message)
	})
	if err != nil {
		return nil, err
	}
	return &AudioOutput{
		ctx:   ctx,
		mixer: m,
	}, nil
}

func (audioOutput *AudioOutput) Start() error {
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatS16
	deviceConfig.Playback.Channels = 1
	deviceConfig.SampleRate = 48000
	deviceConfig.PeriodSizeInFrames = 960

	onRecv := func(pOutput, pInput []byte, framecount uint32) {
		// We likely have 960 here.
		samplesNeeded := int(framecount)

		// Pull from mixer (multiple incoming streams combined)
		mixedPCM := audioOutput.mixer.GetMixedSamples(samplesNeeded)

		// Convert Int16 -> Bytes
		bytesData := Int16ToBytes(mixedPCM)

		copy(pOutput, bytesData)
	}

	deviceCallbacks := malgo.DeviceCallbacks{
		Data: onRecv,
	}

	device, err := malgo.InitDevice(audioOutput.ctx.Context, deviceConfig, deviceCallbacks)
	if err != nil {
		return err
	}
	audioOutput.device = device
	return device.Start()
}

// Helper: Converts Opus output (Int16) to Malgo input (Bytes)
// * Slightly AI generated code *
func Int16ToBytes(data []int16) []byte {
	out := make([]byte, len(data)*2)
	for i, v := range data {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(v))
	}
	return out
}
