package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/settings"
)

// 项目用的两个 tag（规格 §5.4）：qbt 里永远只有这两个，失败不引入第三个。
const (
	// TagInProgress 标记「本项目加的、还没刮削」。
	TagInProgress = "mdc:in-progress"
	// TagFinished 标记「已入库」。
	TagFinished = "mdc:finished"
)

// DefaultCategory 是加种的默认分类（设置表的默认值，见规格 §4.3）。
const DefaultCategory = "mdc"

// defaultHTTPTimeout 是单次调用的上限。测试连接另有 10s 的总预算（settings.TestTimeout）。
const defaultHTTPTimeout = 30 * time.Second

// Config 是一次 qBittorrent 调用要用的设置原文（含凭据，只许在进程内流转）。
type Config struct {
	URL            string
	Username       string
	Password       string
	Category       string
	DownloadRoot   string
	WebhookBaseURL string
}

// ConfigFrom 把设置域的原文翻成调用配置（单点：向导、测试、循环用的是同一份默认值）。
func ConfigFrom(input settingsInput) Config {
	cfg := Config{
		URL:            strings.TrimSpace(input.Text("url")),
		Username:       strings.TrimSpace(input.Text("username")),
		Password:       input.Text("password"),
		Category:       strings.TrimSpace(input.Text("category")),
		DownloadRoot:   strings.TrimSpace(input.Text("download_root")),
		WebhookBaseURL: strings.TrimSpace(input.Text("webhook_base_url")),
	}
	if cfg.Category == "" {
		cfg.Category = DefaultCategory
	}
	return cfg
}

// settingsInput 是 settings.TestInput 里本包用到的那一小片。
//
// 用最小接口而不是直接吃 settings.TestInput：本包不该知道清单登记、enabled 这些
// 与 qBittorrent 无关的东西。
type settingsInput interface {
	Text(name string) string
}

// Preferences 是 qbt 偏好里本项目关心的两项。
//
// 只读这两项、只写 autorun_program：`autorun_on_torrent_added_*` 绝不触碰（ADR-0007）。
type Preferences struct {
	AutorunEnabled bool   `json:"autorun_enabled"`
	AutorunProgram string `json:"autorun_program"`
}

// Autorun 就是偏好里的那两项，名称与规格一致。
func (p Preferences) Autorun() Autorun {
	return Autorun{Enabled: p.AutorunEnabled, Program: p.AutorunProgram}
}

// Properties 是 `torrents/properties` 里本项目判重、收编与完成判定要用的字段。
type Properties struct {
	Hash     string `json:"hash"`
	Name     string `json:"name"`
	SavePath string `json:"save_path"`
	// ContentPath 是种子落盘后的真实路径（文件或目录），完成判定读它。
	ContentPath string `json:"content_path"`
	Category    string `json:"category"`
	// Tags 是 qbt 原样的逗号分隔串（如 "mdc:in-progress, ABP-123"）。
	Tags string `json:"tags"`
}

// HasProjectTag 报告这个种子是不是已经在本项目手里（有任一项目 tag）。
func (p Properties) HasProjectTag() bool {
	return hasProjectTag(p.Tags)
}

// hasProjectTag 是 tag 串判据的单点实现：properties 与 info 两条读法共用。
func hasProjectTag(raw string) bool {
	for _, tag := range splitTags(raw) {
		if tag == TagInProgress || tag == TagFinished {
			return true
		}
	}
	return false
}

// Number 从 properties 的 tag 串里取番号（规格 P7-5：番号从 qbt tag 取，不解析文件名）。
func (p Properties) Number() (string, bool) {
	return NumberFromTags(p.Tags)
}

// NumberFromTags 找出 tag 串里的番号：本项目只写「番号 + 项目 tag」，
// 所以番号就是唯一那个非项目 tag。
func NumberFromTags(raw string) (string, bool) {
	for _, tag := range splitTags(raw) {
		if tag == TagInProgress || tag == TagFinished {
			continue
		}
		return tag, true
	}
	return "", false
}

