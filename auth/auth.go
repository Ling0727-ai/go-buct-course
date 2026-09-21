// Package auth 提供北化课程平台的认证能力。
//
// 登录流程（2026-09 起，统一身份认证）
// -----------------------------------
// 课程平台 MEOL 已关闭旧的直连登录接口（/meol/loginCheck.do 现返回 404），
// 改为统一身份认证。现在必须走 portal.buct.edu.cn 的 CAS：
//
//  1. GET  course.buct.edu.cn/meol/homepage/common/sso_login.jsp
//     拿到本次登录的 service（形如
//     https://course.buct.edu.cn/meol/homepage/common/sso_login.jsp;jsessionid=<MEOL会话>）
//  2. GET  portal.buct.edu.cn/cas/login?service=<service>
//     服务端下发 COOKIE_INFO cookie，其中 data.flowKey 就是登录要用的 flowKey
//  3. GET  portal.buct.edu.cn/cas/api/reset/rules
//     拿到 data.encrypt.publicKey（algorithm = sm2，65 字节未压缩点）
//  4. 用 SM2 加密口令（见 sm2_crypto.go），口令先做全角转半角
//  5. POST portal.buct.edu.cn/cas/username-password/login
//     body: {"username": ..., "password": <sm2 base64>, "flowKey": ...}
//  6. 成功后 data.service 是带 ticket 的回跳地址，跟随它即可建立 MEOL 会话
//
// 要点：
//
//   - flowKey 由服务端下发、且**一次性**（重复使用报 180076），因此每次登录都要重新获取。
//   - password 是 SM2 密文：base64(C1.x||C1.y||C3||C2)，C1 为 64 字节不带 04 前缀。
//   - 该实现**不需要**执行站点 JS，纯 Go 算法复现。
//
// 对外接口与旧版保持一致：New / Login / GetClient / IsLoggedIn / Logout / SetBaseURL。
// 旧的直连 MEOL 实现（/meol/loginCheck.do）已彻底移除——该接口现已返回 404。
package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/Ling0727-ai/go-buct-course/exceptions"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

// 统一身份认证相关的固定地址与参数
const (
	portalBase = "https://portal.buct.edu.cn"
	casBase    = portalBase + "/cas"
	courseBase = "https://course.buct.edu.cn"

	// meolSSOPath 课程平台 MEOL 的 SSO 入口路径，用于换取本次登录的 service
	meolSSOPath = "/meol/homepage/common/sso_login.jsp"

	// DefaultPortalService 不指定 service 时，直接登录门户（i北化）所用的服务地址
	DefaultPortalService = "http://i.buct.edu.cn"

	// defaultTimeout 单次请求超时（对应 Python 的 timeout=15）
	defaultTimeout = 15 * time.Second

	// successCode 统一身份认证登录成功返回码
	successCode = 666666

	// maxProbeBytes 判定「票据被拒绝」时读取响应体的最大字节数
	maxProbeBytes = 8192

	// maxReadBytes 单个响应体最多读取的字节数（避免异常页面撑爆内存）
	maxReadBytes = 1 << 20

	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"
)

// loginErrorMessages 统一身份认证返回码 -> 可读信息
var loginErrorMessages = map[int]string{
	160001: "该账号需要 MFA 二次验证，本库暂不支持，请先在浏览器完成一次登录",
	160002: "该账号需要图形验证码，本库暂不支持，请稍后再试或先在浏览器完成一次登录",
	160066: "该账号需要选择代理用户，本库暂不支持",
	160067: "该账号需要添加常用设备，请先在浏览器确认一次",
	170002: "用户名或密码错误",
	170004: "登录信息错误",
	170006: "认证未通过",
	180003: "用户名为空",
	180004: "密码为空",
	180006: "密码需要修改",
	180014: "请求格式错误（json 格式错误）",
	180026: "没找到 UID",
	180028: "账号登录失败次数过多，已临时锁定，请等待 30 分钟",
	180029: "错误次数太多，账号已被锁死",
	180046: "flowKey 无效（FlowKey 没找到）",
	180076: "flowKey 已被使用过，不能重复使用",
	180083: "无系统权限或账号已停用",
	600904: "TGT 创建失败",
	999999: "操作失败",
}

// flowKeyExpiredCodes flowKey 失效时值得换一个重试
var flowKeyExpiredCodes = map[int]bool{180046: true, 180076: true}

