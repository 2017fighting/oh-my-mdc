package organize

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// 图片落盘（规格 §7.1 图片行 / §5.3 海报几何）。
//
// 三件事在这里固定：
//
//   - **poster 是右裁 2:3**：在横版 sleeve（pl/ps 里宽>高的那张）上取最右边的 2:3 竖版；
//   - **fanart 是原横版**：不裁，扩展名随源 URL，不转格式；
//   - **裁剪失败只算部分成功**（mdc-lite ADR-0015 机制）：解码不了就跳过这张，
//     记录告警，任务照常入库。
//
// pl/ps 哪张是横版在规格 §13 里是未实测的悬案，所以由算法判（宽>高者），
// 都不横时取第一张并退化成一刀竖向居中裁（几何兜底，见 cropRect）。

// DefaultImageTimeout 是单张图片下载的上限。
const DefaultImageTimeout = 30 * time.Second

// maxImageBytes 挡住异常响应把一张图读进内存（10 MiB 足够 sleeve 与预览）。
const maxImageBytes = 10 << 20

// ImageFetcher 是图片下载接缝（真实实现是 HTTP GET；测试注入假实现）。
type ImageFetcher interface {
	Get(ctx context.Context, url string) ([]byte, error)
}

// HTTPImages 是图片下载的真实实现。
type HTTPImages struct {
	client *http.Client
}

// NewHTTPImages 装配图片下载；client 为空时用带超时的默认客户端。
func NewHTTPImages(client *http.Client) *HTTPImages {
	if client == nil {
		client = &http.Client{Timeout: DefaultImageTimeout}
	}
	return &HTTPImages{client: client}
}

// Get 下载一张图。非 2xx、空响应、超大都算失败（调用方按部分成功处理）。
func (h *HTTPImages) Get(ctx context.Context, rawURL string) ([]byte, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, errors.New("organize: 图片 URL 为空")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("organize: 构造图片请求 %s: %w", rawURL, err)
	}
	response, err := h.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("organize: 下载图片 %s: %w", rawURL, err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("organize: 下载图片 %s: HTTP %d", rawURL, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("organize: 读图片 %s: %w", rawURL, err)
	}
	if len(body) > maxImageBytes {
		return nil, fmt.Errorf("organize: 图片 %s 超过 %d 字节上限", rawURL, maxImageBytes)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("organize: 图片 %s 是空响应", rawURL)
	}
	return body, nil
}

// imageExtension 取图片扩展名（随源 URL，不转格式）：认不出就给 .jpg。
func imageExtension(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ".jpg"
	}
	ext := strings.ToLower(path.Ext(parsed.Path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return ext
	}
	return ".jpg"
}

// decodeImage 解码一张图，返回图像与格式（jpeg/png/gif）。
func decodeImage(data []byte) (image.Image, string, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("organize: 解码图片: %w", err)
	}
	return img, format, nil
}

// isLandscape 报告这张图是不是横版（宽>高）：sleeve 选择的判据（规格 §5.3）。
func isLandscape(data []byte) bool {
	img, _, err := decodeImage(data)
	if err != nil {
		return false
	}
	bounds := img.Bounds()
	return bounds.Dx() > bounds.Dy()
}

// cropPoster 把一张图右裁成 2:3 竖版，并按源格式重新编码。
//
// 源格式必须是能编码回去的那三种（jpeg/png/gif）；webp 之类解码/编码不支持时返回错误，
// 调用方按「裁剪失败=部分成功」处理（fanart 照样落盘）。
func cropPoster(data []byte) ([]byte, error) {
	img, format, err := decodeImage(data)
	if err != nil {
		return nil, err
	}
	cropped := cropRight(img, cropRect(img.Bounds()))
	return encodeImage(cropped, format)
}

// cropRect 是右裁 2:3 的几何：
//
//   - 源比 2:3 宽（横版 sleeve）→ 取最右边那一竖条，宽 = 高×2/3（右裁）；
//   - 源比 2:3 窄（竖版）→ 右裁无从谈起，退化成整宽 + 竖向居中裁（几何兜底）。
func cropRect(bounds image.Rectangle) image.Rectangle {
	width, height := bounds.Dx(), bounds.Dy()
	targetWidth := height * 2 / 3
	if targetWidth <= 0 {
		targetWidth = 1
	}

	if width >= targetWidth {
		// 右裁：右边那一条（保留源图右侧，标题/封面主体通常在这里）。
		return image.Rect(bounds.Max.X-targetWidth, bounds.Min.Y, bounds.Max.X, bounds.Max.Y)
	}

	targetHeight := width * 3 / 2
	if targetHeight <= 0 {
		targetHeight = 1
	}
	if targetHeight > height {
		targetHeight = height
	}
	top := bounds.Min.Y + (height-targetHeight)/2
	return image.Rect(bounds.Min.X, top, bounds.Max.X, top+targetHeight)
}

// cropRight 按矩形裁一份（copy 到新图，不共享源图的底层数组）。
func cropRight(img image.Image, rect image.Rectangle) image.Image {
	rect = rect.Intersect(img.Bounds())
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	for y := 0; y < rect.Dy(); y++ {
		for x := 0; x < rect.Dx(); x++ {
			dst.Set(x, y, img.At(rect.Min.X+x, rect.Min.Y+y))
		}
	}
	return dst
}

// encodeImage 按格式编码；jpeg 用较高画质（海报是展示图）。
func encodeImage(img image.Image, format string) ([]byte, error) {
	var buffer bytes.Buffer
	var err error
	switch format {
	case "jpeg":
		err = jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 92})
	case "png":
		err = png.Encode(&buffer, img)
	case "gif":
		err = gif.Encode(&buffer, img, nil)
	default:
		return nil, fmt.Errorf("organize: 不认识的图片格式 %q", format)
	}
	if err != nil {
		return nil, fmt.Errorf("organize: 编码图片: %w", err)
	}
	return buffer.Bytes(), nil
}
