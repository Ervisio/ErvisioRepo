package avatars

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"  // registers the GIF decoder
	_ "image/jpeg" // registers the JPEG decoder
	"image/png"
)

// Limits of an uploaded image.
const (
	// MaxUploadBytes is the largest accepted upload.
	MaxUploadBytes = 2 << 20
	// maxSide bounds the decoded dimensions, so a small file cannot expand
	// into a huge bitmap.
	maxSide = 4096
	// Size is the side of the stored square avatar, in pixels.
	Size = 460
)

// ErrInvalidImage is returned for a body that is not a PNG, JPEG or GIF image
// within the limits.
var ErrInvalidImage = errors.New("avatar must be a PNG, JPEG or GIF image of at most 4096x4096 pixels")

// normalize decodes a PNG, JPEG or GIF image (the first frame of a GIF),
// crops it to a centred square, scales it to Size x Size and encodes it as
// PNG. Re-encoding drops every metadata chunk of the original.
func normalize(data []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidImage, err)
	}
	if format != "png" && format != "jpeg" && format != "gif" {
		return nil, fmt.Errorf("%w: format %s", ErrInvalidImage, format)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxSide || cfg.Height > maxSide {
		return nil, fmt.Errorf("%w: %dx%d", ErrInvalidImage, cfg.Width, cfg.Height)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidImage, err)
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	if side <= 0 {
		return nil, ErrInvalidImage
	}
	// Premultiplied RGBA keeps the average of transparent pixels right.
	sq := image.NewRGBA(image.Rect(0, 0, side, side))
	origin := image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2)
	draw.Draw(sq, sq.Bounds(), src, origin, draw.Src)

	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, scale(sq, Size)); err != nil {
		return nil, fmt.Errorf("encode avatar: %w", err)
	}
	return buf.Bytes(), nil
}

// scale resizes the square src to n x n. Each output pixel is the average of
// the source pixels it covers (a box filter), or the nearest one when
// enlarging.
func scale(src *image.RGBA, n int) *image.RGBA {
	side := src.Bounds().Dx()
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	span := func(i int) (int, int) {
		lo, hi := i*side/n, (i+1)*side/n
		if hi <= lo {
			hi = lo + 1
		}
		return lo, hi
	}
	for y := range n {
		y0, y1 := span(y)
		for x := range n {
			x0, x1 := span(x)
			var r, g, bl, a, cnt uint32
			for sy := y0; sy < y1; sy++ {
				row := src.Pix[sy*src.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					r += uint32(p[0])
					g += uint32(p[1])
					bl += uint32(p[2])
					a += uint32(p[3])
					cnt++
				}
			}
			o := dst.Pix[y*dst.Stride+x*4:]
			o[0], o[1], o[2], o[3] = avg(r, cnt), avg(g, cnt), avg(bl, cnt), avg(a, cnt)
		}
	}
	return dst
}

// avg is the mean of cnt byte values summing to sum, which fits in a byte.
func avg(sum, cnt uint32) uint8 {
	return uint8(min(sum/cnt, 255)) //nolint:gosec // bounded by min
}
