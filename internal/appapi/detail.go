package appapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Gender 是站点给演员的性别：0 = 女优，1 = 男优（实测 144 位收藏里 138/6 的分布）。
//
// 其它取值一律 GenderUnknown，不猜：字段图把「不知道」写成 NULL，
// 而不是替上游挑一个最常见的。
type Gender int

const (
	// GenderFemale 是站点上的 0。
	GenderFemale Gender = 0
	// GenderMale 是站点上的 1。
	GenderMale Gender = 1
	// GenderUnknown 是缺失/null/不认识的取值。
	GenderUnknown Gender = -1
)

// UnmarshalJSON 只认数字（站点当前形态：0/1）；缺失、null 与任何其它形态都是
// GenderUnknown。
//
// 解析失败**不报错**：演员性别缺一个不值得让整次刮削失败，字段图会把
// GenderUnknown 写成 NULL（规格 §7.1 的 JavDB 列缺失留空）。不为漂移预先编一套
// 字符串词表——那只是把猜测从运行时搬到代码里。
func (g *Gender) UnmarshalJSON(data []byte) error {
	var number int
	if err := json.Unmarshal(data, &number); err != nil {
		*g = GenderUnknown
		return nil
	}
	*g = Gender(number)
	return nil
}

// Actor 是详情里的一位演员。
//
// ID 是 JavDB 的演员 id（actor 表主键），头像 URL 直写 NFO 的 <thumb>：
// v1 不下载头像、也不抓演员详情页（P15-2）。
type Actor struct {
	ID        string
	Name      string
	Gender    Gender
	AvatarURL string
}

// Tag 是详情里的一个标签（<tag> 写它，**不写 <genre>**）。
type Tag struct {
	ID   string
	Name string
}

// PreviewImage 是一张预览图（extrafanart 的 JavDB 回退源）。
type PreviewImage struct {
	ThumbURL string
	LargeURL string
}

// MovieDetail 是 `/api/v4/movies/{id}` 的载荷，摊成字段图的 JavDB 侧取值。
//
// 它是**传输形状**：只做字段改名与类型落定，不做任何「没有就换一个」的决定
// （字段级回退归 scrape 的字段图，那边才是 §7.1 的唯一实现）。
type MovieDetail struct {
	ID          string
	Number      string
	Title       string
	OriginTitle string
	Summary     string
	ThumbURL    string
	CoverURL    string
	// DurationMinutes 是站点给的分钟数（这个端点的 duration 是分钟，与 DMM 的秒不同）。
	DurationMinutes int
	// Score 是站点的**字符串**评分（5 分制，如 "4.43"）。保留原文，
	// ×2 换成 10 分制是字段图的事。
	Score         string
	ReleaseDate   string
	MakerName     string
	PublisherName string
	SeriesName    string
	DirectorName  string
	// TrailerURL 是站点的预览视频地址（preview_video_url）；没有则为空。
	TrailerURL string
	Tags       []Tag
	Actors     []Actor
	Preview    []PreviewImage
}

// DetailResult 是一次详情读取：值 + data 原文。
//
// 两者一起走，是因为 raw_snapshot 的「逐源原样」需要原文，而调用方要的永远
// 是同一份响应的两个面；把它们拆成两个返回值只会让每个接缝都多一个参数。
type DetailResult struct {
	Movie MovieDetail
	// Raw 是信封里 data 的原文，进 raw_snapshot。
	Raw json.RawMessage
}

// detailEnvelope 是详情端点的 data 形态：`{share_info, show_vip_banner, movie}`。
type detailEnvelope struct {
	Movie detailWire `json:"movie"`
}

