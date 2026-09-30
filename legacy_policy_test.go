package opus

// Constructors for tests of ModePolicyLegacy's own decisions (the default
// before v1.5.0; ModePolicyLibopus is checked against the libopus oracle).

func newLegacyEncoder(sampleRate, channels int, application Application) (*Encoder, error) {
	enc, err := NewEncoder(sampleRate, channels, application)
	if err != nil {
		return nil, err
	}
	return enc, enc.SetModePolicy(ModePolicyLegacy)
}

func newLegacyMultistreamEncoder(sampleRate, channels, streams, coupledStreams int, mapping []byte, application Application) (*MultistreamEncoder, error) {
	enc, err := NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams, mapping, application)
	if err != nil {
		return nil, err
	}
	return enc, enc.SetModePolicy(ModePolicyLegacy)
}

func newLegacySurroundEncoder(sampleRate, channels, mappingFamily int, application Application) (*SurroundEncoder, error) {
	enc, err := NewSurroundEncoder(sampleRate, channels, mappingFamily, application)
	if err != nil {
		return nil, err
	}
	return enc, enc.SetModePolicy(ModePolicyLegacy)
}

func newLegacyProjectionEncoder(sampleRate, channels, mappingFamily int, application Application) (*ProjectionEncoder, error) {
	enc, err := NewProjectionEncoder(sampleRate, channels, mappingFamily, application)
	if err != nil {
		return nil, err
	}
	return enc, enc.SetModePolicy(ModePolicyLegacy)
}
