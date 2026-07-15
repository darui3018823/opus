package oggopus

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

const seekPreRoll48k = 3840

// SeekPCM positions the reader for playback sample at 48 kHz in the current
// logical stream. NextPacket returns decoder pre-roll packets first and marks
// all samples before the target in DiscardStart.
func (r *Reader) SeekPCM(sample int64) (err error) {
	if r.seeker == nil {
		return ErrNotSeekable
	}
	currentOffset, err := r.seeker.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotSeekable, err)
	}
	restore := true
	defer func() {
		if restore {
			_, _ = r.seeker.Seek(currentOffset, io.SeekStart)
		}
	}()

	end, err := r.seeker.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("%w: determine stream size: %v", ErrNotSeekable, err)
	}
	if !r.haveLinkEnd {
		r.linkFinalGranule, r.linkEndOffset, err = findLogicalStreamEnd(r.seeker, r.audioOffset, end, r.Serial())
		if err != nil {
			return err
		}
		r.haveLinkEnd = true
	}
	playable := r.linkFinalGranule - int64(r.Head.PreSkip)
	if sample < 0 || sample > playable {
		return fmt.Errorf("%w: sample %d outside [0,%d]", ErrSeekOutOfRange, sample, playable)
	}
	if sample == playable {
		if _, err := r.seeker.Seek(r.linkEndOffset, io.SeekStart); err != nil {
			return err
		}
		r.packets = NewPacketReader(r.seeker)
		r.pending = nil
		r.preSkipRemaining = 0
		r.haveAudioGranule = false
		r.seekDiscardActive = false
		r.atEnd = true
		r.physicalEOF = false
		r.terminalErr = nil
		restore = false
		return nil
	}

	targetGranule := sample + int64(r.Head.PreSkip)
	searchGranule := targetGranule - seekPreRoll48k
	startOffset := r.audioOffset
	startSequence := uint32(0)
	allowOrphan := false
	if searchGranule > int64(r.Head.PreSkip) {
		page, offset, found, err := bisectPageAtOrBefore(r.seeker, r.audioOffset, r.linkEndOffset, r.Serial(), searchGranule)
		if err != nil {
			return err
		}
		if found {
			startOffset = offset
			startSequence = page.Sequence
			allowOrphan = page.Continued()
		}
	}
	if _, err := r.seeker.Seek(startOffset, io.SeekStart); err != nil {
		return err
	}
	if startOffset == r.audioOffset {
		r.packets = newPacketReaderAt(r.seeker, r.Serial(), r.audioSequence, true)
		r.preSkipRemaining = int(r.Head.PreSkip)
	} else {
		r.packets = newPacketReaderAt(r.seeker, r.Serial(), startSequence, allowOrphan)
		r.preSkipRemaining = 0
	}
	r.pending = nil
	r.previousPageGranule = 0
	r.haveAudioGranule = false
	r.seekDiscardActive = true
	r.seekTargetGranule = targetGranule
	r.atEnd = false
	r.physicalEOF = false
	r.terminalErr = nil
	restore = false
	return nil
}

func findLogicalStreamEnd(rs io.ReadSeeker, start, end int64, serial uint32) (int64, int64, error) {
	low, high := start, end
	bestOffset := int64(-1)
	for range 64 {
		if high-low <= 1 {
			break
		}
		mid := low + (high-low)/2
		page, offset, next, err := scanNextPage(rs, mid, end)
		if errors.Is(err, io.EOF) {
			high = mid
			continue
		}
		if err != nil {
			return 0, 0, err
		}
		if page.Serial == serial {
			bestOffset = offset
			low = max(next, mid+1)
		} else {
			high = offset
		}
	}
	from := start
	if bestOffset >= 0 {
		from = bestOffset
	}
	for from < end {
		page, offset, next, err := scanNextPage(rs, from, end)
		if err != nil {
			return 0, 0, err
		}
		if page.Serial != serial {
			break
		}
		if page.EOS() {
			if page.GranulePosition < 0 {
				return 0, 0, fmt.Errorf("%w: EOS page has no granule position", ErrInvalidOpusStream)
			}
			return page.GranulePosition, next, nil
		}
		from = max(next, offset+1)
	}
	return 0, 0, fmt.Errorf("%w: logical stream has no EOS page", ErrInvalidOpusStream)
}

func bisectPageAtOrBefore(rs io.ReadSeeker, start, end int64, serial uint32, target int64) (Page, int64, bool, error) {
	low, high := start, end
	var best Page
	bestOffset := int64(-1)
	bestNext := start
	for range 64 {
		if high-low <= 1 {
			break
		}
		mid := low + (high-low)/2
		page, offset, next, err := scanNextPage(rs, mid, end)
		if errors.Is(err, io.EOF) {
			high = mid
			continue
		}
		if err != nil {
			return Page{}, 0, false, err
		}
		if page.Serial != serial {
			return Page{}, 0, false, fmt.Errorf("%w: got %d, want %d", ErrSerial, page.Serial, serial)
		}
		if page.GranulePosition >= 0 && page.GranulePosition <= target {
			best, bestOffset, bestNext = page, offset, next
			low = max(next, mid+1)
		} else {
			high = offset
		}
	}
	if bestOffset < 0 {
		return Page{}, 0, false, nil
	}
	for from := bestNext; from < end; {
		page, offset, next, err := scanNextPage(rs, from, end)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Page{}, 0, false, err
		}
		if page.Serial != serial {
			return Page{}, 0, false, fmt.Errorf("%w: got %d, want %d", ErrSerial, page.Serial, serial)
		}
		if page.GranulePosition < 0 || page.GranulePosition <= target {
			if page.GranulePosition >= 0 {
				best, bestOffset = page, offset
			}
			from = max(next, offset+1)
			continue
		}
		break
	}
	return best, bestOffset, true, nil
}

func scanNextPage(rs io.ReadSeeker, from, end int64) (Page, int64, int64, error) {
	const chunkSize = 32 * 1024
	buffer := make([]byte, chunkSize)
	for from < end {
		if _, err := rs.Seek(from, io.SeekStart); err != nil {
			return Page{}, 0, 0, err
		}
		readSize := min(int64(len(buffer)), end-from)
		n, err := rs.Read(buffer[:readSize])
		if err != nil && !errors.Is(err, io.EOF) {
			return Page{}, 0, 0, err
		}
		if n == 0 {
			return Page{}, 0, 0, io.EOF
		}
		search := buffer[:n]
		for {
			index := bytes.Index(search, []byte(CapturePattern))
			if index < 0 {
				break
			}
			candidate := from + int64(n-len(search)+index)
			if _, err := rs.Seek(candidate, io.SeekStart); err != nil {
				return Page{}, 0, 0, err
			}
			page, err := ReadPage(rs)
			if err == nil {
				next, err := rs.Seek(0, io.SeekCurrent)
				if err != nil {
					return Page{}, 0, 0, err
				}
				if next <= end {
					return page, candidate, next, nil
				}
			}
			search = search[index+1:]
		}
		advance := int64(n)
		if n >= len(CapturePattern)-1 {
			advance -= int64(len(CapturePattern) - 1)
		}
		from += max(advance, 1)
	}
	return Page{}, 0, 0, io.EOF
}
