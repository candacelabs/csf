package core

// BoolToFloat64 converts a bool to a float64 using the 0/1 convention shared
// by Prometheus gauges and similar numeric encodings of boolean state.
func BoolToFloat64(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
