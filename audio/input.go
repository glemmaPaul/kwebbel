package audio

import (
	"log"

	"github.com/gen2brain/malgo"
)

type AudioInput struct {
	ctx    *malgo.AllocatedContext
	device *malgo.Device

	OutputChan chan []byte
}

func NewAudioInput() (*AudioInput, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
		log.Printf("Malgo Log: %v", message)
	})
	if err != nil {
		return nil, err
	}

	return &AudioInput{
		ctx:        ctx,
		OutputChan: make(chan []byte, 100), // Buffer slightly to prevent blocking
	}, nil
}

func (ai *AudioInput) Start() error {
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = 1
	deviceConfig.SampleRate = 48000
	deviceConfig.PeriodSizeInFrames = 960

	// 1. Define the callback function separately
	// Note: The signature uses []byte, NOT *[0]byte
	onRecv := func(pOutput, pInput []byte, framecount uint32) {
		// pInput contains the raw audio bytes from the microphone

		// clone the data (pInput is only valid for this function call)
		dataCopy := make([]byte, len(pInput))
		copy(dataCopy, pInput)

		// Send to channel (non-blocking)
		select {
		case ai.OutputChan <- dataCopy:
		default:
			// Drop packet if full to avoid blocking the audio thread
		}
	}

	// 2. Put it in the DeviceCallbacks struct
	deviceCallbacks := malgo.DeviceCallbacks{
		Data: onRecv,
	}

	// 3. Pass deviceCallbacks as the 3rd argument
	// (In the previous code, we passed an empty malgo.DeviceCallbacks{} which was wrong)
	device, err := malgo.InitDevice(ai.ctx.Context, deviceConfig, deviceCallbacks)
	if err != nil {
		return err
	}

	ai.device = device
	return device.Start()
}
