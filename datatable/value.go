package datatable

import (
	"bytes"
	"fmt"
	"math"

	"github.com/galaco/sourcenet/bitbuf"
)

// Wire limits of property values.
const (
	maxStringBits       = 9
	maxStringBufferSize = 1 << maxStringBits
)

// Value is a decoded property value. Which field is meaningful depends on the
// property type:
//
//	Int, Int64        Int (unsigned props are zero-extended)
//	Float             Float
//	Vector, VectorXY  Vector (Z is zero for VectorXY)
//	String            String
//	Array             Array, one Value per transmitted element
type Value struct {
	Int    int64
	Float  float32
	Vector [3]float32
	String string
	Array  []Value
}

// Clone returns a deep copy of v.
func (v Value) Clone() Value {
	if v.Array != nil {
		arr := make([]Value, len(v.Array))
		for i, e := range v.Array {
			arr[i] = e.Clone()
		}
		v.Array = arr
	}
	return v
}

// Format renders the value according to the property type.
func (v Value) Format(t PropType) string {
	switch t {
	case Int, Int64:
		return fmt.Sprint(v.Int)
	case Float:
		return fmt.Sprint(v.Float)
	case Vector:
		return fmt.Sprintf("(%g, %g, %g)", v.Vector[0], v.Vector[1], v.Vector[2])
	case VectorXY:
		return fmt.Sprintf("(%g, %g)", v.Vector[0], v.Vector[1])
	case String:
		return fmt.Sprintf("%q", v.String)
	case Array:
		return fmt.Sprintf("array[%d]", len(v.Array))
	}
	return "?"
}

// DecodeProp reads one property value (dt_encode.cpp *_Decode).
func DecodeProp(r *bitbuf.Reader, p *SendProp) Value {
	var v Value
	decodeInto(r, p, &v)
	return v
}

// decodeInto decodes into dst, reusing dst's array storage where possible.
func decodeInto(r *bitbuf.Reader, p *SendProp, dst *Value) {
	switch p.Type {
	case Int:
		dst.Int = decodeInt(r, p)
	case Float:
		dst.Float = decodeFloat(r, p)
	case Vector:
		dst.Vector[0] = decodeFloat(r, p)
		dst.Vector[1] = decodeFloat(r, p)
		if p.Flags&FlagNormal == 0 {
			dst.Vector[2] = decodeFloat(r, p)
		} else {
			neg := r.ReadBit()
			// Explicit conversions stop the compiler fusing this into an
			// FMA, which would differ from the engine in the last bit.
			sq := float32(dst.Vector[0]*dst.Vector[0]) + float32(dst.Vector[1]*dst.Vector[1])
			z := float32(0)
			if sq < 1 {
				z = float32(math.Sqrt(float64(1 - sq)))
			}
			if neg {
				z = -z
			}
			dst.Vector[2] = z
		}
	case VectorXY:
		dst.Vector[0] = decodeFloat(r, p)
		dst.Vector[1] = decodeFloat(r, p)
		dst.Vector[2] = 0
	case String:
		n := int(r.ReadBits(maxStringBits))
		b := r.ReadBytes(n)
		// The engine treats the payload as a C string.
		if i := bytes.IndexByte(b, 0); i >= 0 {
			b = b[:i]
		}
		dst.String = string(b)
	case Array:
		n := int(r.ReadBits(arrayLengthBits(p)))
		elem := p.ArrayElement
		if cap(dst.Array) >= n {
			dst.Array = dst.Array[:n]
		} else {
			dst.Array = make([]Value, n)
		}
		for i := 0; i < n; i++ {
			if elem == nil {
				r.Skip(r.BitsLeft() + 1) // force overflow: unlinked array
				return
			}
			decodeInto(r, elem, &dst.Array[i])
		}
	case Int64:
		dst.Int = decodeInt64(r, p)
	default:
		r.Skip(r.BitsLeft() + 1)
	}
}

func arrayLengthBits(p *SendProp) int {
	return log2(p.NumElements) + 1
}