func splitTags(raw string) []string {
	parts := strings.Split(raw, ",")
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		if tag := strings.TrimSpace(part); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// client 是一个已登录（或即将登录）的 qbt WebUI API v2 客户端。
//
// 只放实测确认过的传输事实：登录拿 SID cookie、请求带 Referer（qbt 的 CSRF 检查）、
// 4.6.5 的纯文本与 5.x 的 JSON 双解析都在这里。
type client struct {
	base *url.URL
	http *http.Client
	sid  string
}

func newClient(cfg Config, httpClient *http.Client) (*client, error) {
	raw := strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	if raw == "" {
		return nil, ValidationError{Message: "qBittorrent 节还没设置地址"}
	}
	base, err := url.Parse(raw)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, ValidationError{Message: "qBittorrent 地址需要 http(s) 地址: " + raw}
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &client{base: base, http: httpClient}, nil
}

// origin 是 Referer 要用的值：qbt 会校验写请求的 Referer 与 WebUI 地址同源。
func (c *client) origin() string {
	return c.base.Scheme + "://" + c.base.Host
}

// login 完成一次登录并留住 SID cookie。
func (c *client) login(ctx context.Context, cfg Config) error {
	form := url.Values{}
	form.Set("username", cfg.Username)
	form.Set("password", cfg.Password)

	status, body, header, err := c.do(ctx, http.MethodPost, "/api/v2/auth/login",
		strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}

	for _, cookieHeader := range header.Values("Set-Cookie") {
		header := http.Header{"Cookie": {cookieHeader}}
		if cookie, err := (&http.Request{Header: header}).Cookie("SID"); err == nil {
			c.sid = cookie.Value
		}
	}

	return parseLoginResponse(status, body)
}

// parseLoginResponse 双解析登录回执（规格 §5.4）：
//
//	4.6.5 → 200 纯文本 "Ok." / "Fails."，失败累计还可能 403 封 IP
//	5.x   → 200 JSON {"success":true} / {"success":false,"error":"…"}
//
// 判据先看状态码再看 body 形态：两种形态都要认，否则一次小版本升级就会把
// 「密码错了」显示成「协议不对」。
func parseLoginResponse(status int, body []byte) error {
	text := strings.TrimSpace(string(body))

	if status == http.StatusForbidden {
		return authError("auth/login", "qBittorrent 拒绝了登录（403）：密码错误次数过多被封 IP，或 WebUI 禁止了这台机器的访问", nil)
	}
	if text == "Ok." {
		return nil
	}

	var payload struct {
		Success *bool  `json:"success"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Success != nil {
		if *payload.Success {
			return nil
		}
		detail := "qBittorrent 拒绝了登录（JSON 回执）"
		if payload.Error != "" {
			detail += ": " + payload.Error
		}
		return authError("auth/login", detail, nil)
	}

	if text == "Fails." {
		return authError("auth/login", "qBittorrent 拒绝了登录：用户名或密码不对", nil)
	}
	if status < 200 || status >= 300 {
		return protocolError("auth/login", fmt.Sprintf("登录回执不是预期的形态（HTTP %d）: %s", status, truncate(text, 120)), nil)
	}
	return protocolError("auth/login", "登录回执既不是 Ok. 也不是 success JSON: "+truncate(text, 120), nil)
}

// version 读 qbt 版本（纯文本，4.x 与 5.x 一致）。
func (c *client) version(ctx context.Context) (string, error) {
	status, body, _, err := c.get(ctx, "/api/v2/app/version", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", c.statusError("app/version", status, body)
	}
	return strings.TrimSpace(string(body)), nil
}

// preferences 读偏好现值（只读 autorun 两项）。
func (c *client) preferences(ctx context.Context) (Preferences, error) {
	var prefs Preferences
	status, body, _, err := c.get(ctx, "/api/v2/app/preferences", nil)
	if err != nil {
		return Preferences{}, err
	}
	if status != http.StatusOK {
		return Preferences{}, c.statusError("app/preferences", status, body)
	}
	if err := json.Unmarshal(body, &prefs); err != nil {
		return Preferences{}, protocolError("app/preferences", "偏好回执不是 JSON: "+truncate(string(body), 120), err)
	}
	return prefs, nil
}

// setPreferences 只写传入的那几个键：qbt 的 setPreferences 是部分更新，
// 因此这就是「绝不碰 autorun_on_torrent_added_*」的实现保证。
func (c *client) setPreferences(ctx context.Context, values map[string]any) error {
	payload, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("序列化偏好: %w", err)
	}
	form := url.Values{"json": {string(payload)}}

	status, body, _, err := c.do(ctx, http.MethodPost, "/api/v2/app/setPreferences",
		strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return c.statusError("app/setPreferences", status, body)
	}
	return nil
}

// properties 查一个 infohash 在不在 qbt 里；第二个返回值是「在不在」。
func (c *client) properties(ctx context.Context, hash string) (Properties, bool, error) {
	status, body, _, err := c.get(ctx, "/api/v2/torrents/properties", url.Values{"hash": {hash}})
	if err != nil {
		return Properties{}, false, err
	}
	if status == http.StatusNotFound || strings.TrimSpace(string(body)) == "Not Found" {
		return Properties{}, false, nil
	}
	if status != http.StatusOK {
		return Properties{}, false, c.statusError("torrents/properties", status, body)
	}

	var props Properties
	if err := json.Unmarshal(body, &props); err != nil {
		return Properties{}, false, protocolError("torrents/properties", "properties 回执不是 JSON: "+truncate(string(body), 120), err)
	}
	return props, true, nil
}

// addRequest 是一次加种要带的东西。
type addRequest struct {
	// Magnet 与 Torrent 二选一：磁力走 urls=，私种走 multipart 的 torrents=。
	Magnet  string
	Torrent []byte
	// Filename 是私种在 multipart 里的文件名。
	Filename string
	SavePath string
	Category string
	// Tags 以逗号分隔，加种时一次带上（番号 + mdc:in-progress）。
	Tags string
}

// addTorrent 发一次 `torrents/add`（规格 §5.4）。
//
// 交付形态决定编码：磁力走 urlencoded 的 `urls=`，私有种子走 multipart 的 `torrents=`
// （part 的 Content-Type 是 application/x-bittorrent）。
// 每次显式 savepath + category + createCategories；**不设** autoTMM、不设 contentLayout：
// 那两项是用户自己的偏好，本项目不覆盖。
func (c *client) addTorrent(ctx context.Context, request addRequest) error {
	fields := url.Values{
		"savepath":         {request.SavePath},
		"category":         {request.Category},
		"createCategories": {"true"},
	}
	if request.Tags != "" {
		fields.Set("tags", request.Tags)
	}

	var (
		body        io.Reader
		contentType string
	)
	if len(request.Torrent) == 0 {
		fields.Set("urls", request.Magnet)
		body = strings.NewReader(fields.Encode())
		contentType = "application/x-www-form-urlencoded"
	} else {
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		for key, values := range fields {
			for _, value := range values {
				if err := writer.WriteField(key, value); err != nil {
					return fmt.Errorf("拼 multipart 字段 %s: %w", key, err)
				}
			}
		}
		part, err := createTorrentPart(writer, request.Filename)
		if err != nil {
			return err
		}
		if _, err := part.Write(request.Torrent); err != nil {
			return fmt.Errorf("写种子文件: %w", err)
		}
		if err := writer.Close(); err != nil {
			return fmt.Errorf("收尾 multipart: %w", err)
		}
		body = &buffer
		contentType = writer.FormDataContentType()
	}

	status, response, _, err := c.do(ctx, http.MethodPost, "/api/v2/torrents/add", body, contentType)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return c.statusError("torrents/add", status, response)
	}
	return parseAddResponse(response)
}

// createTorrentPart 手写种子文件的 part 头。
//
// multipart.Writer.CreateFormFile 只会给 application/octet-stream，规格 §5.4 要的是
// application/x-bittorrent；文件名里的引号一律去掉，免得拼坏 Content-Disposition。
func createTorrentPart(writer *multipart.Writer, filename string) (io.Writer, error) {
	safe := strings.NewReplacer(`"`, "", `\`, "").Replace(filename)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="torrents"; filename="`+safe+`"`)
	header.Set("Content-Type", "application/x-bittorrent")

	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, fmt.Errorf("拼 multipart 种子文件: %w", err)
	}
	return part, nil
}

// parseAddResponse 解析加种回执：4.6.5 是 "Ok."/"Fails."，5.x 可能回 JSON 或空体。
func parseAddResponse(body []byte) error {
	text := strings.TrimSpace(string(body))
	switch {
	case text == "" || text == "Ok.":
		return nil
	case text == "Fails.":
		return protocolError("torrents/add", "qBittorrent 拒绝了这次加种（回执 Fails.）", nil)
	}

	var payload struct {
		Success *bool  `json:"success"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Success != nil && !*payload.Success {
		detail := "qBittorrent 拒绝了这次加种"
		if payload.Error != "" {
			detail += ": " + payload.Error
		}
		return protocolError("torrents/add", detail, nil)
	}
	return nil
}

// createCategory 显式建分类（规格 §5.4 的 createCategories 显式）。
//
// 已存在时 qbt 回 409，那不是错误：幂等比「先查再建」少一次竞态。
func (c *client) createCategory(ctx context.Context, name string) error {
	form := url.Values{"category": {name}}
	status, body, _, err := c.do(ctx, http.MethodPost, "/api/v2/torrents/createCategory",
		strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	if status == http.StatusConflict {
		return nil
	}
	if status < 200 || status >= 300 {
		return c.statusError("torrents/createCategory", status, body)
	}
	return nil
}

// setCategory 给已存在的种子补分类（收编用；不动 savepath）。
func (c *client) setCategory(ctx context.Context, hash, category string) error {
	form := url.Values{"hashes": {hash}, "category": {category}}
	status, body, _, err := c.do(ctx, http.MethodPost, "/api/v2/torrents/setCategory",
		strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return c.statusError("torrents/setCategory", status, body)
	}
	return nil
}

// addTags 给种子补 tag（收编与 tag 换装用）。
func (c *client) addTags(ctx context.Context, hash string, tags []string) error {
	return c.changeTags(ctx, "torrents/addTags", hash, tags)
}

// removeTags 摘掉 tag（tag 换装：mdc:in-progress → mdc:finished）。
func (c *client) removeTags(ctx context.Context, hash string, tags []string) error {
	return c.changeTags(ctx, "torrents/removeTags", hash, tags)
}

// changeTags 是 addTags / removeTags 的共同形状：端点是唯一差别。
func (c *client) changeTags(ctx context.Context, endpoint, hash string, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	form := url.Values{"hashes": {hash}, "tags": {strings.Join(tags, ",")}}
	status, body, _, err := c.do(ctx, http.MethodPost, "/api/v2/"+endpoint,
		strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return c.statusError(endpoint, status, body)
	}
	return nil
}

// get 发一次读请求。
func (c *client) get(ctx context.Context, path string, query url.Values) (int, []byte, http.Header, error) {
	target := path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	return c.do(ctx, http.MethodGet, target, nil, "")
}

// do 是一次 qbt 调用的公共部分：URL、Referer、SID cookie、响应体读完。
//
// 它只负责「请求有没有发出去、回没回来」，非 2xx 的含义留给调用方
// （404 在 properties 里是「不在」，在别处是错）。
func (c *client) do(ctx context.Context, method, path string, body io.Reader, contentType string) (int, []byte, http.Header, error) {
	u := c.origin() + path
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return 0, nil, nil, protocolError(path, "构造请求失败: "+err.Error(), err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// qbt 的 CSRF 检查看 Referer 是否与 WebUI 同源；不带就 403。
	req.Header.Set("Referer", c.origin())
	if c.sid != "" {
		req.Header.Set("Cookie", "SID="+c.sid)
	}

	response, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, transportError(path, ctx, err)
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, nil, transportError(path, ctx, err)
	}
	return response.StatusCode, raw, response.Header, nil
}

// statusError 把非 2xx 归成 auth（401/403）或 protocol（其余）。
func (c *client) statusError(op string, status int, body []byte) error {
	detail := fmt.Sprintf("%s 回 HTTP %d: %s", op, status, truncate(strings.TrimSpace(string(body)), 160))
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return authError(op, detail+"（会话失效或被 WebUI 拒之门外，重新测试连接）", nil)
	}
	return protocolError(op, detail, nil)
}

// transportError 把连不上归成 timeout 或 network。
func transportError(op string, ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || isTimeout(err) {
		return &Error{Code: settings.CodeTimeout, Op: op, Detail: op + " 超时: " + err.Error(), Err: err}
	}
	return &Error{Code: settings.CodeNetwork, Op: op, Detail: "连不上 qBittorrent（" + op + "）: " + err.Error(), Err: err}
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
