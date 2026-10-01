package silk

// silkLTPScaleCtrl ports silk_LTP_scale_ctrl_FLP: the LTP scaling index of a
// voiced frame. Only independently coded frames (first in packet) scale;
// round_loss = PacketLoss_perc * nFramesPerPacket, squared and floored at 2%
// when the packet carries LBRR (LBRR_flag), and the index counts how many of
// the two thresholds log2lin(2900 - SNR_dB_Q7), log2lin(3900 - SNR_dB_Q7)
// the product silk_SMULBB(LTPredCodGain, round_loss) exceeds (the float
// coding gain truncates to int16 inside the macro).
func silkLTPScaleCtrl(independent bool, ltpPredCodGain float64, packetLossPerc, nFramesPerPacket int, lbrrFlag bool, snrDBQ7 int) int {
	if !independent {
		return 0
	}
	roundLoss := packetLossPerc * nFramesPerPacket
	if lbrrFlag {
		roundLoss = 2 + roundLoss*roundLoss/100
	}
	prod := int32(int16(int32(float32(ltpPredCodGain)))) * int32(int16(roundLoss))
	idx := 0
	if prod > silkLog2Lin(int32(2900-snrDBQ7)) {
		idx++
	}
	if prod > silkLog2Lin(int32(3900-snrDBQ7)) {
		idx++
	}
	return idx
}
