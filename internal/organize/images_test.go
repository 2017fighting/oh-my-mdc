package organize

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 测试缝：海报几何与图片下载（规格 §5.3 右裁 2:3 / §7.1 扩展名随源）。

// makeJPEG 造一张横向渐变图：左半黑、右半白，用来验证「右裁」真的取了右侧。
func makeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x < width/2 {
				img.Set(x, y, color.RGBA{R: 0, G: 0, B: 0, A: 255})
			} else {
				img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buffer.Bytes()
}

// TestCropRectTakesTheRightTwoThirds 钉住右裁几何：宽>高时取最右边的 2:3 竖条。
func TestCropRectTakesTheRightTwoThirds(t *testing.T) {
	rect := cropRect(image.Rect(0, 0, 1200, 800))
	if rect.Dx() != 533 || rect.Dy() != 800 {
		t.Fatalf("rect = %v (%dx%d), want 533x800", rect, rect.Dx(), rect.Dy())
	}
	if rect.Max.X != 1200 {
		t.Errorf("rect.Max.X = %d, want 1200（贴着右边裁）", rect.Max.X)
	}
	if rect.Min.X != 667 {
		t.Errorf("rect.Min.X = %d, want 667", rect.Min.X)
	}
}

// TestCropRectFallsBackForVerticalSources 钉住几何兜底：竖版源退化成整宽 + 居中竖裁。
func TestCropRectFallsBackForVerticalSources(t *testing.T) {
	rect := cropRect(image.Rect(0, 0, 400, 900))
	if rect.Dx() != 400 || rect.Dy() != 600 {
		t.Fatalf("rect = %v (%dx%d), want 400x600", rect, rect.Dx(), rect.Dy())
	}
	if rect.Min.Y != 150 {
		t.Errorf("rect.Min.Y = %d, want 150（居中）", rect.Min.Y)
	}
}

// TestCropPosterKeepsTheRightHalf 钉住「裁的是右边、编码回原格式」。
func TestCropPosterKeepsTheRightHalf(t *testing.T) {
	poster, err := cropPoster(makeJPEG(t, 1200, 800))
	if err != nil {
		t.Fatalf("cropPoster: %v", err)
	}

	img, format, err := image.Decode(bytes.NewReader(poster))
	if err != nil {
		t.Fatalf("解裁剪结果: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("format = %q, want jpeg（扩展名随源、不转格式）", format)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 533 || bounds.Dy() != 800 {
		t.Fatalf("poster = %dx%d, want 533x800", bounds.Dx(), bounds.Dy())
	}

	// 源图右半是白的：右裁结果应该整片接近白（取错了就会是黑）。
	r, g, b, _ := img.At(bounds.Dx()/2, bounds.Dy()/2).RGBA()
	if r>>8 < 200 || g>>8 < 200 || b>>8 < 200 {
		t.Errorf("裁剪结果中心像素 = (%d,%d,%d)，看起来裁到了左半", r>>8, g>>8, b>>8)
	}
}

// TestCropPosterCanEncodePNG 钉住 png 源也能裁（扩展名随源）。
func TestCropPosterCanEncodePNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	poster, err := cropPoster(buffer.Bytes())
	if err != nil {
		t.Fatalf("cropPoster: %v", err)
	}
	if _, format, err := image.Decode(bytes.NewReader(poster)); err != nil || format != "png" {
		t.Fatalf("format = %q, err = %v, want png", format, err)
	}
}

// TestCropPosterFailsOnUndecodableData 钉住「裁剪失败」这一档（调用方按部分成功处理）。
func TestCropPosterFailsOnUndecodableData(t *testing.T) {
	if _, err := cropPoster([]byte("not an image")); err == nil {
		t.Fatal("非图片数据竟然裁成功了")
	}
}

// TestImageExtensionFollowsTheSourceURL 钉住扩展名随源 URL（不转格式）。
func TestImageExtensionFollowsTheSourceURL(t *testing.T) {
	cases := map[string]string{
		"https://pics.dmm.co.jp/mono/movie/adult/ssis001/ssis001pl.jpg": ".jpg",
		"https://example.test/a.PNG?token=1":                            ".png",
		"https://example.test/a.webp":                                   ".webp",
		"https://example.test/no-extension":                             ".jpg",
		"://broken":                                                     ".jpg",
	}
	for rawURL, want := range cases {
		if got := imageExtension(rawURL); got != want {
			t.Errorf("imageExtension(%q) = %q, want %q", rawURL, got, want)
		}
	}
}

// TestIsLandscapePicksTheWideSleeve 钉住 pl/ps 的判据：宽>高者才是横版。
func TestIsLandscapePicksTheWideSleeve(t *testing.T) {
	if !isLandscape(makeJPEG(t, 1200, 800)) {
		t.Error("1200x800 应该判成横版")
	}
	if isLandscape(makeJPEG(t, 800, 1200)) {
		t.Error("800x1200 不该判成横版")
	}
}

// TestHTTPImagesDownloads 钉住真实下载实现：2xx 给字节，非 2xx 报错。
func TestHTTPImagesDownloads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.jpg":
			_, _ = w.Write([]byte("bytes"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	images := NewHTTPImages(server.Client())
	body, err := images.Get(context.Background(), server.URL+"/ok.jpg")
	if err != nil || string(body) != "bytes" {
		t.Fatalf("Get(ok) = (%q, %v)", body, err)
	}
	if _, err := images.Get(context.Background(), server.URL+"/missing.jpg"); err == nil {
		t.Fatal("404 竟然算成功")
	}
}
