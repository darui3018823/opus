package opus

// The controls below forward to the elementary streams through the inner
// multistream encoder, as opus_projection_encoder_ctl forwards every request
// to opus_multistream_encoder_ctl.

// VBR reports whether variable bitrate is enabled.
func (e *ProjectionEncoder) VBR() bool { return e.multistream.VBR() }

// VBRConstraint reports the VBR constraint setting.
func (e *ProjectionEncoder) VBRConstraint() bool { return e.multistream.VBRConstraint() }

// Complexity returns the complexity setting.
func (e *ProjectionEncoder) Complexity() int { return e.multistream.Complexity() }

// SetApplication changes the application of every stream; see
// MultistreamEncoder.SetApplication.
func (e *ProjectionEncoder) SetApplication(application Application) error {
	return e.multistream.SetApplication(application)
}

// Application returns the application of the elementary streams.
func (e *ProjectionEncoder) Application() Application { return e.multistream.Application() }

// SetSignalType sets the signal type hint of every stream.
func (e *ProjectionEncoder) SetSignalType(signal SignalType) { e.multistream.SetSignalType(signal) }

// SignalType returns the signal type hint.
func (e *ProjectionEncoder) SignalType() SignalType { return e.multistream.SignalType() }

// SetDTX enables or disables discontinuous transmission on every stream.
func (e *ProjectionEncoder) SetDTX(enabled bool) { e.multistream.SetDTX(enabled) }

// DTX reports whether DTX is enabled.
func (e *ProjectionEncoder) DTX() bool { return e.multistream.DTX() }

// SetInbandFEC enables or disables in-band FEC on every stream.
func (e *ProjectionEncoder) SetInbandFEC(enabled bool) { e.multistream.SetInbandFEC(enabled) }

// InbandFEC reports whether in-band FEC is enabled.
func (e *ProjectionEncoder) InbandFEC() bool { return e.multistream.InbandFEC() }

// SetInbandFECMode sets OPUS_SET_INBAND_FEC (0, 1 or 2) on every stream.
func (e *ProjectionEncoder) SetInbandFECMode(mode int) error {
	return e.multistream.SetInbandFECMode(mode)
}

// InbandFECMode returns the in-band FEC mode.
func (e *ProjectionEncoder) InbandFECMode() int { return e.multistream.InbandFECMode() }

// SetPacketLossPerc sets the expected packet loss percentage of every stream.
func (e *ProjectionEncoder) SetPacketLossPerc(perc int) { e.multistream.SetPacketLossPerc(perc) }

// PacketLossPerc returns the expected packet loss percentage.
func (e *ProjectionEncoder) PacketLossPerc() int { return e.multistream.PacketLossPerc() }

// SetLSBDepth sets the input precision hint of every stream.
func (e *ProjectionEncoder) SetLSBDepth(depth int) error { return e.multistream.SetLSBDepth(depth) }

// LSBDepth returns the input precision hint.
func (e *ProjectionEncoder) LSBDepth() int { return e.multistream.LSBDepth() }

// SetPredictionDisabled sets OPUS_SET_PREDICTION_DISABLED on every stream.
func (e *ProjectionEncoder) SetPredictionDisabled(disabled bool) {
	e.multistream.SetPredictionDisabled(disabled)
}

// PredictionDisabled reports OPUS_GET_PREDICTION_DISABLED.
func (e *ProjectionEncoder) PredictionDisabled() bool { return e.multistream.PredictionDisabled() }

// SetPhaseInversionDisabled disables CELT intensity-stereo phase inversion on
// every stream.
func (e *ProjectionEncoder) SetPhaseInversionDisabled(disabled bool) {
	e.multistream.SetPhaseInversionDisabled(disabled)
}

// PhaseInversionDisabled reports whether phase inversion is disabled.
func (e *ProjectionEncoder) PhaseInversionDisabled() bool {
	return e.multistream.PhaseInversionDisabled()
}

// SetMaxBandwidth sets the maximum bandwidth of every stream.
func (e *ProjectionEncoder) SetMaxBandwidth(bandwidth int) error {
	return e.multistream.SetMaxBandwidth(bandwidth)
}

// MaxBandwidth returns the maximum bandwidth.
func (e *ProjectionEncoder) MaxBandwidth() int { return e.multistream.MaxBandwidth() }

// SetBandwidth forces the bandwidth of every stream (BandwidthAuto to let
// the encoder decide).
func (e *ProjectionEncoder) SetBandwidth(bandwidth int) error {
	return e.multistream.SetBandwidth(bandwidth)
}

// Bandwidth returns the bandwidth setting.
func (e *ProjectionEncoder) Bandwidth() int { return e.multistream.Bandwidth() }

// Lookahead returns the encoder delay in samples.
func (e *ProjectionEncoder) Lookahead() int { return e.multistream.Lookahead() }

// SetMaxPacketSize sets the libopus max_data_bytes of the packet; see
// MultistreamEncoder.SetMaxPacketSize.
func (e *ProjectionEncoder) SetMaxPacketSize(n int) error { return e.multistream.SetMaxPacketSize(n) }

// MaxPacketSize returns the SetMaxPacketSize value, or 0 when none is set.
func (e *ProjectionEncoder) MaxPacketSize() int { return e.multistream.MaxPacketSize() }
