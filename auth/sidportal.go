package auth

import (
	"bytes"
	"crypto/aes"
	"crypto/md5" // The site's CSRF interceptor specifies MD5, not a password hash.
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Ling0727-ai/go-buct-course/exceptions"
	"github.com/PuerkitoBio/goquery"
)

const sidPortalBase = "https://sidportal.buct.edu.cn"

var errSIDRedirect = errors.New("authentication redirected to sidportal")

// encryptSIDValue matches the published aesEncrypt: Base64 key, UTF-8 text,
// AES-ECB, PKCS#7 padding, Base64 ciphertext. Do not normalize the password.
func encryptSIDValue(keyBase64, text string) (string, error) {
	key, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return "", fmt.Errorf("invalid Base64 AES key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("invalid AES key length")
	}
	data := []byte(text)
	padding := aes.BlockSize - len(data)%aes.BlockSize
	data = append(data, bytes.Repeat([]byte{byte(padding)}, padding)...)
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Encrypt(data[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

type sidPage struct {
	execution  string
	key        string
	vendor     string
	risk       string
	rule       string
	second     string
	errorCode  string
	captchaURL string
}

func parseSIDPage(body []byte) (sidPage, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return sidPage{}, fmt.Errorf("%w: 无法解析统一认证页面", exceptions.ErrLogin)
	}
	text := func(id string) string { return strings.TrimSpace(doc.Find("#" + id).First().Text()) }
	code := text("login-error-msg")
	if code == "" {
		code = text("login-error-code")
	}
	return sidPage{execution: text("login-page-flowkey"), key: text("login-croypto"), vendor: text("recaptchaVendor"), risk: text("riskSystemSwitch"), rule: text("login-rule-type"), second: text("sso-second"), errorCode: code, captchaURL: text("captcha-url")}, nil
}

func sidLoginError(code string) error {
	message := map[string]string{
		"1030027": "用户名或密码错误", "1030031": "用户名或密码错误",
		"1030028": "账号已锁定，请在浏览器处理或等待解锁",
		"1410040": "当前账号不可用", "1410041": "当前账号不可用",
		"1320007": "需要图形验证码，请在浏览器完成验证",
		"1030007": "验证码不正确，请在浏览器重新验证",
		"1330001": "需要风险验证，请在浏览器完成验证",
		"1030078": "需要选择账号，请在浏览器完成登录",
		"1030099": "需要选择身份，请在浏览器完成登录",
	}[code]
	if message == "" {
		message = "统一身份认证未完成，请在浏览器检查账号、验证码或二次验证"
	}
	// Do not echo arbitrary response content, which may include session secrets.
	return fmt.Errorf("%w: %s", exceptions.ErrLogin, message)
}

// SetLoginURL explicitly selects an authentication entry (normally unnecessary).
// An empty value restores automatic discovery through the MEOL SSO redirect.
// SetService without an override uses the new sidportal entry.
func (a *BUCTAuth) SetLoginURL(rawURL string) { a.loginURLOverride = rawURL }

func isSIDLoginURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && u.User == nil && u.Scheme == "https" && u.Host == "sidportal.buct.edu.cn" && u.Path == "/login"
}

// sidClient follows only authentication/business redirects. Never forward a
// credential-bearing POST with HTTP 307/308, even to the same host.
func (a *BUCTAuth) sidClient(service *url.URL) *http.Client {
	client := *a.Client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many authentication redirects")
		}
		if req.Method != http.MethodGet {
			return fmt.Errorf("refusing credential replay on redirect")
		}
		u := req.URL
		if u.User != nil || u.Scheme != "https" || (u.Host != "sidportal.buct.edu.cn" && u.Host != "portal.buct.edu.cn" && u.Host != service.Host) {
			return fmt.Errorf("unexpected authentication redirect")
		}
		return nil
	}
	return &client
}

