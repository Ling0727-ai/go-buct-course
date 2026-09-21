package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Ling0727-ai/go-buct-course/exceptions"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// 本文件是认证流程的**离线**测试：用假传输层复刻统一身份认证服务端行为，
// 不发出任何真实网络请求。对应 Python 参考实现的 test/test_auth_flow.py。

const (
	fakeMEOLService = courseBase + "/meol/homepage/common/sso_login.jsp;jsessionid=ABC.TM1"
	gbkErrorPage    = `<HTML><HEAD><TITLE>错误！</TITLE></HEAD></HTML>`
)

// ---------------------------------------------------------------------------
// 假服务端
// ---------------------------------------------------------------------------

type recordedRequest struct {
	Method string
	URL    string
	Body   []byte
	Header http.Header
}

// fakeCAS 模拟 portal.buct.edu.cn + course.buct.edu.cn 的行为。
type fakeCAS struct {
	mu sync.Mutex

	loginBodies  []string // username-password/login 依次返回的响应体
	loginIndex   int
	loginCalls   int
	flowKeyCalls int
	casSession   bool   // 登录成功后置位：之后 /cas/login 才下发带 ticket 的回跳
	ticketMode   string // "" 正常 / "gbk" 错误页 / "bounce" 弹回门户
	publicKey    string
	cookieAttrs  string // COOKIE_INFO 的 Set-Cookie 附加属性
	requests     []recordedRequest
}

func newFakeCAS(loginBodies ...string) *fakeCAS {
	return &fakeCAS{
		loginBodies: loginBodies,
		publicKey:   pythonVectorPublicKey,
		cookieAttrs: "; Path=/",
	}
}

func (f *fakeCAS) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}
	f.requests = append(f.requests, recordedRequest{
		Method: req.Method,
		URL:    req.URL.String(),
		Body:   bodyBytes,
		Header: req.Header.Clone(),
	})

	host := req.URL.Host
	path := req.URL.Path

	switch {
	// 课程平台：带 ticket 的回跳
	case host == "course.buct.edu.cn" && req.URL.Query().Get("ticket") != "":
		if f.ticketMode == "gbk" {
			page, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(gbkErrorPage))
			if err != nil {
				return nil, err
			}
			return fakeResponse(req, 200, map[string]string{
				"Content-Type": "text/html; charset=gbk",
			}, string(page)), nil
		}
		return fakeResponse(req, 200, nil, "<html>ok</html>"), nil

	// 课程平台：MEOL SSO 入口，回 302 带上本次登录的 service
	case host == "course.buct.edu.cn" && path == meolSSOPath:
		return fakeResponse(req, http.StatusFound, map[string]string{
			"Location": portalBase + "/cas/login?service=" + quoteAll(fakeMEOLService),
		}, ""), nil

	// CAS 登录页：首次下发 COOKIE_INFO（flowKey），登录成功后下发 ticket 回跳
	case host == "portal.buct.edu.cn" && path == "/cas/login":
		if !f.casSession {
			f.flowKeyCalls++
			info := fmt.Sprintf(`{"code":600901,"data":{"flowKey":"flow.%064x"}}`, f.flowKeyCalls)
			return fakeResponse(req, 200, map[string]string{
				"Set-Cookie": "COOKIE_INFO=" + quoteAll(info) + f.cookieAttrs,
			}, info), nil
		}
		if f.ticketMode == "bounce" {
			return fakeResponse(req, http.StatusFound, map[string]string{
				"Location": portalBase + "/?timestamp=1&service=" + quoteAll(fakeMEOLService),
			}, ""), nil
		}
		return fakeResponse(req, http.StatusFound, map[string]string{
			"Location": fakeMEOLService + "?ticket=ST-1",
		}, ""), nil

	case host == "portal.buct.edu.cn" && path == "/cas/api/reset/rules":
		return fakeResponse(req, 200, nil, fmt.Sprintf(
			`{"code":666666,"data":{"encrypt":{"algorithm":"sm2","publicKey":%q}}}`, f.publicKey,
		)), nil

	case host == "portal.buct.edu.cn" && path == "/cas/info-query":
		return fakeResponse(req, 200, nil, `{"code":666666,"data":{}}`), nil

	case host == "portal.buct.edu.cn" && path == "/cas/username-password/login":
		f.loginCalls++
		index := f.loginIndex
		if index >= len(f.loginBodies) {
			index = len(f.loginBodies) - 1
		}
		f.loginIndex++
		body := f.loginBodies[index]
		if strings.Contains(body, fmt.Sprintf(`"code":%d`, successCode)) {
			f.casSession = true
		}
		return fakeResponse(req, 200, nil, body), nil

	case host == "portal.buct.edu.cn" && path == "/cas/logout":
		return fakeResponse(req, 200, nil, "<html>logout</html>"), nil

	case host == "portal.buct.edu.cn" && path == "/":
		return fakeResponse(req, 200, nil, "<html>portal</html>"), nil
	}

	return nil, fmt.Errorf("fakeCAS: 未预期的请求 %s %s", req.Method, req.URL)
}

