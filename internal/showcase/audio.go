package showcase

import (
	"encoding/binary"
	"io"
	"math"
)

// Synthetic audio: every track and chapter is a deterministic, gentle
// chord progression rendered as 16-bit mono WAV at 22.05 kHz — pleasant
// enough to leave running, nobody's recording, and because every sample
// is a pure function of (seed, time) any byte range can be produced on
// demand, so seeking is O(1) and nothing is stored.

const (
	wavRate     = 22050
	wavChannels = 1
	wavBits     = 16
	wavHeader   = 44
)

// WAVSize is the total file size for a duration.
func WAVSize(durationSec float64) int64 {
	return wavHeader + int64(durationSec*wavRate)*wavChannels*wavBits/8
}

// WriteWAVRange writes bytes [from, to) of the WAV for (seed, duration).
// `to` is exclusive and clamped to the file size.
func WriteWAVRange(w io.Writer, seed uint32, durationSec float64, from, to int64) error {
	total := WAVSize(durationSec)
	if to > total {
		to = total
	}
	if from < 0 {
		from = 0
	}
	if from >= to {
		return nil
	}
	dataLen := uint32(total - wavHeader)
	header := make([]byte, wavHeader)
	copy(header[0:], "RIFF")
	binary.LittleEndian.PutUint32(header[4:], 36+dataLen)
	copy(header[8:], "WAVE")
	copy(header[12:], "fmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1) // PCM
	binary.LittleEndian.PutUint16(header[22:], wavChannels)
	binary.LittleEndian.PutUint32(header[24:], wavRate)
	binary.LittleEndian.PutUint32(header[28:], wavRate*wavChannels*wavBits/8)
	binary.LittleEndian.PutUint16(header[32:], wavChannels*wavBits/8)
	binary.LittleEndian.PutUint16(header[34:], wavBits)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], dataLen)

	pos := from
	if pos < wavHeader {
		end := to
		if end > wavHeader {
			end = wavHeader
		}
		if _, err := w.Write(header[pos:end]); err != nil {
			return err
		}
		pos = end
	}
	if pos >= to {
		return nil
	}
	// Sample-aligned generation from the first sample that covers `pos`.
	sampleIndex := (pos - wavHeader) / 2
	byteInSample := (pos - wavHeader) % 2
	buf := make([]byte, 0, 8192)
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		_, err := w.Write(buf)
		buf = buf[:0]
		return err
	}
	voice := newVoice(seed)
	for pos < to {
		v := voice.sample(sampleIndex, durationSec)
		b := [2]byte{byte(v), byte(v >> 8)}
		for i := byteInSample; i < 2 && pos < to; i++ {
			buf = append(buf, b[i])
			pos++
		}
		byteInSample = 0
		sampleIndex++
		if len(buf) >= 8192 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

// voice: a four-chord loop in a key chosen by the seed, three sine
// partials per note with a slow swell, plus a soft pad an octave down.
type voice struct {
	root   float64
	chords [4][3]float64 // semitone offsets
	tempo  float64       // seconds per chord
	gain   float64
}

func newVoice(seed uint32) voice {
	keys := []float64{196.0, 220.0, 246.9, 261.6, 293.7, 329.6} // G3..E4
	progressions := [][4][3]float64{
		{{0, 4, 7}, {5, 9, 12}, {7, 11, 14}, {0, 4, 7}},    // I IV V I
		{{0, 3, 7}, {8, 12, 15}, {3, 7, 10}, {10, 14, 17}}, // i VI III VII
		{{0, 4, 7}, {9, 12, 16}, {5, 9, 12}, {7, 11, 14}},  // I vi IV V
		{{0, 4, 7}, {2, 5, 9}, {7, 11, 14}, {0, 4, 7}},     // I ii V I
	}
	return voice{
		root:   keys[int(seed)%len(keys)],
		chords: progressions[int(seed>>4)%len(progressions)],
		tempo:  2.4 + float64(int(seed>>8)%5)*0.4,
		gain:   0.22,
	}
}

func (v voice) sample(i int64, durationSec float64) int16 {
	t := float64(i) / wavRate
	chordIdx := int(t/v.tempo) % 4
	phase := math.Mod(t, v.tempo) / v.tempo
	env := math.Sin(phase*math.Pi) * 0.85 // swell per chord
	var out float64
	for n, semis := range v.chords[chordIdx] {
		f := v.root * math.Pow(2, semis/12)
		amp := 1.0 / float64(n+1)
		out += amp * (math.Sin(2*math.Pi*f*t) + 0.35*math.Sin(2*math.Pi*2*f*t) + 0.12*math.Sin(2*math.Pi*3*f*t))
	}
	out += 0.5 * math.Sin(2*math.Pi*(v.root/2)*t) // pad
	out *= env * v.gain
	// Fade the whole piece in over a second and out over its last two.
	if t < 1 {
		out *= t
	}
	if rem := durationSec - t; rem < 2 {
		out *= math.Max(0, rem/2)
	}
	if out > 1 {
		out = 1
	} else if out < -1 {
		out = -1
	}
	return int16(out * 32000)
}