// checkSIDCaptcha follows the frontend's getCount(username) preflight. Refuse
// required challenges; do not manufacture provider errors or CAPTCHA tokens.
func (a *BUCTAuth) checkSIDCaptcha(username, referer string) error {
	req, err := a.newRequest(http.MethodGet, sidPortalBase+"/api/protected/user/findCaptchaCount/"+url.PathEscape(username), nil)
	if err != nil {
		return fmt.Errorf("%w: 无法构造验证码检查请求", exceptions.ErrLogin)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return fmt.Errorf("%w: 无法生成 CSRF 参数", exceptions.ErrLogin)
	}
	key := hex.EncodeToString(random)
	encoded := base64.StdEncoding.EncodeToString([]byte(key))
	value := encoded[:len(encoded)/2] + encoded + encoded[len(encoded)/2:]
	digest := md5.Sum([]byte(value))
	req.Header.Set("Csrf-Key", key)
	req.Header.Set("Csrf-Value", hex.EncodeToString(digest[:]))
	req.Header.Set("Referer", referer)
	// This JSON endpoint must not redirect to a different host.
	resp, err := a.doWithoutRedirect(req)
	if err != nil {
		return err
	}
	defer drainBody(resp)
	var payload struct {
		Code int `json:"code"`
		Data *struct {
			CaptchaInvisible *bool `json:"captchaInvisible"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: 无法检查验证码要求（HTTP %d）", exceptions.ErrLogin, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReadBytes)).Decode(&payload); err != nil || payload.Code != 200 || payload.Data == nil || payload.Data.CaptchaInvisible == nil {
		return fmt.Errorf("%w: 无法确认验证码要求，请在浏览器检查登录页", exceptions.ErrLogin)
	}
	if *payload.Data.CaptchaInvisible {
		return sidLoginError("1320007")
	}
	return nil
}

func (a *BUCTAuth) loginSID(username, password, entry string) error {
	if !isSIDLoginURL(entry) {
		return fmt.Errorf("%w: 新版认证入口不受信任", exceptions.ErrLogin)
	}
	service, err := url.Parse(a.resolvedService)
	if err != nil || service.Host == "" || service.User != nil || service.Scheme != "https" {
		return fmt.Errorf("%w: 新版认证要求有效的 HTTPS service", exceptions.ErrLogin)
	}
	client := a.sidClient(service)
	req, err := a.newRequest(http.MethodGet, entry, nil)
	if err != nil {
		return fmt.Errorf("%w: 无法构造认证请求", exceptions.ErrLogin)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Del("X-Requested-With")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: 无法获取新版认证页面", exceptions.ErrNetwork)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxReadBytes+1))
	drainBody(resp)
	if readErr != nil || len(body) > maxReadBytes {
		return fmt.Errorf("%w: 无法完整读取认证页面", exceptions.ErrLogin)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: 认证页面 HTTP %d", exceptions.ErrLogin, resp.StatusCode)
	}
	if resp.Request == nil || resp.Request.URL == nil || !isSIDLoginURL(resp.Request.URL.String()) {
		return fmt.Errorf("%w: 认证入口发生非预期跳转", exceptions.ErrLogin)
	}
	page, err := parseSIDPage(body)
	if err != nil {
		return err
	}
	if page.errorCode != "" {
		return sidLoginError(page.errorCode)
	}
	if page.execution == "" || page.key == "" {
		return fmt.Errorf("%w: 新版认证页面缺少 execution 或 croypto", exceptions.ErrLogin)
	}
	if (page.vendor != "" && page.vendor != "system") || (page.risk != "" && page.risk != "default") || (page.rule != "" && page.rule != "normal") || page.second != "" || page.captchaURL != "" {
		return fmt.Errorf("%w: 当前认证需要验证码、风控或二次验证，请在浏览器完成", exceptions.ErrLogin)
	}
	cipher, err := encryptSIDValue(page.key, password)
	if err != nil {
		return fmt.Errorf("%w: 无效的认证加密参数", exceptions.ErrLogin)
	}
	captcha, err := encryptSIDValue(page.key, "{}")
	if err != nil {
		return fmt.Errorf("%w: 无效的认证加密参数", exceptions.ErrLogin)
	}
	if err := a.checkSIDCaptcha(username, entry); err != nil {
		return err
	}
	form := url.Values{"username": {username}, "type": {"UsernamePassword"}, "_eventId": {"submit"}, "geolocation": {""}, "execution": {page.execution}, "captcha_code": {""}, "croypto": {page.key}, "password": {cipher}, "captcha_payload": {captcha}}
	// The published form action="login" resolves without the service query.
	req, err = a.newRequest(http.MethodPost, sidPortalBase+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("%w: 无法构造登录表单", exceptions.ErrLogin)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Origin", sidPortalBase)
	req.Header.Set("Referer", entry)
	req.Header.Del("X-Requested-With")
	resp, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: 新版登录请求失败（未自动重试）", exceptions.ErrNetwork)
	}
	defer drainBody(resp)
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxReadBytes+1))
	if err != nil || len(body) > maxReadBytes {
		return fmt.Errorf("%w: 无法完整读取登录响应", exceptions.ErrLogin)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: 登录响应 HTTP %d", exceptions.ErrLogin, resp.StatusCode)
	}
	result, err := parseSIDPage(body)
	if err != nil {
		return err
	}
	if result.errorCode != "" {
		return sidLoginError(result.errorCode)
	}
	if result.execution != "" || result.key != "" || resp.Request == nil || resp.Request.URL == nil || resp.Request.URL.Host != service.Host || resp.Request.Method != http.MethodGet || ticketRejected(resp, a.resolvedService, body) {
		return fmt.Errorf("%w: 认证未完成或业务系统票据未被接受，请检查验证码或二次验证", exceptions.ErrLogin)
	}
	// A response on the right host but outside the requested service directory
	// (e.g. a business login page or proxy landing page) is not authentication.
	prefix := service.Path[:strings.LastIndex(service.Path, "/")+1]
	if service.Host == "course.buct.edu.cn" && strings.HasPrefix(service.Path, "/meol/") {
		// The SSO callback under homepage/common redirects to homepage/student.
		prefix = "/meol/"
	}
	if !strings.HasPrefix(resp.Request.URL.Path, prefix) || strings.Contains(strings.ToLower(resp.Request.URL.Path), "login") && resp.Request.URL.Path != service.Path {
		return fmt.Errorf("%w: 未到达预期业务页面", exceptions.ErrLogin)
	}
	a.isLoggedIn = true
	a.activeSID = true
	return nil
}