func decodeInt(r *bitbuf.Reader, p *SendProp) int64 {
	if p.Flags&FlagVarInt != 0 {
		if p.Flags&FlagUnsigned != 0 {
			return int64(r.ReadVarInt32())
		}
		return int64(r.ReadSignedVarInt32())
	}
	if p.Flags&FlagUnsigned != 0 {
		return int64(r.ReadBits(p.NumBits))
	}
	return int64(r.ReadSBits(p.NumBits))
}

func decodeInt64(r *bitbuf.Reader, p *SendProp) int64 {
	if p.Flags&FlagVarInt != 0 {
		if p.Flags&FlagUnsigned != 0 {
			return int64(r.ReadVarInt64())
		}
		return r.ReadSignedVarInt64()
	}
	if p.Flags&FlagUnsigned != 0 {
		lo := uint64(r.ReadBits(32))
		hi := uint64(r.ReadBits(p.NumBits - 32))
		return int64(hi<<32 | lo)
	}
	neg := r.ReadBit()
	lo := uint64(r.ReadBits(32))
	hi := uint64(r.ReadBits(p.NumBits - 32 - 1))
	v := int64(hi<<32 | lo)
	if neg {
		v = -v
	}
	return v
}

func coordMPFlags(flags uint32) (integral, lowPrecision bool) {
	return flags&FlagCoordMPIntegral != 0, flags&FlagCoordMPLowPrecision != 0
}

const coordMPMask = FlagCoordMP | FlagCoordMPLowPrecision | FlagCoordMPIntegral

func decodeFloat(r *bitbuf.Reader, p *SendProp) float32 {
	switch f := p.Flags; {
	case f&FlagCoord != 0:
		return r.ReadBitCoord()
	case f&coordMPMask != 0:
		integral, low := coordMPFlags(f)
		return r.ReadBitCoordMP(integral, low)
	case f&FlagNoScale != 0:
		return r.ReadFloat32()
	case f&FlagNormal != 0:
		return r.ReadBitNormal()
	}
	if p.NumBits <= 0 || p.NumBits > 32 {
		return p.LowValue
	}
	interp := r.ReadBits(p.NumBits)
	// Explicit float32 conversions keep each step in single precision and
	// prevent fused multiply-add, matching the engine's arithmetic.
	frac := float32(interp) / float32(uint32(1<<uint(p.NumBits)-1))
	return p.LowValue + float32((p.HighValue-p.LowValue)*frac)
}

// SkipProp advances r past one encoded property value.
func SkipProp(r *bitbuf.Reader, p *SendProp) {
	switch p.Type {
	case Int:
		if p.Flags&FlagVarInt != 0 {
			r.ReadVarInt32()
		} else {
			r.Skip(p.NumBits)
		}
	case Float:
		skipFloat(r, p)
	case Vector:
		skipFloat(r, p)
		skipFloat(r, p)
		if p.Flags&FlagNormal != 0 {
			r.Skip(1)
		} else {
			skipFloat(r, p)
		}
	case VectorXY:
		skipFloat(r, p)
		skipFloat(r, p)
	case String:
		r.Skip(int(r.ReadBits(maxStringBits)) * 8)
	case Array:
		n := int(r.ReadBits(arrayLengthBits(p)))
		for i := 0; i < n && p.ArrayElement != nil; i++ {
			SkipProp(r, p.ArrayElement)
		}
	default:
		var v Value
		decodeInto(r, p, &v)
	}
}

func skipFloat(r *bitbuf.Reader, p *SendProp) {
	switch f := p.Flags; {
	case f&FlagCoord != 0:
		r.ReadBitCoord()
	case f&coordMPMask != 0:
		integral, low := coordMPFlags(f)
		r.ReadBitCoordMP(integral, low)
	case f&FlagNoScale != 0:
		r.Skip(32)
	case f&FlagNormal != 0:
		r.Skip(bitbuf.NormalFractionalBits + 1)
	default:
		r.Skip(p.NumBits)
	}
}