// BUCTAuth 北化课程平台认证结构体
type BUCTAuth struct {
	// Client 已认证的 HTTP 客户端（内部 CookieJar 保存会话）
	Client *http.Client
	// BaseURL 课程平台基础地址
	BaseURL string

	isLoggedIn      bool
	timeout         time.Duration
	service         string
	flowKey         string
	publicKey       string
	resolvedService string
	meolSSOEntry    string
	userAgent       string
}

// New 创建一个新的认证实例
func New() *BUCTAuth {
	a := &BUCTAuth{
		BaseURL:   courseBase,
		timeout:   defaultTimeout,
		userAgent: defaultUserAgent,
	}
	a.meolSSOEntry = a.BaseURL + meolSSOPath
	a.Client = a.newClient()
	return a
}

// newClient 创建带全新 CookieJar 的 HTTP 客户端（登录/登出都会重建，避免旧 cookie 干扰）。
func (a *BUCTAuth) newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:     jar,
		Timeout: a.timeout,
	}
}

// SetService 指定 CAS 的 service 参数。
//
// 默认空值表示走课程平台 MEOL 的 SSO 入口（也就是本库真正需要的登录态）；
// 传 DefaultPortalService 或任意 URL 可直接登录该服务。
func (a *BUCTAuth) SetService(service string) {
	a.service = service
}

// SetTimeout 设置单次请求超时（默认 15 秒）。
func (a *BUCTAuth) SetTimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	a.timeout = timeout
	if a.Client != nil {
		a.Client.Timeout = timeout
	}
}

// newRequest 构造带统一请求头的请求（对应 Python 的 _new_session 里设置的会话头）。
func (a *BUCTAuth) newRequest(method, rawURL string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", a.userAgent)
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	return req, nil
}

// do 发送请求，并把传输层错误统一包装为 ErrNetwork（对应 Python 的 RequestException 分支）。
func (a *BUCTAuth) do(req *http.Request) (*http.Response, error) {
	resp, err := a.Client.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, fmt.Errorf("%w: 登录请求超时", exceptions.ErrNetwork)
		}
		return nil, fmt.Errorf("%w: %v", exceptions.ErrNetwork, err)
	}
	return resp, nil
}

// doWithoutRedirect 发送请求但**不跟随重定向**，用于读取 302 的 Location。
// 浅拷贝的客户端与 a.Client 共享同一个 CookieJar。
func (a *BUCTAuth) doWithoutRedirect(req *http.Request) (*http.Response, error) {
	client := *a.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, fmt.Errorf("%w: 登录请求超时", exceptions.ErrNetwork)
		}
		return nil, fmt.Errorf("%w: %v", exceptions.ErrNetwork, err)
	}
	return resp, nil
}

// drainBody 读完并关闭响应体，保证连接可以复用。
func drainBody(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxReadBytes))
	_ = resp.Body.Close()
}

// cookieValue 按名字读取 cookie。
//
// 对应 Python 的 _cookie_value：同名多域时 cookies.get 会抛
// CookieConflictError，因此这里在若干候选域上逐个查找而不是直接用单域取值。
func (a *BUCTAuth) cookieValue(name string) string {
	if a.Client == nil || a.Client.Jar == nil {
		return ""
	}
	for _, raw := range []string{casBase + "/login", portalBase, a.BaseURL} {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		for _, cookie := range a.Client.Jar.Cookies(u) {
			if cookie.Name == name {
				return cookie.Value
			}
		}
	}
	return ""
}

// toHalfWidth 全角转半角，对齐前端 toHalfWidth（fullToHalf = true）。
//
// 注意 ｟(U+FF5F) / ｠(U+FF60) 落在站点正则 [\uff01-\uff5e] 之外，必须保持不变。
func toHalfWidth(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, ch := range text {
		switch {
		case ch >= 0xFF01 && ch <= 0xFF5E:
			builder.WriteRune(ch - 0xFEE0)
		case ch == 0x3000:
			builder.WriteRune(' ')
		default:
			builder.WriteRune(ch)
		}
	}
	return builder.String()
}

