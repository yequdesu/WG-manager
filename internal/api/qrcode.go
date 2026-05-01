package api

import (
	"bytes"
	"fmt"
)

func generateQR(data string) string {
	const modules = 57      // version 10, 57x57, ~400 bytes capacity
	matrix := make([][]byte, modules)
	for i := range matrix {
		matrix[i] = make([]byte, modules)
	}

	bits := encodeQRData(data, modules)
	padded := padToCapacity(bits, capacityForVersion(modules))

	placeFinder(matrix, 0, 0)
	placeFinder(matrix, modules-7, 0)
	placeFinder(matrix, 0, modules-7)
	placeTiming(matrix)
	// Version 10 alignment positions: [6, 28, 50]
	for _, ar := range []int{6, 28, 50} {
		for _, ac := range []int{6, 28, 50} {
			if isReserved(ar, ac, modules) { continue }
			placeAlignmentAt(matrix, ar, ac)
		}
	}

	matrix[modules-8][8] = 1 // dark module

	placeDataZigzag(matrix, padded, modules)

	applyMask2(matrix, modules)

	return renderSVG(matrix, modules)
}

func encodeQRData(data string, modules int) []byte {
	n := len(data)
	// Byte mode: 0100 + count(16 bits for version 10+) + data bytes
	bits := make([]byte, 0, 4+16+n*8)
	bits = append(bits, 0, 1, 0, 0) // mode indicator: byte
	for i := 15; i >= 0; i-- {       // 16-bit character count
		bits = append(bits, byte((n>>i)&1))
	}
	for _, c := range []byte(data) {
		for i := 7; i >= 0; i-- {
			bits = append(bits, (c>>i)&1)
		}
	}
	// Terminator: up to 4 zero bits
	cap := capacityForVersion(modules)
	term := 4
	if len(bits)+term > cap { term = cap - len(bits) }
	if term < 0 { term = 0 }
	for i := 0; i < term; i++ { bits = append(bits, 0) }
	// Pad to 8-bit boundary
	for len(bits)%8 != 0 { bits = append(bits, 0) }
	return bits
}

func padToCapacity(bits []byte, capacity int) []byte {
	padCodes := []byte{0xEC, 0x11}
	ci := 0
	for len(bits) < capacity {
		b := padCodes[ci%2]
		for i := 7; i >= 0 && len(bits) < capacity; i-- {
			bits = append(bits, (b>>i)&1)
		}
		ci++
	}
	return bits
}

func capacityForVersion(modules int) int {
	switch modules {
	case 21: return 152
	case 25: return 272
	case 29: return 440
	case 33: return 640
	case 57: return 1648 // version 10 byte mode
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
	modules := len(m)
	for i := 0; i < 8 && r+i < modules; i++ {
		if c+7 < modules { m[r+i][c+7] = 0 }
	}
	if r+7 < modules {
		for j := 0; j < 8 && c+j < modules; j++ { m[r+7][c+j] = 0 }
	}
	for i := 0; i < 7 && r+1+i < modules; i++ {
		for j := 0; j < 7 && c+1+j < modules; j++ {
			m[r+1+i][c+1+j] = pat[i][j]
		}
	}
}

func placeTiming(m [][]byte) {
	modules := len(m)
	for i := 8; i < modules-8; i++ {
		v := byte(0)
		if i%2 == 0 { v = 1 }
		m[i][6] = v
		m[6][i] = v
	}
}

func placeAlignmentAt(m [][]byte, centerR, centerC int) {
	for dr := -2; dr <= 2; dr++ {
		for dc := -2; dc <= 2; dc++ {
			r, c := centerR+dr, centerC+dc
			if r < 0 || r >= len(m) || c < 0 || c >= len(m[0]) { continue }
			if dr == -2 || dr == 2 || dc == -2 || dc == 2 { m[r][c] = 1 } else { m[r][c] = 0 }
			if dr == 0 && dc == 0 { m[r][c] = 1 }
		}
	}
}

func placeDataZigzag(m [][]byte, data []byte, modules int) {
	col := modules - 1
	bi := 0
	for col > 0 {
		if col == 6 { col = 5 }
		var rows []int
		if (col/2)%2 == 0 {
			for r := modules - 1; r >= 0; r-- { rows = append(rows, r) }
		} else {
			for r := 0; r < modules; r++ { rows = append(rows, r) }
		}
		for _, r := range rows {
			for c := col; c >= col-1; c-- {
				if c < 0 || c >= modules { continue }
				if m[r][c] == 0 && !isReserved(r, c, modules) {
					if bi < len(data) { m[r][c] = data[bi] | 2 }
					bi++
				}
			}
		}
		col -= 2
	}
}

func isReserved(r, c, modules int) bool {
	if r == 6 || c == 6 { return true } // timing
	// Three finder pattern areas
	if (r <= 8 && c <= 8) || (r <= 8 && c >= modules-8) || (r >= modules-8 && c <= 8) { return true }
	// Dark module
	if r == modules-8 && c == 8 { return true }
	// Version 10 alignment pattern positions [6, 28, 50]
	for _, ar := range []int{6, 28, 50} {
		for _, ac := range []int{6, 28, 50} {
			if abs(r-ar) <= 2 && abs(c-ac) <= 2 { return true }
		}
	}
	// Format info areas around finder patterns
	if r <= 8 && c <= 8 { return true }
	if r <= 8 && c >= modules-8 { return true }
	if r >= modules-8 && c <= 8 { return true }
	return false
}

func abs(x int) int { if x < 0 { return -x }; return x }

func applyMask2(m [][]byte, modules int) {
	for r := 0; r < modules; r++ {
		for c := 0; c < modules; c++ {
			if m[r][c]&2 != 0 {
				val := m[r][c] & 1
				if (r+c)%3 == 0 { val ^= 1 }
				m[r][c] = val
			}
		}
	}
}

func renderSVG(matrix [][]byte, modules int) string {
	scale := 6
	quiet := 4 * scale
	size := modules*scale + quiet*2

	var b bytes.Buffer
	b.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`,
		size, size, size, size))
	b.WriteString(`<rect width="100%" height="100%" fill="white"/>`)

	for r := 0; r < modules; r++ {
		for c := 0; c < modules; c++ {
			if matrix[r][c] == 1 {
				b.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="%d" fill="black"/>`,
					quiet+c*scale, quiet+r*scale, scale, scale))
			}
		}
	}
	b.WriteString("</svg>")
	return b.String()
}
