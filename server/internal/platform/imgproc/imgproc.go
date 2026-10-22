package imgproc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"

	_ "golang.org/x/image/webp"

	xdraw "golang.org/x/image/draw"
)

// Result holds the normalized original plus the two derived sizes.
type Result struct {
	Ext      string
	Original []byte
	Preview  []byte
	Thumb    []byte
	Width    int
	Height   int
}

const (
	maxOriginal = 2400
	previewSide = 1280
	thumbSide   = 420
)

// Process decodes, applies EXIF orientation, caps the long edge, strips metadata
// (re-encoding drops EXIF/GPS) and produces preview/thumb JPEGs.
func Process(raw []byte, origExt string) (*Result, error) {
	src, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("无法解析图片内容，请确认是有效图片")
	}
	img := applyOrientation(src, exifOrientation(raw))
	b := img.Bounds()

	if b.Dx() > maxOriginal || b.Dy() > maxOriginal {
		img = resize(img, maxOriginal)
		b = img.Bounds()
	}

	// Composite on white so transparent PNGs never turn black in JPEG.
	flat := flatWhite(img)
	b = flat.Bounds()

	res := &Result{Width: b.Dx(), Height: b.Dy()}
	if format == "png" || origExt == "png" {
		res.Ext = "png"
		var buf bytes.Buffer
		if err := png.Encode(&buf, flat); err != nil {
			return nil, err
		}
		res.Original = buf.Bytes()
	} else {
		res.Ext = "jpg"
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: 90}); err != nil {
			return nil, err
		}
		res.Original = buf.Bytes()
	}

	if res.Preview, err = encodeJPEG(resize(flat, previewSide), 82); err != nil {
		return nil, err
	}
	if res.Thumb, err = encodeJPEG(resize(flat, thumbSide), 78); err != nil {
		return nil, err
	}
	return res, nil
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func flatWhite(src image.Image) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(image.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	return dst
}

func resize(src image.Image, side int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= side && h <= side {
		return src
	}
	scale := float64(side) / float64(w)
	if h > w {
		scale = float64(side) / float64(h)
	}
	nw, nh := int(float64(w)*scale+0.5), int(float64(h)*scale+0.5)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

// applyOrientation returns an upright copy for EXIF orientation 1..8.
func applyOrientation(src image.Image, orientation int) image.Image {
	if orientation <= 1 || orientation > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for dy := 0; dy < dh; dy++ {
		for dx := 0; dx < dw; dx++ {
			var sx, sy int
			switch orientation {
			case 2:
				sx, sy = w-1-dx, dy
			case 3:
				sx, sy = w-1-dx, h-1-dy
			case 4:
				sx, sy = dx, h-1-dy
			case 5:
				sx, sy = dy, dx
			case 6:
				sx, sy = dy, h-1-dx
			case 7:
				sx, sy = w-1-dy, h-1-dx
			case 8:
				sx, sy = w-1-dy, dx
			}
			r, g, bl, a := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
			off := dst.PixOffset(dx, dy)
			dst.Pix[off+0] = uint8(r >> 8)
			dst.Pix[off+1] = uint8(g >> 8)
			dst.Pix[off+2] = uint8(bl >> 8)
			dst.Pix[off+3] = uint8(a >> 8)
		}
	}
	return dst
}

// exifOrientation reads tag 0x0112 from the first APP1 segment of a JPEG.
func exifOrientation(raw []byte) int {
	if len(raw) < 4 || raw[0] != 0xFF || raw[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(raw) {
		if raw[i] != 0xFF {
			return 1
		}
		marker := raw[i+1]
		if marker == 0xDA || marker == 0xD9 {
			return 1
		}
		segLen := int(binary.BigEndian.Uint16(raw[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(raw) {
			return 1
		}
		if marker == 0xE1 {
			seg := raw[i+4 : i+2+segLen]
			if len(seg) > 6 && string(seg[:4]) == "Exif" {
				return tiffOrientation(seg[6:])
			}
		}
		i += 2 + segLen
	}
	return 1
}

func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	ifdOff := int(order.Uint32(tiff[4:8]))
	if ifdOff <= 0 || ifdOff+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[ifdOff : ifdOff+2]))
	pos := ifdOff + 2
	for n := 0; n < count; n++ {
		if pos+12 > len(tiff) {
			return 1
		}
		tag := order.Uint16(tiff[pos : pos+2])
		if tag == 0x0112 {
			return int(order.Uint16(tiff[pos+8 : pos+10]))
		}
		pos += 12
	}
	return 1
}