// quoteAll 等价于 Python 的 urllib.parse.quote(s, safe="")（空格编码为 %20 而非 +）。
func quoteAll(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// resolveService 确定本次登录的 service 参数。
func (a *BUCTAuth) resolveService() (string, error) {
	if a.service != "" {
		return a.service, nil
	}

	req, err := a.newRequest(http.MethodGet, a.meolSSOEntry, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", exceptions.ErrLogin, err)
	}
	resp, err := a.doWithoutRedirect(req)
	if err != nil {
		return "", err
	}
	defer drainBody(resp)

	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf(
			"%w: 无法从课程平台 SSO 入口获取 service（HTTP %d）",
			exceptions.ErrLogin, resp.StatusCode,
		)
	}

	// Location 可能是相对路径，url.Parse 依然能取到 query 里的 service
	parsed, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("%w: SSO 入口返回的 Location 非法: %v", exceptions.ErrLogin, err)
	}
	service := parsed.Query().Get("service")
	if service == "" {
		return "", fmt.Errorf("%w: SSO 入口未返回 service 参数", exceptions.ErrLogin)
	}
	return service, nil
}

// fetchFlowKey 从 CAS 下发的 COOKIE_INFO 中取出一次性 flowKey。
func (a *BUCTAuth) fetchFlowKey(service string) (string, error) {
	target := fmt.Sprintf("%s/login?service=%s", casBase, url.QueryEscape(service))
	req, err := a.newRequest(http.MethodGet, target, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", exceptions.ErrLogin, err)
	}
	resp, err := a.doWithoutRedirect(req)
	if err != nil {
		return "", err
	}
	defer drainBody(resp)

	raw := a.cookieValue("COOKIE_INFO")
	if raw == "" {
		return "", fmt.Errorf("%w: 统一身份认证未下发 COOKIE_INFO，无法获取 flowKey", exceptions.ErrLogin)
	}

	// 站点用 JS 的 encodeURIComponent 编码，对应 Python 的 urllib.parse.unquote：
	// 只还原 %XX，不把 '+' 当成空格，因此用 PathUnescape 而不是 QueryUnescape。
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", fmt.Errorf("%w: 解析 COOKIE_INFO 失败: %v", exceptions.ErrLogin, err)
	}

	var info struct {
		Data struct {
			FlowKey string `json:"flowKey"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(decoded), &info); err != nil {
		return "", fmt.Errorf("%w: 解析 COOKIE_INFO 失败: %v", exceptions.ErrLogin, err)
	}
	if info.Data.FlowKey == "" {
		return "", fmt.Errorf("%w: 解析 COOKIE_INFO 失败: 缺少 flowKey", exceptions.ErrLogin)
	}
	return info.Data.FlowKey, nil
}

// fetchPublicKey 获取 SM2 加密公钥（base64）。
func (a *BUCTAuth) fetchPublicKey() (string, error) {
	req, err := a.newRequest(http.MethodGet, casBase+"/api/reset/rules", nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", exceptions.ErrLogin, err)
	}
	resp, err := a.do(req)
	if err != nil {
		return "", err
	}
	defer drainBody(resp)

	var payload struct {
		Data struct {
			Encrypt struct {
				PublicKey string `json:"publicKey"`
			} `json:"encrypt"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReadBytes)).Decode(&payload); err != nil {
		return "", fmt.Errorf("%w: 获取 SM2 公钥失败: %v", exceptions.ErrLogin, err)
	}
	if payload.Data.Encrypt.PublicKey == "" {
		return "", fmt.Errorf("%w: 获取 SM2 公钥失败: 响应缺少 publicKey", exceptions.ErrLogin)
	}
	a.publicKey = payload.Data.Encrypt.PublicKey
	return a.publicKey, nil
}

