package auth

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/Ling0727-ai/go-buct-course/exceptions"
)

// BUCTAuth 北化课程平台认证结构体
type BUCTAuth struct {
	Client     *http.Client
	BaseURL    string
	isLoggedIn bool
}

// New 创建一个新的认证实例
func New() *BUCTAuth {
	// 初始化 CookieJar，用于自动管理 Session/Cookie
	jar, _ := cookiejar.New(nil)

	return &BUCTAuth{
		Client: &http.Client{
			Jar:     jar,
			Timeout: 10 * time.Second, // 对应 requests 的 timeout=10
		},
		BaseURL:    "https://course.buct.edu.cn",
		isLoggedIn: false,
	}
}

// userAgent 用于所有请求的 User-Agent 头
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"

// Login 登录到北化课程平台
func (a *BUCTAuth) Login(username, password string) error {
	// 1. 重置 Session (对应 Python: self.session = requests.Session())
	// 在 Go 中，给 Client 换一个新的 CookieJar 即可清空旧 Cookie
	newJar, _ := cookiejar.New(nil)
	a.Client.Jar = newJar
	a.isLoggedIn = false

	// 2. 先访问登录页面，获取初始 session cookie (JSESSIONID)
	loginPageURL := fmt.Sprintf("%s/meol/login.do", a.BaseURL)
	req, err := http.NewRequest("GET", loginPageURL, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", exceptions.ErrNetwork, err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := a.Client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", exceptions.ErrNetwork, err)
	}
	resp.Body.Close()

	// 3. 构造表单数据
	formData := url.Values{}
	formData.Set("IPT_LOGINUSERNAME", username)
	formData.Set("IPT_LOGINPASSWORD", password)
	formData.Set("logintoken", fmt.Sprintf("%d", time.Now().UnixMilli()))

	// 4. 发送 POST 请求
	loginURL := fmt.Sprintf("%s/meol/loginCheck.do", a.BaseURL)
	req, err = http.NewRequest("POST", loginURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return fmt.Errorf("%w: %v", exceptions.ErrNetwork, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", a.BaseURL)
	req.Header.Set("Referer", loginPageURL)

	resp, err = a.Client.Do(req)
	if err != nil {
		// 网络层面错误 (DNS, 连接拒绝, 超时等)
		return fmt.Errorf("%w: %v", exceptions.ErrNetwork, err)
	}
	defer resp.Body.Close()

	// 5. 检查登录是否成功
	// http.Client 默认会自动跟随重定向。
	// resp.Request.URL 就是最终重定向后的 URL。
	finalURL := resp.Request.URL.String()

	// 如果 URL 仍然包含 login.do 或 loginCheck.do，说明被重定向回了登录页（登录失败）
	if strings.Contains(finalURL, "login.do") || strings.Contains(finalURL, "loginCheck.do") {
		a.isLoggedIn = false
		return exceptions.ErrLogin
	}

	a.isLoggedIn = true
	return nil
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
	// 尝试调用服务器注销接口
	logoutURL := "https://portal.buct.edu.cn/cas/logout"

	// 忽略错误，只管发请求
	_, _ = a.Client.Get(logoutURL)

	// 无论如何，清空本地 Session
	newJar, _ := cookiejar.New(nil)
	a.Client.Jar = newJar
	a.isLoggedIn = false
}

// SetBaseURL 设置基础URL
func (a *BUCTAuth) SetBaseURL(urlStr string) {
	a.BaseURL = strings.TrimRight(urlStr, "/")
}
