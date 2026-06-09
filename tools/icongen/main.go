package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

func main() {
	root := filepath.Join("packaging", "icons")
	if err := os.MkdirAll(root, 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(root, "OneSync.svg"), []byte(svgIcon), 0o644); err != nil {
		panic(err)
	}
	if err := writeICO(filepath.Join(root, "OneSync.ico"), []int{16, 32, 48, 64, 128, 256}); err != nil {
		panic(err)
	}
}

func writeICO(path string, sizes []int) error {
	images := make([][]byte, 0, len(sizes))
	for _, size := range sizes {
		var buffer bytes.Buffer
		if err := png.Encode(&buffer, drawIcon(size)); err != nil {
			return err
		}
		images = append(images, buffer.Bytes())
	}

	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(images)))
	offset := uint32(6 + len(images)*16)
	for index, data := range images {
		size := sizes[index]
		width := byte(size)
		height := byte(size)
		if size >= 256 {
			width = 0
			height = 0
		}
		out.WriteByte(width)
		out.WriteByte(height)
		out.WriteByte(0)
		out.WriteByte(0)
		_ = binary.Write(&out, binary.LittleEndian, uint16(1))
		_ = binary.Write(&out, binary.LittleEndian, uint16(32))
		_ = binary.Write(&out, binary.LittleEndian, uint32(len(data)))
		_ = binary.Write(&out, binary.LittleEndian, offset)
		offset += uint32(len(data))
	}
	for _, data := range images {
		out.Write(data)
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

func drawIcon(size int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := float64(size) / 256
	blue := color.NRGBA{R: 35, G: 122, B: 235, A: 255}
	light := color.NRGBA{R: 237, G: 247, B: 255, A: 255}
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	drawCircle(img, 74, 116, 44, scale, light)
	drawCircle(img, 118, 92, 58, scale, light)
	drawCircle(img, 166, 118, 48, scale, light)
	drawRoundedRect(img, 54, 112, 196, 168, 28, scale, light)
	drawCircle(img, 74, 116, 44, scale, blue)
	drawCircle(img, 118, 92, 58, scale, blue)
	drawCircle(img, 166, 118, 48, scale, blue)
	drawRoundedRect(img, 54, 112, 196, 168, 28, scale, blue)
	drawArrow(img, []point{{92, 136}, {124, 112}, {155, 112}}, scale, white)
	drawArrow(img, []point{{164, 126}, {132, 150}, {101, 150}}, scale, white)
	return img
}

type point struct{ x, y float64 }

func drawCircle(img *image.NRGBA, cx, cy, radius float64, scale float64, c color.NRGBA) {
	minX := int((cx - radius) * scale)
	maxX := int((cx + radius) * scale)
	minY := int((cy - radius) * scale)
	maxY := int((cy + radius) * scale)
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			dx := float64(x)/scale - cx
			dy := float64(y)/scale - cy
			if dx*dx+dy*dy <= radius*radius {
				set(img, x, y, c)
			}
		}
	}
}

func drawRoundedRect(img *image.NRGBA, x1, y1, x2, y2, radius float64, scale float64, c color.NRGBA) {
	for y := int(y1 * scale); y <= int(y2*scale); y++ {
		for x := int(x1 * scale); x <= int(x2*scale); x++ {
			ux := float64(x) / scale
			uy := float64(y) / scale
			cx := math.Max(x1+radius, math.Min(ux, x2-radius))
			cy := math.Max(y1+radius, math.Min(uy, y2-radius))
			if (ux-cx)*(ux-cx)+(uy-cy)*(uy-cy) <= radius*radius {
				set(img, x, y, c)
			}
		}
	}
}

func drawArrow(img *image.NRGBA, pts []point, scale float64, c color.NRGBA) {
	for i := 0; i < len(pts)-1; i++ {
		drawLine(img, pts[i], pts[i+1], 12, scale, c)
	}
	head := pts[len(pts)-1]
	dir := point{head.x - pts[len(pts)-2].x, head.y - pts[len(pts)-2].y}
	length := math.Hypot(dir.x, dir.y)
	if length == 0 {
		return
	}
	dir.x /= length
	dir.y /= length
	left := point{head.x - dir.x*24 - dir.y*14, head.y - dir.y*24 + dir.x*14}
	right := point{head.x - dir.x*24 + dir.y*14, head.y - dir.y*24 - dir.x*14}
	fillTriangle(img, head, left, right, scale, c)
}

func drawLine(img *image.NRGBA, a, b point, width float64, scale float64, c color.NRGBA) {
	minX := int((math.Min(a.x, b.x) - width) * scale)
	maxX := int((math.Max(a.x, b.x) + width) * scale)
	minY := int((math.Min(a.y, b.y) - width) * scale)
	maxY := int((math.Max(a.y, b.y) + width) * scale)
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			p := point{float64(x) / scale, float64(y) / scale}
			if distanceToSegment(p, a, b) <= width/2 {
				set(img, x, y, c)
			}
		}
	}
}

func fillTriangle(img *image.NRGBA, a, b, cpt point, scale float64, c color.NRGBA) {
	minX := int(math.Min(a.x, math.Min(b.x, cpt.x)) * scale)
	maxX := int(math.Max(a.x, math.Max(b.x, cpt.x)) * scale)
	minY := int(math.Min(a.y, math.Min(b.y, cpt.y)) * scale)
	maxY := int(math.Max(a.y, math.Max(b.y, cpt.y)) * scale)
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			p := point{float64(x) / scale, float64(y) / scale}
			if insideTriangle(p, a, b, cpt) {
				set(img, x, y, c)
			}
		}
	}
}

func distanceToSegment(p, a, b point) float64 {
	dx := b.x - a.x
	dy := b.y - a.y
	if dx == 0 && dy == 0 {
		return math.Hypot(p.x-a.x, p.y-a.y)
	}
	t := ((p.x-a.x)*dx + (p.y-a.y)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(p.x-(a.x+t*dx), p.y-(a.y+t*dy))
}

func insideTriangle(p, a, b, c point) bool {
	d1 := sign(p, a, b)
	d2 := sign(p, b, c)
	d3 := sign(p, c, a)
	return !((d1 < 0 || d2 < 0 || d3 < 0) && (d1 > 0 || d2 > 0 || d3 > 0))
}

func sign(p1, p2, p3 point) float64 {
	return (p1.x-p3.x)*(p2.y-p3.y) - (p2.x-p3.x)*(p1.y-p3.y)
}

func set(img *image.NRGBA, x, y int, c color.NRGBA) {
	if !image.Pt(x, y).In(img.Rect) {
		return
	}
	img.SetNRGBA(x, y, c)
}

const svgIcon = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256">
  <path fill="#237aeb" d="M54 168h142a48 48 0 0 0 7-95 58 58 0 0 0-109-18 44 44 0 0 0-59 41 45 45 0 0 0 19 72z"/>
  <path fill="#fff" d="M151 99l38 30-38 30v-20h-44a10 10 0 0 1 0-20h44V99zM105 157l-38-30 38-30v20h44a10 10 0 0 1 0 20h-44v20z"/>
</svg>
`
