package distribution

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
)

const qrErrorCorrectionM = 0

var qrECCPerBlockM = [...]int{
	0, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26,
	26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28,
}

var qrBlocksM = [...]int{
	0, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17,
	17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49,
}

type qrMatrix struct {
	version  int
	size     int
	modules  [][]bool
	function [][]bool
}

func QRCodeSVG(text string, scale, border int) ([]byte, error) {
	matrix, err := makeQRMatrix(text)
	if err != nil {
		return nil, err
	}
	if scale == 0 {
		scale = 8
	}
	if border == 0 {
		border = 4
	}
	if scale < 1 || scale > 64 || border < 1 || border > 32 {
		return nil, errors.New("invalid QR scale or border")
	}
	dimension := (matrix.size + border*2) * scale
	var pathData strings.Builder
	for y := 0; y < matrix.size; y++ {
		for x := 0; x < matrix.size; x++ {
			if matrix.modules[y][x] {
				fmt.Fprintf(&pathData, "M%d %dh1v1h-1z", x+border, y+border)
			}
		}
	}
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="QR code" shape-rendering="crispEdges"><rect width="100%%" height="100%%" fill="#fff"/><path d="%s" fill="#000" transform="scale(%d)"/></svg>`, dimension, dimension, matrix.size+border*2, matrix.size+border*2, pathData.String(), scale)), nil
}

func QRCodePNG(text string, size int) ([]byte, error) {
	matrix, err := makeQRMatrix(text)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		size = 512
	}
	if size < 128 || size > 2048 {
		return nil, errors.New("QR PNG size must be between 128 and 2048 pixels")
	}
	border := 4
	scale := size / (matrix.size + border*2)
	if scale < 1 {
		return nil, errors.New("QR PNG size is too small")
	}
	dimension := scale * (matrix.size + border*2)
	image := image.NewRGBA(image.Rect(0, 0, dimension, dimension))
	for i := range image.Pix {
		image.Pix[i] = 255
	}
	black := color.RGBA{A: 255}
	for y := 0; y < matrix.size; y++ {
		for x := 0; x < matrix.size; x++ {
			if !matrix.modules[y][x] {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					image.Set((x+border)*scale+dx, (y+border)*scale+dy, black)
				}
			}
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, image); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func makeQRMatrix(text string) (*qrMatrix, error) {
	data := []byte(text)
	if len(data) == 0 {
		return nil, errors.New("QR content must not be empty")
	}
	version := 0
	var dataCodewords []byte
	for candidate := 1; candidate <= 40; candidate++ {
		capacity := qrRawDataModules(candidate)/8 - qrECCPerBlockM[candidate]*qrBlocksM[candidate]
		lengthBits := 8
		if candidate >= 10 {
			lengthBits = 16
		}
		if len(data) > (1<<lengthBits)-1 {
			continue
		}
		requiredBits := 4 + lengthBits + len(data)*8
		if requiredBits > capacity*8 {
			continue
		}
		bits := make([]bool, 0, capacity*8)
		appendBits := func(value, count int) {
			for i := count - 1; i >= 0; i-- {
				bits = append(bits, (value>>i)&1 != 0)
			}
		}
		appendBits(4, 4)
		appendBits(len(data), lengthBits)
		for _, value := range data {
			appendBits(int(value), 8)
		}
		terminator := minInt(4, capacity*8-len(bits))
		appendBits(0, terminator)
		for len(bits)%8 != 0 {
			bits = append(bits, false)
		}
		dataCodewords = make([]byte, 0, capacity)
		for i := 0; i < len(bits); i += 8 {
			value := 0
			for j := 0; j < 8; j++ {
				value <<= 1
				if bits[i+j] {
					value |= 1
				}
			}
			dataCodewords = append(dataCodewords, byte(value))
		}
		for pad := byte(0xec); len(dataCodewords) < capacity; pad ^= 0xec ^ 0x11 {
			dataCodewords = append(dataCodewords, pad)
		}
		version = candidate
		break
	}
	if version == 0 {
		return nil, errors.New("QR content is too long")
	}
	codewords := qrInterleave(dataCodewords, version)
	matrix := newQRMatrix(version)
	matrix.drawFunctionPatterns()
	matrix.drawCodewords(codewords)
	bestMask := 0
	bestPenalty := int(^uint(0) >> 1)
	for mask := 0; mask < 8; mask++ {
		matrix.applyMask(mask)
		matrix.drawFormatBits(mask)
		if penalty := matrix.penalty(); penalty < bestPenalty {
			bestMask, bestPenalty = mask, penalty
		}
		matrix.applyMask(mask)
	}
	matrix.applyMask(bestMask)
	matrix.drawFormatBits(bestMask)
	return matrix, nil
}

func qrRawDataModules(version int) int {
	result := (16*version+128)*version + 64
	if version >= 2 {
		align := version/7 + 2
		result -= (25*align-10)*align - 55
		if version >= 7 {
			result -= 36
		}
	}
	return result
}

func qrInterleave(data []byte, version int) []byte {
	blockCount := qrBlocksM[version]
	eccLength := qrECCPerBlockM[version]
	rawCodewords := qrRawDataModules(version) / 8
	shortBlocks := blockCount - rawCodewords%blockCount
	shortLength := rawCodewords / blockCount
	shortDataLength := shortLength - eccLength
	divisor := qrDivisor(eccLength)
	blocks := make([][]byte, 0, blockCount)
	offset := 0
	for i := 0; i < blockCount; i++ {
		dataLength := shortDataLength
		if i >= shortBlocks {
			dataLength++
		}
		blockData := append([]byte(nil), data[offset:offset+dataLength]...)
		offset += dataLength
		ecc := qrRemainder(blockData, divisor)
		if i < shortBlocks {
			blockData = append(blockData, 0)
		}
		blocks = append(blocks, append(blockData, ecc...))
	}
	result := make([]byte, 0, rawCodewords)
	for i := 0; i < shortLength+1; i++ {
		for j, block := range blocks {
			if i == shortDataLength && j < shortBlocks {
				continue
			}
			if i < len(block) {
				result = append(result, block[i])
			}
		}
	}
	return result
}

func qrDivisor(degree int) []byte {
	result := make([]byte, degree)
	result[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := range result {
			value := qrMultiply(result[j], root)
			if j+1 < len(result) {
				value ^= result[j+1]
			}
			result[j] = value
		}
		root = qrMultiply(root, 2)
	}
	return result
}

func qrRemainder(data, divisor []byte) []byte {
	result := make([]byte, len(divisor))
	for _, value := range data {
		factor := value ^ result[0]
		copy(result, result[1:])
		result[len(result)-1] = 0
		for i := range result {
			result[i] ^= qrMultiply(divisor[i], factor)
		}
	}
	return result
}

func qrMultiply(x, y byte) byte {
	result := 0
	for i := 7; i >= 0; i-- {
		result = (result << 1) ^ ((result >> 7) * 0x11d)
		result ^= int(y>>i&1) * int(x)
	}
	return byte(result)
}

func newQRMatrix(version int) *qrMatrix {
	size := version*4 + 17
	modules := make([][]bool, size)
	function := make([][]bool, size)
	for i := range modules {
		modules[i] = make([]bool, size)
		function[i] = make([]bool, size)
	}
	return &qrMatrix{version: version, size: size, modules: modules, function: function}
}

func (m *qrMatrix) setFunction(x, y int, dark bool) {
	if x < 0 || y < 0 || x >= m.size || y >= m.size {
		return
	}
	m.modules[y][x] = dark
	m.function[y][x] = true
}

func (m *qrMatrix) drawFunctionPatterns() {
	for i := 0; i < m.size; i++ {
		m.setFunction(6, i, i%2 == 0)
		m.setFunction(i, 6, i%2 == 0)
	}
	m.drawFinder(3, 3)
	m.drawFinder(m.size-4, 3)
	m.drawFinder(3, m.size-4)
	positions := qrAlignmentPositions(m.version)
	for i, x := range positions {
		for j, y := range positions {
			if i == 0 && j == 0 || i == 0 && j == len(positions)-1 || i == len(positions)-1 && j == 0 {
				continue
			}
			m.drawAlignment(x, y)
		}
	}
	m.drawFormatBits(0)
	if m.version >= 7 {
		remainder := m.version
		for i := 0; i < 12; i++ {
			remainder = remainder<<1 ^ (remainder>>11)*0x1f25
		}
		bits := m.version<<12 | remainder
		for i := 0; i < 18; i++ {
			bit := (bits>>i)&1 != 0
			a := m.size - 11 + i%3
			b := i / 3
			m.setFunction(a, b, bit)
			m.setFunction(b, a, bit)
		}
	}
}

func (m *qrMatrix) drawFinder(centerX, centerY int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			distance := maxInt(absInt(dx), absInt(dy))
			m.setFunction(centerX+dx, centerY+dy, distance != 2 && distance != 4)
		}
	}
}

func (m *qrMatrix) drawAlignment(centerX, centerY int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			m.setFunction(centerX+dx, centerY+dy, maxInt(absInt(dx), absInt(dy)) != 1)
		}
	}
}

func qrAlignmentPositions(version int) []int {
	if version == 1 {
		return nil
	}
	count := version/7 + 2
	step := 26
	if version != 32 {
		step = (version*4 + count*2 + 1) / (count*2 - 2) * 2
	}
	result := make([]int, count)
	result[0] = 6
	position := version*4 + 10
	for i := count - 1; i >= 1; i-- {
		result[i] = position
		position -= step
	}
	return result
}

func (m *qrMatrix) drawFormatBits(mask int) {
	data := qrErrorCorrectionM<<3 | mask
	remainder := data
	for i := 0; i < 10; i++ {
		remainder = remainder<<1 ^ (remainder>>9)*0x537
	}
	bits := (data<<10 | remainder) ^ 0x5412
	bit := func(i int) bool { return (bits>>i)&1 != 0 }
	for i := 0; i <= 5; i++ {
		m.setFunction(8, i, bit(i))
	}
	m.setFunction(8, 7, bit(6))
	m.setFunction(8, 8, bit(7))
	m.setFunction(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		m.setFunction(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		m.setFunction(m.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		m.setFunction(8, m.size-15+i, bit(i))
	}
	m.setFunction(8, m.size-8, true)
}

func (m *qrMatrix) drawCodewords(data []byte) {
	index := 0
	for right := m.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vertical := 0; vertical < m.size; vertical++ {
			for j := 0; j < 2; j++ {
				x := right - j
				y := vertical
				if (right+1)&2 == 0 {
					y = m.size - 1 - vertical
				}
				if !m.function[y][x] && index < len(data)*8 {
					m.modules[y][x] = (data[index>>3]>>(7-index&7))&1 != 0
					index++
				}
			}
		}
	}
}

func (m *qrMatrix) applyMask(mask int) {
	for y := 0; y < m.size; y++ {
		for x := 0; x < m.size; x++ {
			if m.function[y][x] {
				continue
			}
			invert := false
			switch mask {
			case 0:
				invert = (x+y)%2 == 0
			case 1:
				invert = y%2 == 0
			case 2:
				invert = x%3 == 0
			case 3:
				invert = (x+y)%3 == 0
			case 4:
				invert = (x/3+y/2)%2 == 0
			case 5:
				invert = x*y%2+x*y%3 == 0
			case 6:
				invert = (x*y%2+x*y%3)%2 == 0
			case 7:
				invert = ((x+y)%2+x*y%3)%2 == 0
			}
			m.modules[y][x] = m.modules[y][x] != invert
		}
	}
}

func (m *qrMatrix) penalty() int {
	score := 0
	dark := 0
	for y := 0; y < m.size; y++ {
		runColor := false
		runLength := 0
		for x := 0; x < m.size; x++ {
			value := m.modules[y][x]
			if value {
				dark++
			}
			if x == 0 || value != runColor {
				if runLength >= 5 {
					score += runLength - 2
				}
				runColor, runLength = value, 1
			} else {
				runLength++
			}
		}
		if runLength >= 5 {
			score += runLength - 2
		}
	}
	for x := 0; x < m.size; x++ {
		runColor := false
		runLength := 0
		for y := 0; y < m.size; y++ {
			value := m.modules[y][x]
			if y == 0 || value != runColor {
				if runLength >= 5 {
					score += runLength - 2
				}
				runColor, runLength = value, 1
			} else {
				runLength++
			}
		}
		if runLength >= 5 {
			score += runLength - 2
		}
	}
	for y := 0; y < m.size-1; y++ {
		for x := 0; x < m.size-1; x++ {
			value := m.modules[y][x]
			if m.modules[y][x+1] == value && m.modules[y+1][x] == value && m.modules[y+1][x+1] == value {
				score += 3
			}
		}
	}
	patterns := []uint16{0x05d, 0x5d0}
	for y := 0; y < m.size; y++ {
		for x := 0; x <= m.size-11; x++ {
			value := uint16(0)
			for i := 0; i < 11; i++ {
				value <<= 1
				if m.modules[y][x+i] {
					value |= 1
				}
			}
			if value == patterns[0] || value == patterns[1] {
				score += 40
			}
		}
	}
	for x := 0; x < m.size; x++ {
		for y := 0; y <= m.size-11; y++ {
			value := uint16(0)
			for i := 0; i < 11; i++ {
				value <<= 1
				if m.modules[y+i][x] {
					value |= 1
				}
			}
			if value == patterns[0] || value == patterns[1] {
				score += 40
			}
		}
	}
	total := m.size * m.size
	score += absInt(dark*20-total*10) / total * 10
	return score
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
