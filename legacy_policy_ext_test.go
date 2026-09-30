package opus_test

import opus "github.com/darui3018823/opus"

// newLegacyEncoder is NewEncoder pinned to ModePolicyLegacy, for tests of
// that policy's decisions (the default before v1.5.0).
func newLegacyEncoder(sampleRate, channels int, application opus.Application) (*opus.Encoder, error) {
	enc, err := opus.NewEncoder(sampleRate, channels, application)
	if err != nil {
		return nil, err
	}
	return enc, enc.SetModePolicy(opus.ModePolicyLegacy)
}