// EncodeProp writes one property value (dt_encode.cpp *_Encode). Out of range
// floats are clamped as the engine does.
func EncodeProp(w *bitbuf.Writer, p *SendProp, v Value) {
	switch p.Type {
	case Int:
		if p.Flags&FlagVarInt != 0 {
			if p.Flags&FlagUnsigned != 0 {
				w.WriteVarInt32(uint32(v.Int))
			} else {
				w.WriteSignedVarInt32(int32(v.Int))
			}
			return
		}
		w.WriteBits(uint32(v.Int), p.NumBits)
	case Float:
		encodeFloat(w, p, v.Float)
	case Vector:
		encodeFloat(w, p, v.Vector[0])
		encodeFloat(w, p, v.Vector[1])
		if p.Flags&FlagNormal == 0 {
			encodeFloat(w, p, v.Vector[2])
		} else {
			w.WriteBit(v.Vector[2] <= -bitbuf.NormalResolution)
		}
	case VectorXY:
		encodeFloat(w, p, v.Vector[0])
		encodeFloat(w, p, v.Vector[1])
	case String:
		s := v.String
		for i := 0; i < len(s); i++ {
			if s[i] == 0 {
				s = s[:i]
				break
			}
		}
		if len(s) > maxStringBufferSize-1 {
			s = s[:maxStringBufferSize-1]
		}
		w.WriteBits(uint32(len(s)), maxStringBits)
		w.WriteBytes([]byte(s))
	case Array:
		n := len(v.Array)
		if n > p.NumElements {
			n = p.NumElements
		}
		w.WriteBits(uint32(n), arrayLengthBits(p))
		for i := 0; i < n; i++ {
			EncodeProp(w, p.ArrayElement, v.Array[i])
		}
	case Int64:
		encodeInt64(w, p, v.Int)
	}
}

func encodeInt64(w *bitbuf.Writer, p *SendProp, v int64) {
	if p.Flags&FlagVarInt != 0 {
		if p.Flags&FlagUnsigned != 0 {
			w.WriteVarInt64(uint64(v))
		} else {
			w.WriteSignedVarInt64(v)
		}
		return
	}
	if p.Flags&FlagUnsigned != 0 {
		u := uint64(v)
		w.WriteBits(uint32(u), 32)
		w.WriteBits(uint32(u>>32), p.NumBits-32)
		return
	}
	neg := v < 0
	u := uint64(v)
	if neg {
		u = uint64(-v)
	}
	w.WriteBit(neg)
	w.WriteBits(uint32(u), 32)
	w.WriteBits(uint32(u>>32), p.NumBits-32-1)
}

func encodeFloat(w *bitbuf.Writer, p *SendProp, f float32) {
	switch flags := p.Flags; {
	case flags&FlagCoord != 0:
		w.WriteBitCoord(f)
		return
	case flags&coordMPMask != 0:
		integral, low := coordMPFlags(flags)
		w.WriteBitCoordMP(f, integral, low)
		return
	case flags&FlagNormal != 0:
		w.WriteBitNormal(f)
		return
	case flags&FlagNoScale != 0:
		w.WriteFloat32(f)
		return
	}
	if p.NumBits <= 0 || p.NumBits > 32 {
		return
	}
	var u uint32
	switch {
	case f < p.LowValue:
		u = 0
	case f > p.HighValue:
		u = uint32(1<<uint(p.NumBits) - 1)
	default:
		mul := rangeMultiplier(p.NumBits, float64(p.HighValue-p.LowValue))
		rangeVal := float32((f - p.LowValue) * mul)
		u = uint32(math.RoundToEven(float64(rangeVal)))
	}
	w.WriteBits(u, p.NumBits)
}

// rangeMultiplier mirrors AssignRangeMultiplier: the factor mapping the
// prop's range onto its integer encoding without exceeding the top value.
func rangeMultiplier(nbits int, rng float64) float32 {
	high := uint64(1)<<uint(nbits) - 1
	if nbits == 32 {
		high = 0xFFFFFFFE
	}
	mul := float32(float64(high) / rng)
	if math.Abs(rng) <= 0.001 { // CloseEnough(range, 0)
		mul = float32(high)
	}
	exceeds := func(m float32) bool {
		prod := float64(m) * rng
		return uint64(prod) > high || prod > float64(high)
	}
	if exceeds(mul) {
		for _, m := range []float32{0.9999, 0.99, 0.9, 0.8, 0.7} {
			mul = float32(float64(high)/rng) * m
			if !exceeds(mul) {
				return mul
			}
		}
		return 0
	}
	return mul
}