// detailWire 是详情端点的线格式（改名/删字段时只改这一处）。
type detailWire struct {
	ID              string `json:"id"`
	Number          string `json:"number"`
	Title           string `json:"title"`
	OriginTitle     string `json:"origin_title"`
	Summary         string `json:"summary"`
	ThumbURL        string `json:"thumb_url"`
	CoverURL        string `json:"cover_url"`
	DurationMinutes int    `json:"duration"`
	Score           string `json:"score"`
	ReleaseDate     string `json:"release_date"`
	MakerName       string `json:"maker_name"`
	PublisherName   string `json:"publisher_name"`
	SeriesName      string `json:"series_name"`
	DirectorName    string `json:"director_name"`
	PreviewVideoURL string `json:"preview_video_url"`

	Tags []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"tags"`
	Actors []struct {
		ID        string  `json:"id"`
		Name      string  `json:"name"`
		Gender    *Gender `json:"gender"`
		AvatarURL string  `json:"avatar_url"`
	} `json:"actors"`
	PreviewImages []struct {
		ThumbURL string `json:"thumb_url"`
		LargeURL string `json:"large_url"`
	} `json:"preview_images"`
}

// Detail 读一部作品的详情（`GET /api/v4/movies/{id}?from_rankings=false`）。
//
// token 匿名可读（app-API 的详情不需要用户域），在场时携带。
func (c *Client) Detail(ctx context.Context, movieID string) (DetailResult, error) {
	id := strings.TrimSpace(movieID)
	if id == "" {
		return DetailResult{}, &ValidationError{Message: "读详情需要 movie_id"}
	}

	path := "/api/v4/movies/" + url.PathEscape(id)
	params := url.Values{"from_rankings": {"false"}}

	req, err := c.newRequest(ctx, "GET", path, params, nil)
	if err != nil {
		return DetailResult{}, err
	}
	raw, err := c.do(req, path)
	if err != nil {
		return DetailResult{}, err
	}
	if len(raw) == 0 {
		// success:1 但没有 data：上游说「没有这一条」，但详情端点的正常形态不是这样。
		// 归 protocol，让它可见（静默当成空详情会让一部片悄悄缺所有 JavDB 字段）。
		return DetailResult{}, &ProtocolError{Path: path, Message: "详情没有 data"}
	}

	var envelope detailEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return DetailResult{}, fmt.Errorf("解析 %s 负载: %w", path, err)
	}
	return DetailResult{Movie: movieOf(envelope.Movie), Raw: raw}, nil
}

// movieOf 把线格式摊成 MovieDetail。
func movieOf(wire detailWire) MovieDetail {
	movie := MovieDetail{
		ID:              strings.TrimSpace(wire.ID),
		Number:          strings.TrimSpace(wire.Number),
		Title:           strings.TrimSpace(wire.Title),
		OriginTitle:     strings.TrimSpace(wire.OriginTitle),
		Summary:         strings.TrimSpace(wire.Summary),
		ThumbURL:        strings.TrimSpace(wire.ThumbURL),
		CoverURL:        strings.TrimSpace(wire.CoverURL),
		DurationMinutes: wire.DurationMinutes,
		Score:           strings.TrimSpace(wire.Score),
		ReleaseDate:     strings.TrimSpace(wire.ReleaseDate),
		MakerName:       strings.TrimSpace(wire.MakerName),
		PublisherName:   strings.TrimSpace(wire.PublisherName),
		SeriesName:      strings.TrimSpace(wire.SeriesName),
		DirectorName:    strings.TrimSpace(wire.DirectorName),
		TrailerURL:      strings.TrimSpace(wire.PreviewVideoURL),
	}
	for _, tag := range wire.Tags {
		if name := strings.TrimSpace(tag.Name); name != "" {
			movie.Tags = append(movie.Tags, Tag{ID: strings.TrimSpace(tag.ID), Name: name})
		}
	}
	for _, actor := range wire.Actors {
		name := strings.TrimSpace(actor.Name)
		if name == "" {
			continue
		}
		gender := GenderUnknown
		if actor.Gender != nil {
			gender = *actor.Gender
		}
		movie.Actors = append(movie.Actors, Actor{
			ID:        strings.TrimSpace(actor.ID),
			Name:      name,
			Gender:    gender,
			AvatarURL: strings.TrimSpace(actor.AvatarURL),
		})
	}
	for _, preview := range wire.PreviewImages {
		if preview.LargeURL == "" && preview.ThumbURL == "" {
			continue
		}
		movie.Preview = append(movie.Preview, PreviewImage{
			ThumbURL: strings.TrimSpace(preview.ThumbURL),
			LargeURL: strings.TrimSpace(preview.LargeURL),
		})
	}
	return movie
}
