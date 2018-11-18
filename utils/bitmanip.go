package utils

// FlipBit ?
func FlipBit(v uint, b uint) uint {
	if v&b != 0 {
		v &^= b
	} else {
		v |= b
	}

	return v
}

// PadNumber Rounds a number up to the next multiple
// of provided boundary
func PadNumber(number int32, boundary int32) int32 {
	return (((number) + ((boundary) - 1)) / (boundary)) * (boundary)
}