func (f *fakeCAS) requestsTo(path string) []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedRequest
	for _, item := range f.requests {
		if parsed, err := url.Parse(item.URL); err == nil && parsed.Path == path {
			out = append(out, item)
		}
	}
	return out
}

func (f *fakeCAS) lastRequest() recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func (f *fakeCAS) allRequests() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

func (f *fakeCAS) setCASSession(value bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.casSession = value
}

func (f *fakeCAS) stats() (loginCalls, flowKeyCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loginCalls, f.flowKeyCalls
}

func fakeResponse(req *http.Request, status int, headers map[string]string, body string) *http.Response {
	header := make(http.Header, len(headers))
	for key, value := range headers {
		header.Set(key, value)
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// successLoginBody 构造登录成功响应；service 传入相对路径以复刻线上实测形态。
func successLoginBody(service string) string {
	raw, _ := json.Marshal(map[string]any{
		"code": successCode,
		"data": map[string]any{"service": service},
	})
	return string(raw)
}

func relativeTicketService() string {
	return "/cas/login?service=" + quoteAll(fakeMEOLService)
}

func newFakeAuth(cas *fakeCAS) *BUCTAuth {
	a := New()
	a.Client.Transport = cas
	return a
}

// ---------------------------------------------------------------------------
// 成功路径
// ---------------------------------------------------------------------------

func TestLoginSuccessFlow(t *testing.T) {
	cas := newFakeCAS(successLoginBody(relativeTicketService()))
	a := newFakeAuth(cas)

	// 口令故意用全角，验证「先全角转半角再 SM2 加密」
	if err := a.Login("2024030178", "ＰＡＳＳ１２３"); err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if !a.IsLoggedIn() {
		t.Error("登录后 IsLoggedIn 应为 true")
	}
	if a.flowKey != "flow.0000000000000000000000000000000000000000000000000000000000000001" {
		t.Errorf("flowKey = %q", a.flowKey)
	}
	if a.resolvedService != fakeMEOLService {
		t.Errorf("resolvedService = %q, 期望 %q", a.resolvedService, fakeMEOLService)
	}

	client, err := a.GetClient()
	if err != nil {
		t.Fatalf("GetClient 失败: %v", err)
	}
	if client != a.Client {
		t.Error("GetClient 应返回同一个 http.Client")
	}

	// 提交的登录请求
	loginRequests := cas.requestsTo("/cas/username-password/login")
	if len(loginRequests) != 1 {
		t.Fatalf("登录提交次数 = %d, 期望 1", len(loginRequests))
	}
	var submitted struct {
		Username string `json:"username"`
		Password string `json:"password"`
		FlowKey  string `json:"flowKey"`
	}
	if err := json.Unmarshal(loginRequests[0].Body, &submitted); err != nil {
		t.Fatalf("登录请求体不是合法 JSON: %v", err)
	}
	if submitted.Username != "2024030178" {
		t.Errorf("username = %q", submitted.Username)
	}
	if submitted.FlowKey != a.flowKey {
		t.Errorf("flowKey = %q, 期望 %q", submitted.FlowKey, a.flowKey)
	}

	plaintext, err := sm2Decrypt(submitted.Password, pythonVectorPrivateKey)
	if err != nil {
		t.Fatalf("口令密文无法用参考私钥解密: %v", err)
	}
	if string(plaintext) != "PASS123" {
		t.Errorf("口令 = %q, 期望 PASS123（全角转半角后再 SM2 加密）", plaintext)
	}

	// 相对路径 service 必须补全为门户地址
	casLoginRequests := cas.requestsTo("/cas/login")
	if len(casLoginRequests) != 2 {
		t.Fatalf("/cas/login 请求次数 = %d, 期望 2（取 flowKey + 跟随 ticket）", len(casLoginRequests))
	}
	if !strings.HasPrefix(casLoginRequests[1].URL, portalBase+"/cas/login?service=") {
		t.Errorf("ticket 回跳地址未补全: %s", casLoginRequests[1].URL)
	}

	// 最终必须落在课程平台
	if last := cas.lastRequest(); !strings.Contains(last.URL, "course.buct.edu.cn") {
		t.Errorf("最终请求未落在课程平台: %s", last.URL)
	}
}

// 与 Python 参考实现逐字符对齐的请求契约。
// service 的查询串编码来自 urllib.parse.urlencode({"service": ...})，
// Referer 来自 urllib.parse.quote(service, safe="")（空格 %20、';' -> %3B、'/' -> %2F）。
const (
	pythonEncodedService = "service=https%3A%2F%2Fcourse.buct.edu.cn%2Fmeol%2Fhomepage%2Fcommon%2Fsso_login.jsp%3Bjsessionid%3DABC.TM1"
	pythonReferer        = "https://portal.buct.edu.cn/?service=https%3A%2F%2Fcourse.buct.edu.cn%2Fmeol%2Fhomepage%2Fcommon%2Fsso_login.jsp%3Bjsessionid%3DABC.TM1"
)

// TestRequestContractMatchesPythonReference 锁定请求层面的编码与头部，防止移植漂移。
func TestRequestContractMatchesPythonReference(t *testing.T) {
	cas := newFakeCAS(successLoginBody(relativeTicketService()))
	a := newFakeAuth(cas)
	if err := a.Login("2024030178", "pw"); err != nil {
		t.Fatalf("登录失败: %v", err)
	}

	// 1) service 的查询串编码（取 flowKey 与 ticket 回跳两处）
	casLogin := cas.requestsTo("/cas/login")
	if len(casLogin) != 2 {
		t.Fatalf("/cas/login 请求次数 = %d, 期望 2", len(casLogin))
	}
	for index, request := range casLogin {
		parsed, err := url.Parse(request.URL)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.RawQuery != pythonEncodedService {
			t.Errorf("第 %d 次 /cas/login 查询串不一致\n got = %s\nwant = %s",
				index+1, parsed.RawQuery, pythonEncodedService)
		}
	}

	// 2) 所有请求都要带 Python _new_session 里设置的会话头
	sessionHeaders := map[string]string{
		"User-Agent":       defaultUserAgent,
		"Accept":           "application/json, text/javascript, */*; q=0.01",
		"Accept-Language":  "zh-CN,zh;q=0.9",
		"X-Requested-With": "XMLHttpRequest",
	}
	for _, request := range cas.allRequests() {
		for name, want := range sessionHeaders {
			if got := request.Header.Get(name); got != want {
				t.Errorf("%s %s 的 %s = %q, 期望 %q", request.Method, request.URL, name, got, want)
			}
		}
	}

	// 3) 登录提交的 Origin / Referer / Content-Type
	login := cas.requestsTo("/cas/username-password/login")
	if len(login) != 1 {
		t.Fatalf("登录提交次数 = %d, 期望 1", len(login))
	}
	if got := login[0].Header.Get("Origin"); got != portalBase {
		t.Errorf("Origin = %q, 期望 %q", got, portalBase)
	}
	if got := login[0].Header.Get("Referer"); got != pythonReferer {
		t.Errorf("Referer 与 Python 不一致\n got = %s\nwant = %s", got, pythonReferer)
	}
	if got := login[0].Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, 期望 application/json", got)
	}

	// 4) info-query 也要带 Origin
	for _, request := range cas.requestsTo("/cas/info-query") {
		if got := request.Header.Get("Origin"); got != portalBase {
			t.Errorf("info-query Origin = %q, 期望 %q", got, portalBase)
		}
	}
}

// TestCookieValueAcrossDomains COOKIE_INFO 可能以 host-only 或 Domain=.buct.edu.cn 下发，
// 两种形态都必须能读到（对应 Python 的 _cookie_value 按名字遍历所有 cookie）。
func TestCookieValueAcrossDomains(t *testing.T) {
	cases := []struct {
		name   string
		domain string // 空表示 host-only
		value  string
	}{
		{"host-only", "", "hostonly"},
		{"父域 Domain", ".buct.edu.cn", "parentdomain"},
	}
	for _, tc := range cases {
		a := New()
		requestURL, err := url.Parse(casBase + "/login")
		if err != nil {
			t.Fatal(err)
		}
		cookie := &http.Cookie{Name: "COOKIE_INFO", Value: tc.value, Path: "/"}
		if tc.domain != "" {
			cookie.Domain = tc.domain
		}
		a.Client.Jar.SetCookies(requestURL, []*http.Cookie{cookie})

		if got := a.cookieValue("COOKIE_INFO"); got != tc.value {
			t.Errorf("%s: cookieValue = %q, 期望 %q", tc.name, got, tc.value)
		}
	}

	a := New()
	if got := a.cookieValue("COOKIE_INFO"); got != "" {
		t.Errorf("无 cookie 时应返回空串, 实际 %q", got)
	}
}

// TestLoginWithParentDomainCookie 完整流程中 COOKIE_INFO 使用父域 Domain 也要能登录。
func TestLoginWithParentDomainCookie(t *testing.T) {
	cas := newFakeCAS(successLoginBody(relativeTicketService()))
	cas.cookieAttrs = "; Path=/; Domain=.buct.edu.cn"
	a := newFakeAuth(cas)

	if err := a.Login("2024030178", "pw"); err != nil {
		t.Fatalf("COOKIE_INFO 使用父域 Domain 时登录失败: %v", err)
	}
	if !a.IsLoggedIn() {
		t.Error("登录后 IsLoggedIn 应为 true")
	}
}

func TestLoginResetsSession(t *testing.T) {
	cas := newFakeCAS(successLoginBody(relativeTicketService()))
	a := newFakeAuth(cas)

	if err := a.Login("2024030178", "pw"); err != nil {
		t.Fatalf("第一次登录失败: %v", err)
	}
	cas.setCASSession(false) // 模拟服务端会话已过期
	if err := a.Login("2024030178", "pw"); err != nil {
		t.Fatalf("第二次登录失败: %v", err)
	}

	_, flowKeyCalls := cas.stats()
	if flowKeyCalls != 2 {
		t.Errorf("flowKey 获取次数 = %d, 期望 2（每次登录都要重新获取）", flowKeyCalls)
	}
}

// ---------------------------------------------------------------------------
// flowKey 一次性失效重试
// ---------------------------------------------------------------------------

func TestFlowKeyExpiredRetries(t *testing.T) {
	cas := newFakeCAS(
		`{"code":180076,"msg":"flowKey不能再次使用"}`,
		successLoginBody(relativeTicketService()),
	)
	a := newFakeAuth(cas)

	if err := a.Login("2024030178", "pw"); err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if !a.IsLoggedIn() {
		t.Error("重试成功后 IsLoggedIn 应为 true")
	}
	loginCalls, flowKeyCalls := cas.stats()
	if loginCalls != 2 {
		t.Errorf("登录提交次数 = %d, 期望 2", loginCalls)
	}
	if flowKeyCalls != 2 {
		t.Errorf("flowKey 获取次数 = %d, 期望 2", flowKeyCalls)
	}
}

func TestFlowKeyExpiredTwice(t *testing.T) {
	cas := newFakeCAS(
		`{"code":180046,"msg":"FlowKey没找到"}`,
		`{"code":180076,"msg":"flowKey不能再次使用"}`,
	)
	a := newFakeAuth(cas)

	err := a.Login("2024030178", "pw")
	if err == nil {
		t.Fatal("flowKey 连续失效时应报错")
	}
	if !errors.Is(err, exceptions.ErrLogin) {
		t.Errorf("错误应包装 ErrLogin, 实际 %v", err)
	}
	if !strings.Contains(err.Error(), "flowKey") {
		t.Errorf("错误信息应提到 flowKey, 实际 %v", err)
	}
	if a.IsLoggedIn() {
		t.Error("失败时不应标记为已登录")
	}
}

// ---------------------------------------------------------------------------
// 各类失败码
// ---------------------------------------------------------------------------

func TestWrongPassword(t *testing.T) {
	cas := newFakeCAS(`{"code":170002,"msg":"用户名或密码错误"}`)
	a := newFakeAuth(cas)

	err := a.Login("2024030178", "wrong")
	if err == nil {
		t.Fatal("口令错误时应报错")
	}
	if !strings.Contains(err.Error(), "用户名或密码错误") {
		t.Errorf("错误信息 = %v", err)
	}
	if !errors.Is(err, exceptions.ErrLogin) {
		t.Errorf("错误应包装 ErrLogin, 实际 %v", err)
	}
	loginCalls, flowKeyCalls := cas.stats()
	if loginCalls != 1 || flowKeyCalls != 1 {
		t.Errorf("口令错误不应重试: loginCalls=%d flowKeyCalls=%d", loginCalls, flowKeyCalls)
	}
}

func TestMFARequired(t *testing.T) {
	cas := newFakeCAS(`{"code":160001,"msg":"需要MFA验证"}`)
	a := newFakeAuth(cas)

	err := a.Login("2024030178", "pw")
	if err == nil || !strings.Contains(err.Error(), "MFA") {
		t.Errorf("应明确提示需要 MFA, 实际 %v", err)
	}
}

func TestAccountLocked(t *testing.T) {
	cas := newFakeCAS(`{"code":180028,"msg":"账号登录失败次数过多"}`)
	a := newFakeAuth(cas)

	err := a.Login("2024030178", "pw")
	if err == nil || !strings.Contains(err.Error(), "锁定") {
		t.Errorf("应明确提示账号锁定, 实际 %v", err)
	}
}

func TestUnknownCodeFallsBackToServerMessage(t *testing.T) {
	cas := newFakeCAS(`{"code":123456,"msg":"服务端自定义文案"}`)
	a := newFakeAuth(cas)

	err := a.Login("2024030178", "pw")
	if err == nil || !strings.Contains(err.Error(), "服务端自定义文案") {
		t.Errorf("未知返回码应回退到服务端 msg, 实际 %v", err)
	}
}

// ---------------------------------------------------------------------------
// 票据回跳校验
// ---------------------------------------------------------------------------

func TestTicketRejectedGBKErrorPage(t *testing.T) {
	cas := newFakeCAS(successLoginBody(relativeTicketService()))
	cas.ticketMode = "gbk"
	a := newFakeAuth(cas)

	err := a.Login("2024030178", "pw")
	if err == nil || !strings.Contains(err.Error(), "票据校验失败") {
		t.Errorf("GBK 错误页应判定票据被拒, 实际 %v", err)
	}
	if a.IsLoggedIn() {
		t.Error("票据被拒时不应标记为已登录")
	}
}

func TestTicketRejectedBouncedToPortal(t *testing.T) {
	cas := newFakeCAS(successLoginBody(relativeTicketService()))
	cas.ticketMode = "bounce"
	a := newFakeAuth(cas)

	err := a.Login("2024030178", "pw")
	if err == nil || !strings.Contains(err.Error(), "票据校验失败") {
		t.Errorf("弹回门户应判定票据被拒, 实际 %v", err)
	}
}

func TestMissingServiceRedirect(t *testing.T) {
	cas := newFakeCAS(`{"code":666666,"data":{}}`)
	a := newFakeAuth(cas)

	err := a.Login("2024030178", "pw")
	if err == nil || !strings.Contains(err.Error(), "service") {
		t.Errorf("成功码却缺少回跳地址时应报错, 实际 %v", err)
	}
	if a.IsLoggedIn() {
		t.Error("缺少回跳地址时不应标记为已登录")
	}
}

func TestExpectedServiceHost(t *testing.T) {
	cases := map[string]string{
		// 业务 host 必须从 service 参数推断
		portalBase + "/cas/login?service=" + quoteAll(fakeMEOLService): "course.buct.edu.cn",
		// 回跳地址本身就是业务地址
		"https://course.buct.edu.cn/meol/index.do?ticket=ST-1": "course.buct.edu.cn",
		// 无法解析
		"::::": "",
	}
	for input, want := range cases {
		if got := expectedServiceHost(input); got != want {
			t.Errorf("expectedServiceHost(%q) = %q, 期望 %q", input, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// 会话生命周期
// ---------------------------------------------------------------------------

func TestGetClientBeforeLogin(t *testing.T) {
	a := New()
	client, err := a.GetClient()
	if err == nil {
		t.Fatal("未登录时 GetClient 应报错")
	}
	if client != nil {
		t.Error("未登录时不应返回 http.Client")
	}
	if !errors.Is(err, exceptions.ErrLogin) {
		t.Errorf("错误应包装 ErrLogin, 实际 %v", err)
	}
	if a.IsLoggedIn() {
		t.Error("初始状态不应是已登录")
	}
}

func TestLogout(t *testing.T) {
	cas := newFakeCAS(successLoginBody(relativeTicketService()))
	a := newFakeAuth(cas)

	if err := a.Login("2024030178", "pw"); err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	a.Logout()

	if a.IsLoggedIn() {
		t.Error("登出后 IsLoggedIn 应为 false")
	}
	if a.flowKey != "" || a.resolvedService != "" {
		t.Errorf("登出后应清理 flowKey/resolvedService: %q %q", a.flowKey, a.resolvedService)
	}
	if _, err := a.GetClient(); err == nil {
		t.Error("登出后 GetClient 应报错")
	}
	if requests := cas.requestsTo("/cas/logout"); len(requests) != 1 {
		t.Errorf("应调用一次注销接口, 实际 %d 次", len(requests))
	}
}

// failingTransport 让所有请求在网络层失败。
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

func TestNetworkErrorWrapped(t *testing.T) {
	a := New()
	a.Client.Transport = failingTransport{}

	err := a.Login("2024030178", "pw")
	if err == nil {
		t.Fatal("网络故障时应报错")
	}
	if !errors.Is(err, exceptions.ErrNetwork) {
		t.Errorf("错误应包装 ErrNetwork, 实际 %v", err)
	}
}

// timeoutTransport 让所有请求以超时失败。
type timeoutTransport struct{}

func (timeoutTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, timeoutError{}
}

type timeoutError struct{}

func (timeoutError) Error() string { return "i/o timeout" }
func (timeoutError) Timeout() bool { return true }

func TestTimeoutErrorWrapped(t *testing.T) {
	a := New()
	a.Client.Transport = timeoutTransport{}

	err := a.Login("2024030178", "pw")
	if err == nil {
		t.Fatal("超时时应报错")
	}
	if !errors.Is(err, exceptions.ErrNetwork) {
		t.Errorf("错误应包装 ErrNetwork, 实际 %v", err)
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Errorf("超时应给出可读提示（对应 Python 的「登录请求超时」）, 实际 %v", err)
	}
}

func TestSetBaseURL(t *testing.T) {
	a := New()
	a.SetBaseURL("https://example.com/")

	if a.BaseURL != "https://example.com" {
		t.Errorf("BaseURL = %q", a.BaseURL)
	}
	if a.meolSSOEntry != "https://example.com"+meolSSOPath {
		t.Errorf("meolSSOEntry = %q", a.meolSSOEntry)
	}
}

func TestSetServiceSkipsSSOEntry(t *testing.T) {
	cas := newFakeCAS(successLoginBody(relativeTicketService()))
	a := newFakeAuth(cas)
	a.SetService(fakeMEOLService)

	if err := a.Login("2024030178", "pw"); err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	// 指定 service 后不应再访问 MEOL SSO 入口
	if requests := cas.requestsTo(meolSSOPath); len(requests) != 0 {
		t.Errorf("指定 service 后不应访问 SSO 入口, 实际 %d 次", len(requests))
	}
}

// ---------------------------------------------------------------------------
// 全角转半角 / 公开接口
// ---------------------------------------------------------------------------

func TestToHalfWidth(t *testing.T) {
	// 向量与站点 toHalfWidth 逐例对照（portal.buct.edu.cn 页面实跑采集）。
	// 注意 ｟(U+FF5F) / ｠(U+FF60) 落在站点正则 [\uff01-\uff5e] 之外，必须保持不变。
	cases := map[string]string{
		"ＡＢＣ１２３":      "ABC123",
		"ａｂｃ":         "abc",
		"ｐ＠ｓｓｗｏｒｄ！":   "p@ssword!",
		"全角　空格":       "全角 空格",
		"ASCII123":    "ASCII123",
		"～！＠＃＄％":      "~!@#$%",
		"｟｠｛｝":        "｟｠{}",
		"　":           " ",
		"混合ＡＢc１２３！@#": "混合ABc123!@#",
		"ＮｏＦｕｌｌＷｉｄｔｈ": "NoFullWidth",
	}
	for input, want := range cases {
		if got := toHalfWidth(input); got != want {
			t.Errorf("toHalfWidth(%q) = %q, 期望 %q", input, got, want)
		}
	}
}

// TestPublicAPISurface 锁死对外函数名与签名：调用方依赖这些名字，不得改名。
func TestPublicAPISurface(t *testing.T) {
	expected := map[string]string{
		"Login":      "func(*auth.BUCTAuth, string, string) error",
		"GetClient":  "func(*auth.BUCTAuth) (*http.Client, error)",
		"IsLoggedIn": "func(*auth.BUCTAuth) bool",
		"Logout":     "func(*auth.BUCTAuth)",
		"SetBaseURL": "func(*auth.BUCTAuth, string)",
	}
	authType := reflect.TypeOf(New())
	for name, signature := range expected {
		method, ok := authType.MethodByName(name)
		if !ok {
			t.Errorf("公开方法 %s 丢失（不得改名）", name)
			continue
		}
		if got := method.Type.String(); got != signature {
			t.Errorf("%s 签名 = %s, 期望 %s", name, got, signature)
		}
	}

	if got := reflect.TypeOf(New).String(); got != "func() *auth.BUCTAuth" {
		t.Errorf("New 签名 = %s, 期望 func() *auth.BUCTAuth", got)
	}
}
