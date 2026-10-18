// Command sidportal-check tests only the new sidportal login, once.
// Credentials are read from the terminal and never stored or printed.
package main

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/Ling0727-ai/go-buct-course/auth"
	"golang.org/x/term"
)

// strictTransport enforces the test's network boundary, even if a server
// redirects back to the old authentication system. Logs omit queries/bodies.
type strictTransport struct{ base http.RoundTripper }

func (s strictTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	allowed := req.URL.User == nil && req.URL.Scheme == "https"
	switch req.URL.Host {
	case "sidportal.buct.edu.cn":
		allowed = allowed && (req.Method == http.MethodGet && (path == "/login" || strings.HasPrefix(path, "/api/protected/user/findCaptchaCount/")) || req.Method == http.MethodPost && path == "/login")
	case "course.buct.edu.cn":
		allowed = allowed && req.Method == http.MethodGet && strings.HasPrefix(path, "/meol/")
	default:
		allowed = false
	}
	if !allowed {
		return nil, fmt.Errorf("strict sidportal test blocked unexpected destination (no fallback)")
	}
	if strings.HasPrefix(path, "/api/protected/user/findCaptchaCount/") {
		path = "/api/protected/user/findCaptchaCount/<redacted>"
	}
	// Do not expose jsessionid embedded in callback path parameters.
	if i := strings.IndexByte(path, ';'); i >= 0 {
		path = path[:i] + ";<redacted>"
	}
	fmt.Printf("[sidportal-only] %s %s%s\n", req.Method, req.URL.Host, path)
	return s.base.RoundTrip(req)
}

func run() error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("请在本机终端中运行，密码使用无回显输入")
	}
	fmt.Println("强制新版 sidportal：仅一次登录提交；禁止旧门户和其他登录域名；不抓取作业。")
	fmt.Print("学号: ")
	username, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return fmt.Errorf("读取学号失败")
	}
	username = strings.TrimSpace(username)
	fmt.Print("密码（不回显）: ")
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("读取密码失败")
	}
	defer func() {
		for i := range password {
			password[i] = 0
		}
	}()
	a := auth.New()
	a.SetService("https://course.buct.edu.cn/meol/homepage/common/sso_login.jsp")
	a.SetLoginURL("https://sidportal.buct.edu.cn/login")
	a.Client.Transport = strictTransport{base: http.DefaultTransport}
	if err := a.Login(username, string(password)); err != nil {
		return err
	}
	if !a.IsLoggedIn() {
		return fmt.Errorf("未建立登录状态")
	}
	fmt.Println("新版登录通过，已回跳课程平台；没有调用旧版登录。")
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "新版测试失败（不回退、不重试）:", err)
		os.Exit(1)
	}
}
