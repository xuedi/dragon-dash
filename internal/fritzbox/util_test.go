package fritzbox

import "strconv"

func appendFloat(b []byte, f float64) []byte {
	return strconv.AppendFloat(b, f, 'f', -1, 64)
}
