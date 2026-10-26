package imgproc

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
)

func solid(w, h int) *image.Paletted {
	pal := color.Palette{color.White, color.Black}
	img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if (x/8+y/8)%2 == 0 {
				img.SetColorIndex(x, y, 1)
			}
		}
	}
	return img
}

func encodeGIF(t *testing.T, frames int) []byte {
	t.Helper()
	g := &gif.GIF{}
	for i := 0; i < frames; i++ {
		g.Image = append(g.Image, solid(64, 48))
		g.Delay = append(g.Delay, 10)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

// GIF 以前在 allowedExt 里声明支持，但解码器没注册，用户上传必然报「无法解析图片内容」。
func TestProcessGIFProducesJPEG(t *testing.T) {
	res, err := Process(encodeGIF(t, 1), "gif")
	if err != nil {
		t.Fatalf("gif 应能处理，实际报错: %v", err)
	}
	if res.Ext != "jpg" {
		t.Fatalf("gif 应转存为 jpg，实际 %q", res.Ext)
	}
	if res.Width != 64 || res.Height != 48 {
		t.Fatalf("尺寸应保持 64x48，实际 %dx%d", res.Width, res.Height)
	}
	if len(res.Original) == 0 || len(res.Preview) == 0 || len(res.Thumb) == 0 {
		t.Fatal("原图/预览/缩略图都不应为空")
	}
	if !bytes.HasPrefix(res.Original, []byte{0xFF, 0xD8}) {
		t.Fatal("输出应为 JPEG（FFD8 开头）")
	}
}

// 动图只取第一帧，不报错。
func TestProcessAnimatedGIFFirstFrame(t *testing.T) {
	if _, err := Process(encodeGIF(t, 3), "gif"); err != nil {
		t.Fatalf("多帧 gif 应取第一帧处理，实际报错: %v", err)
	}
}

func TestProcessPNGKeepsPNG(t *testing.T) {
	pal := solid(40, 30)
	var buf bytes.Buffer
	if err := png.Encode(&buf, pal); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	res, err := Process(buf.Bytes(), "png")
	if err != nil {
		t.Fatalf("png 处理失败: %v", err)
	}
	if res.Ext != "png" {
		t.Fatalf("png 应保持 png，实际 %q", res.Ext)
	}
}

func TestProcessRejectsGarbage(t *testing.T) {
	if _, err := Process([]byte("not an image"), "jpg"); err == nil {
		t.Fatal("非图片内容应被拒绝")
	}
}