// queryLoginInfo 登录前查询 MFA / 验证码要求（失败不影响主流程）。
func (a *BUCTAuth) queryLoginInfo(flowKey string) {
	body, err := json.Marshal(map[string]string{"username": "", "flowKey": flowKey})
	if err != nil {
		return
	}
	req, err := a.newRequest(http.MethodPost, casBase+"/info-query", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", portalBase)

	resp, err := a.do(req)
	if err != nil {
		return
	}
	drainBody(resp)
}

// encryptPassword SM2 加密口令，返回 base64 密文。
func (a *BUCTAuth) encryptPassword(password, publicKey string) (string, error) {
	normalized := toHalfWidth(password)
	cipher, err := sm2Encrypt([]byte(normalized), publicKey)
	if err != nil {
		return "", fmt.Errorf("%w: 加密口令失败: %v", exceptions.ErrLogin, err)
	}
	return cipher, nil
}

// loginPayload 统一身份认证登录接口的响应。
type loginPayload struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// loginErrorMessage 把返回码翻译成可读信息。
func (p *loginPayload) loginErrorMessage() string {
	if message, ok := loginErrorMessages[p.Code]; ok && message != "" {
		return message
	}
	if p.Msg != "" {
		return p.Msg
	}
	return fmt.Sprintf("登录失败（code=%d）", p.Code)
}

// redirectService 取出成功后用于回跳业务系统的 service 地址。
func (p *loginPayload) redirectService() (string, bool) {
	if len(p.Data) == 0 {
		return "", false
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(p.Data, &data); err != nil {
		return "", false
	}
	raw, ok := data["service"]
	if !ok {
		return "", false
	}

	var service string
	if err := json.Unmarshal(raw, &service); err == nil {
		if service == "" {
			return "", false
		}
		return service, true
	}

	// 对象形态：按 Python 的 json.dumps 处理
	var obj any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", false
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

// submitLogin 提交用户名口令登录，返回服务端 JSON。
func (a *BUCTAuth) submitLogin(username, cipher, flowKey string) (*loginPayload, error) {
	body, err := json.Marshal(map[string]string{
		"username": username,
		"password": cipher,
		"flowKey":  flowKey,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", exceptions.ErrLogin, err)
	}

	req, err := a.newRequest(http.MethodPost, casBase+"/username-password/login", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", exceptions.ErrLogin, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", portalBase)
	req.Header.Set("Referer", fmt.Sprintf("%s/?service=%s", portalBase, quoteAll(a.resolvedService)))

	resp, err := a.do(req)
	if err != nil {
		return nil, err
	}
	defer drainBody(resp)

	payload := &loginPayload{}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReadBytes)).Decode(payload); err != nil {
		return nil, fmt.Errorf(
			"%w: 登录响应不是合法 JSON（HTTP %d）", exceptions.ErrLogin, resp.StatusCode,
		)
	}
	return payload, nil
}

// expectedServiceHost 从 CAS 回跳地址推断最终应落地的业务系统 host。
//
// CAS 的回跳地址形如 /cas/login?service=<业务URL>——真正的目标 host 藏在
// service 参数里；若回跳地址本身就是业务地址，则用它自己的 host。
func expectedServiceHost(redirectURL string) string {
	parsed, err := url.Parse(redirectURL)
	if err != nil {
		return ""
	}
	inner := parsed.Query().Get("service")
	if inner != "" {
		if innerURL, err := url.Parse(inner); err == nil && innerURL.Host != "" {
			return innerURL.Host
		}
	}
	return parsed.Host
}

// ticketRejected 判断回跳后票据是否未被接受。
//
// 实测三种形态（均线上采集）：
//
//   - 无 ticket：跟随重定向后停在**门户**登录页
//     https://portal.buct.edu.cn?timestamp=...&service=...
//     ——最终 host 不等于业务系统 host；
//   - ticket 无效：200 + GBK 错误页，<TITLE>错误！</TITLE>；
//   - 正常：最终落在课程平台 course.buct.edu.cn/meol/...。
//
// 注意两个坑：不能只匹配 /cas/login（无 ticket 时最终 URL 不含该路径），
// 也不能拿「回跳地址自身」的 host 去比（成功回跳的第一跳就在门户域上，
// 业务 host 必须从 service 参数里取）。
func ticketRejected(resp *http.Response, redirectURL string, head []byte) bool {
	expectedHost := expectedServiceHost(redirectURL)
	finalURL := ""
	finalHost := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
		finalHost = resp.Request.URL.Host
	}
	if expectedHost != "" && finalHost != "" && finalHost != expectedHost {
		return true
	}
	if strings.Contains(finalURL, "/cas/login") {
		return true
	}

	decoders := []encoding.Encoding{simplifiedchinese.GBK, unicode.UTF8}
	for _, decoder := range decoders {
		text, err := decoder.NewDecoder().Bytes(head)
		if err != nil {
			continue
		}
		if strings.Contains(string(text), "错误！") || strings.Contains(string(text), "错误!") {
			return true
		}
	}
	return false
}

// followServiceTicket 跟随 CAS 回跳地址，建立业务系统（MEOL）会话。
//
// 成功码却没有回跳地址、或票据被课程平台拒绝时返回 ErrLogin。
func (a *BUCTAuth) followServiceTicket(payload *loginPayload) error {
	redirect, ok := payload.redirectService()
	if !ok {
		return fmt.Errorf(
			"%w: 统一身份认证返回成功，但响应中没有可回跳的 service 地址", exceptions.ErrLogin,
		)
	}

	// 实测成功时 data.service 是**相对路径**（如 "/cas/login?service=..."）：
	// 浏览器按当前页自动解析，Go 不会，必须显式补全。
	base, err := url.Parse(portalBase)
	if err != nil {
		return fmt.Errorf("%w: %v", exceptions.ErrLogin, err)
	}
	reference, err := url.Parse(redirect)
	if err != nil {
		return fmt.Errorf("%w: 回跳地址非法: %v", exceptions.ErrLogin, err)
	}
	target := base.ResolveReference(reference).String()

	req, err := a.newRequest(http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", exceptions.ErrLogin, err)
	}
	resp, err := a.do(req)
	if err != nil {
		return err
	}
	defer drainBody(resp)

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxReadBytes))
	if len(body) > maxProbeBytes {
		body = body[:maxProbeBytes]
	}
	if ticketRejected(resp, target, body) {
		return fmt.Errorf(
			"%w: 统一身份认证已通过，但课程平台票据校验失败（ticket 无效或 MEOL 会话不匹配）",
			exceptions.ErrLogin,
		)
	}
	return nil
}

