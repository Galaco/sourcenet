package utils

func FlipBit(v uint, b uint) uint {
	if v&b != 0 {
		v &^= b
	} else {
		v |= b
	}

	return v
}

func PadNumber(number int32, boundary int32) int32 {
	return (((number) + ((boundary) - 1)) / (boundary)) * (boundary)
}
