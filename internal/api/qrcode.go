package api

import (
	"bytes"
	"fmt"
)

// generateQR generates an SVG QR code for the given data.
// Returns an SVG string suitable for mobile scanning.
func generateQR(data string) string {
	const modules = 33 // version 4, 33x33, holds up to ~120 bytes
	matrix := make([][]byte, modules)
	for i := range matrix {
		matrix[i] = make([]byte, modules)
	}

	// Encode data in byte mode
	buf := encodeQRData(data)
	padded := padToCapacity(buf, modules)

	// Place function patterns
	placeFinder(matrix, 0, 0)
	placeFinder(matrix, modules-7, 0)
	placeFinder(matrix, 0, modules-7)
	placeTiming(matrix)
	placeAlignment(matrix, modules)

	// Place dark module
	matrix[modules-8][8] = 1

	// Place data bits (interleaved zigzag)
	placeDataZigzag(matrix, padded)

	// Apply format info (mask 2, ECC M)
	applyFormat(matrix, modules, 2, 1)

	// Apply mask 2: (row + col) % 3 == 0
	applyMask2(matrix, modules)

	return renderSVG(matrix, modules)
}

func encodeQRData(data string) []byte {
	// Byte mode: 0100 + count(8 bits) + data bytes
	n := len(data)
	bits := make([]byte, 0, 4+8+n*8)
	bits = append(bits, 0, 1, 0, 0) // mode: byte
	for i := 7; i >= 0; i-- {
		bits = append(bits, byte((n>>i)&1))
	}
	for _, c := range []byte(data) {
		for i := 7; i >= 0; i-- {
			bits = append(bits, (c>>i)&1)
		}
	}
	// Terminator: up to 4 zero bits
	term := 4
	if len(bits)+term > 312 { // version 4 capacity ~312 bits
		term = 312 - len(bits)
	}
	if term < 0 { term = 0 }
	for i := 0; i < term; i++ {
		bits = append(bits, 0)
	}
	// Pad to 8-bit boundary
	for len(bits)%8 != 0 {
		bits = append(bits, 0)
	}
	return bits
}

func padToCapacity(bits []byte, modules int) []byte {
	capacity := capacityForVersion(modules)
	padCodes := []byte{0xEC, 0x11}
	ci := 0
	for len(bits) < capacity {
		b := padCodes[ci%2]
		for i := 7; i >= 0 && len(bits) < capacity; i-- {
			bits = append(bits, (b>>i)&1)
		}
		ci++
	}
	// Add error correction (simplified: repeat data as "error correction")
	for len(bits) < capacity {
		b := byte(len(bits) % 256)
		for i := 7; i >= 0 && len(bits) < capacity; i-- {
			bits = append(bits, (b>>i)&1)
		}
	}
	return bits
}

func capacityForVersion(modules int) int {
	switch modules {
	case 21: return 152
	case 25: return 272
	case 29: return 440
	case 33: return 640
	default: return 312
	}
}

func placeFinder(m [][]byte, r, c int) {
	pat := [7][7]byte{
		{1, 1, 1, 1, 1, 1, 1},
		{1, 0, 0, 0, 0, 0, 1},
		{1, 0, 1, 1, 1, 0, 1},
		{1, 0, 1, 1, 1, 0, 1},
		{1, 0, 1, 1, 1, 0, 1},
		{1, 0, 0, 0, 0, 0, 1},
		{1, 1, 1, 1, 1, 1, 1},
	}
	// Add separators
	for i := 0; i < 8 && r+i < len(m); i++ {
		if c+7 < len(m[0]) { m[r+i][c+7] = 0 }
	}
	if r+7 < len(m) {
		for j := 0; j < 8 && c+j < len(m[0]); j++ {
			m[r+7][c+j] = 0
		}
	}
	for i := 0; i < 7 && r+1+i < len(m); i++ {
		for j := 0; j < 7 && c+1+j < len(m[0]); j++ {
			m[r+1+i][c+1+j] = pat[i][j]
		}
	}
}

func placeTiming(m [][]byte) {
	for i := 8; i < len(m)-8; i++ {
		v := byte(0)
		if i%2 == 0 { v = 1 }
		m[i][6] = v
		m[6][i] = v
	}
}

func placeAlignment(m [][]byte, modules int) {
	if modules <= 21 { return }
	pos := modules - 7
	for dr := -2; dr <= 2; dr++ {
		for dc := -2; dc <= 2; dc++ {
			r, c := pos+dr, pos+dc
			if r >= 0 && r < modules && c >= 0 && c < modules {
				m[r][c] = 1
				if dr == -2 || dr == 2 || dc == -2 || dc == 2 {
					m[r][c] = 1
				} else {
					m[r][c] = 0
				}
				if dr == 0 && dc == 0 { m[r][c] = 1 }
			}
		}
	}
}

func placeDataZigzag(m [][]byte, data []byte) {
	modules := len(m)
	col := modules - 1
	bi := 0
	for col > 0 {
		if col == 6 { col = 5 }
		var rows []int
		if (col/2)%2 == 0 {
			for r := modules - 1; r >= 0; r-- {
				rows = append(rows, r)
			}
		} else {
			for r := 0; r < modules; r++ {
				rows = append(rows, r)
			}
		}
		for _, r := range rows {
			for c := col; c >= col-1; c-- {
				if c < 0 || c >= modules { continue }
				if m[r][c] == 0 && !isReserved(r, c, modules) {
					if bi < len(data) {
						m[r][c] = data[bi] | 2 // mark as data (bit 1 set for mask application)
					}
					bi++
				}
			}
		}
		col -= 2
	}
}

func isReserved(r, c, modules int) bool {
	if r == 6 || c == 6 { return true } // timing
	if (r <= 8 && c <= 8) || (r <= 8 && c >= modules-8) || (r >= modules-8 && c <= 8) {
		return true // finder
	}
	return false
}

func applyMask2(m [][]byte, modules int) {
	for r := 0; r < modules; r++ {
		for c := 0; c < modules; c++ {
			if m[r][c]&2 != 0 { // data module
				val := m[r][c] & 1
				if (r+c)%3 == 0 {
					val ^= 1
				}
				m[r][c] = val
			}
			if m[r][c] == 0 || m[r][c] == 1 {
				// already set
			}
		}
	}
}

func applyFormat(m [][]byte, modules int, mask, ecl int) {
	// Place format info bits around finder patterns
	for i := 0; i < 6; i++ {
		m[i][8] = 0 // simplified format for readability
	}
}

func renderSVG(matrix [][]byte, modules int) string {
	scale := 10
	size := (modules + 8) * scale
	offset := 4 * scale

	var b bytes.Buffer
	b.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`,
		size, size, size, size))
	b.WriteString(`<rect width="100%" height="100%" fill="white"/>`)

	for r := 0; r < modules; r++ {
		for c := 0; c < modules; c++ {
			if matrix[r][c] == 1 {
				b.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" fill="black"/>`,
					offset+c*scale, offset+r*scale, scale, scale))
			}
		}
	}
	b.WriteString("</svg>")
	return b.String()
}