// Login 登录到北化课程平台（统一身份认证）。
//
// 对应 Python 的 login(username, password)：返回 nil 表示登录成功。
func (a *BUCTAuth) Login(username, password string) error {
	// 1. 重置 Session 与登录态
	a.Client.Jar, _ = cookiejar.New(nil)
	a.isLoggedIn = false
	a.flowKey = ""
	a.resolvedService = ""

	// 2. 确定 service
	service, err := a.resolveService()
	if err != nil {
		return err
	}
	a.resolvedService = service

	// 3. flowKey 一次性；若中途失效则整体重取一次
	for attempt := 0; attempt < 2; attempt++ {
		flowKey, err := a.fetchFlowKey(service)
		if err != nil {
			return err
		}
		a.flowKey = flowKey

		publicKey, err := a.fetchPublicKey()
		if err != nil {
			return err
		}
		a.queryLoginInfo(flowKey)

		cipher, err := a.encryptPassword(password, publicKey)
		if err != nil {
			return err
		}

		payload, err := a.submitLogin(username, cipher, flowKey)
		if err != nil {
			return err
		}

		if payload.Code == successCode {
			if err := a.followServiceTicket(payload); err != nil {
				return err
			}
			a.isLoggedIn = true
			return nil
		}

		if flowKeyExpiredCodes[payload.Code] && attempt == 0 {
			continue
		}
		return fmt.Errorf("%w: %s", exceptions.ErrLogin, payload.loginErrorMessage())
	}

	return fmt.Errorf("%w: 登录失败：flowKey 多次失效", exceptions.ErrLogin)
}

// GetClient 获取已认证的 http.Client (对应 Python: get_session)
func (a *BUCTAuth) GetClient() (*http.Client, error) {
	if !a.isLoggedIn {
		return nil, fmt.Errorf("client not authenticated: %w", exceptions.ErrLogin)
	}
	return a.Client, nil
}

// IsLoggedIn 检查是否已登录
func (a *BUCTAuth) IsLoggedIn() bool {
	return a.isLoggedIn
}

// Logout 注销登录
func (a *BUCTAuth) Logout() {
	// 尝试调用服务器注销接口，忽略错误
	if req, err := a.newRequest(http.MethodGet, casBase+"/logout", nil); err == nil {
		if resp, err := a.do(req); err == nil {
			drainBody(resp)
		}
	}

	// 无论如何，清空本地 Session（对应 Python 的 session.close() + 新建 session）
	a.Client.CloseIdleConnections()
	a.Client.Jar, _ = cookiejar.New(nil)
	a.isLoggedIn = false
	a.flowKey = ""
	a.resolvedService = ""
}

// SetBaseURL 设置基础URL
func (a *BUCTAuth) SetBaseURL(urlStr string) {
	a.BaseURL = strings.TrimRight(urlStr, "/")
	a.meolSSOEntry = a.BaseURL + meolSSOPath
}
