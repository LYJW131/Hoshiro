package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
)

// EncodeWAV encodes 16-bit mono PCM.
func EncodeWAV(samples []int16, rate uint32) []byte {
	var b bytes.Buffer
	n := uint32(len(samples) * 2)
	b.Grow(44 + int(n))
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, 36+n)
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), rate, rate * 2, uint16(2), uint16(16)} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, n)
	binary.Write(&b, binary.LittleEndian, samples)
	return b.Bytes()
}

var errNotWAV = errors.New("not a WAV file")

// DecodeMonoF32 decodes any PCM/float WAV to mono f32 at its native rate.
func DecodeMonoF32(data []byte) ([]float32, uint32, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, errNotWAV
	}
	var format, channels, bits uint16
	var rate uint32
	var pcm []byte
	for p := 12; p+8 <= len(data); {
		id, size := string(data[p:p+4]), int(binary.LittleEndian.Uint32(data[p+4:p+8]))
		body := data[p+8 : min(p+8+size, len(data))]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, 0, errNotWAV
			}
			format, channels = binary.LittleEndian.Uint16(body), binary.LittleEndian.Uint16(body[2:])
			rate, bits = binary.LittleEndian.Uint32(body[4:]), binary.LittleEndian.Uint16(body[14:])
			if format == 0xFFFE && len(body) >= 26 { // WAVE_FORMAT_EXTENSIBLE
				format = binary.LittleEndian.Uint16(body[24:])
			}
		case "data":
			pcm = body
		}
		p += 8 + size + size%2
	}
	if channels == 0 || rate == 0 || pcm == nil {
		return nil, 0, errNotWAV
	}
	bps := int(bits) / 8
	if bps == 0 {
		return nil, 0, errNotWAV
	}
	frames := len(pcm) / (bps * int(channels))
	out := make([]float32, frames)
	for f := range frames {
		var sum float32
		for c := range int(channels) {
			s := pcm[(f*int(channels)+c)*bps:]
			var v float32
			switch {
			case format == 3 && bits == 32:
				v = math.Float32frombits(binary.LittleEndian.Uint32(s))
			case format == 1 && bits == 8:
				v = (float32(s[0]) - 128) / 128
			case format == 1 && bits == 16:
				v = float32(int16(binary.LittleEndian.Uint16(s))) / 32768
			case format == 1 && bits == 24:
				x := int32(s[0]) | int32(s[1])<<8 | int32(int8(s[2]))<<16
				v = float32(x) / (1 << 23)
			case format == 1 && bits == 32:
				v = float32(int32(binary.LittleEndian.Uint32(s))) / (1 << 31)
			default:
				return nil, 0, errors.New("unsupported WAV format")
			}
			sum += v
		}
		out[f] = sum / float32(channels)
	}
	return out, rate, nil
}

// Decode16kMono decodes a WAV (any rate/channels) to 16 kHz mono PCM.
func Decode16kMono(data []byte) ([]int16, error) {
	mono, rate, err := DecodeMonoF32(data)
	if err != nil {
		return nil, err
	}
	r := Resample(mono, rate, TargetSampleRate)
	out := make([]int16, len(r))
	for i, v := range r {
		out[i] = F32ToI16(v)
	}
	return out, nil
}
