package auth

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Ling0727-ai/go-buct-course/exceptions"
)

// Synthetic vectors generated offline with the site's crypto-merged.min.js.
// Key and plaintexts are test data, never account/session material.
func TestSIDEncryptMatchesSiteJS(t *testing.T) {
	cases := []struct{ plain, cipher string }{
		{"", "lU9k8uTobp7ugtICFmhImQ=="},
		{"{}", "XjROMe16mC4WGbUXWRQLQA=="},
		{"test-password", "/mLb7Dc4tKwWZs4PhFsX+w=="},
		{"1234567890abcdef", "GRCEZJw3tEoS6apnZfFxuZVPZPLk6G6e7oLSAhZoSJk="},
		{"密码ＰＡＳＳ 123", "6dBak5SCF0whajQMD7LPjqOdD5gh2j7foi0TqS+7NI0="},
		{strings.Repeat("long password ", 5), "GsBiioxq9LEjQZLGSTqna+d/35II6lHbP7GIm/KyYJ4ntWqES183m3ePoVAYLIG25tcSXfurhacLLgzaF0JCwyeS4Lwdcq1++oizDq23/Nc="},
	}
	for _, tc := range cases {
		got, err := encryptSIDValue("AAECAwQFBgcICQoLDA0ODw==", tc.plain)
		if err != nil || got != tc.cipher {
			t.Errorf("encrypt(%q)=%q, %v; want %q", tc.plain, got, err, tc.cipher)
		}
	}
	for _, key := range []string{"", "not base64", "YWJj"} {
		if _, err := encryptSIDValue(key, "pw"); err == nil {
			t.Errorf("accepted invalid key %q", key)
		}
	}
}

type sidTransport struct {
	t         *testing.T
	page      string
	result    string
	preflight string
	status    int
	redirect  string
	viaPortal bool
	posts     int
	gets      int
	forms     []url.Values
}

func sidFixture() string {
	return `<p id="login-page-flowkey">synthetic-execution+token</p><p id="login-croypto">AAECAwQFBgcICQoLDA0ODw==</p><p id="recaptchaVendor">system</p><p id="riskSystemSwitch">default</p><p id="login-rule-type">normal</p><p id="sso-second"></p><p id="captcha-url"></p>`
}

func (f *sidTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.t.Helper()
	switch {
	case req.URL.Host == "course.buct.edu.cn" && req.URL.Path == meolSSOPath && req.URL.Query().Get("ticket") == "":
		target := sidPortalBase + "/login"
		if f.viaPortal {
			target = casBase + "/login"
		}
		return fakeResponse(req, 302, map[string]string{"Location": target + "?service=" + url.QueryEscape(fakeMEOLService), "Set-Cookie": "JSESSIONID=synthetic-meol; Path=/"}, ""), nil
	case req.URL.Host == "portal.buct.edu.cn" && req.URL.Path == "/cas/login":
		return fakeResponse(req, 302, map[string]string{"Location": sidPortalBase + "/login?service=" + url.QueryEscape(fakeMEOLService)}, ""), nil
	case req.URL.Host == "sidportal.buct.edu.cn" && req.URL.Path == "/login" && req.Method == http.MethodGet:
		f.gets++
		if req.URL.Query().Get("service") != fakeMEOLService {
			f.t.Error("lost service query")
		}
		return fakeResponse(req, 200, map[string]string{"Set-Cookie": "SIDSESSION=synthetic-sid; Path=/; Secure"}, f.page), nil
	case req.URL.Host == "sidportal.buct.edu.cn" && strings.HasPrefix(req.URL.Path, "/api/protected/user/findCaptchaCount/"):
		encoded := base64.StdEncoding.EncodeToString([]byte(req.Header.Get("Csrf-Key")))
		sum := md5.Sum([]byte(encoded[:len(encoded)/2] + encoded + encoded[len(encoded)/2:]))
		if len(req.Header.Get("Csrf-Key")) != 32 || req.Header.Get("Csrf-Value") != hex.EncodeToString(sum[:]) {
			f.t.Error("CSRF contract mismatch")
		}
		if _, err := req.Cookie("SIDSESSION"); err != nil {
			f.t.Error("preflight lost session cookie")
		}
		return fakeResponse(req, 200, nil, f.preflight), nil
	case req.URL.Host == "sidportal.buct.edu.cn" && req.URL.Path == "/login" && req.Method == http.MethodPost:
		f.posts++
		body, _ := io.ReadAll(req.Body)
		form, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, err
		}
		f.forms = append(f.forms, form)
		if req.URL.RawQuery != "" {
			f.t.Error("form action should not preserve query")
		}
		if _, err := req.Cookie("SIDSESSION"); err != nil {
			f.t.Error("POST lost session cookie")
		}
		if req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || req.Header.Get("Origin") != sidPortalBase || !strings.HasPrefix(req.Header.Get("Referer"), sidPortalBase+"/login?") || req.Header.Get("X-Requested-With") != "" {
			f.t.Error("navigation headers mismatch")
		}
		if f.result != "" {
			return fakeResponse(req, 200, nil, f.result), nil
		}
		status := f.status
		if status == 0 {
			status = 302
		}
		redirect := f.redirect
		if redirect == "" {
			redirect = fakeMEOLService + "?ticket=synthetic-ticket"
		}
		return fakeResponse(req, status, map[string]string{"Location": redirect}, ""), nil
	case req.URL.Host == "course.buct.edu.cn" && req.URL.Query().Get("ticket") != "":
		return fakeResponse(req, 302, map[string]string{"Location": "/meol/homepage/student/index.jsp", "Set-Cookie": "JSESSIONID=authenticated-test; Path=/"}, ""), nil
	case req.URL.Host == "course.buct.edu.cn" && req.URL.Path == "/meol/homepage/student/index.jsp":
		if cookie, err := req.Cookie("JSESSIONID"); err != nil || cookie.Value != "authenticated-test" {
			f.t.Error("business callback lost authenticated session cookie")
		}
		return fakeResponse(req, 200, nil, "<html>course homepage</html>"), nil
	case req.URL.Host == "sidportal.buct.edu.cn" && req.URL.Path == "/logout":
		return fakeResponse(req, 200, nil, "logged out"), nil
	}
	return nil, fmt.Errorf("unexpected request %s %s", req.Method, req.URL)
}

func newSIDAuth(t *testing.T) (*BUCTAuth, *sidTransport) {
	t.Helper()
	f := &sidTransport{t: t, page: sidFixture(), preflight: `{"code":200,"data":{"captchaInvisible":false}}`}
	a := New()
	a.Client.Transport = f
	return a, f
}

func TestSIDLoginFlow(t *testing.T) {
	for _, viaPortal := range []bool{false, true} {
		t.Run(fmt.Sprint(viaPortal), func(t *testing.T) {
			a, f := newSIDAuth(t)
			f.viaPortal = viaPortal
			if err := a.Login("synthetic-student", "密码ＰＡＳＳ 123"); err != nil {
				t.Fatal(err)
			}
			if !a.IsLoggedIn() || f.posts != 1 {
				t.Fatal("login state/POST count incorrect")
			}
			form := f.forms[0]
			want := url.Values{"username": {"synthetic-student"}, "password": {"6dBak5SCF0whajQMD7LPjqOdD5gh2j7foi0TqS+7NI0="}, "type": {"UsernamePassword"}, "_eventId": {"submit"}, "geolocation": {""}, "execution": {"synthetic-execution+token"}, "croypto": {"AAECAwQFBgcICQoLDA0ODw=="}, "captcha_code": {""}, "captcha_payload": {"XjROMe16mC4WGbUXWRQLQA=="}}
			if form.Encode() != want.Encode() {
				t.Errorf("form=%v, want=%v", form, want)
			}
			a.Logout()
			if a.IsLoggedIn() || a.activeSID {
				t.Error("logout did not reset state")
			}
		})
	}
}

func TestSIDExplicitServiceAndEntry(t *testing.T) {
	a, f := newSIDAuth(t)
	a.SetService(fakeMEOLService)
	if err := a.Login("synthetic-student", "test-password"); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 {
		t.Fatal("expected one POST")
	}
	a.SetLoginURL(sidPortalBase + "/login")
	if err := a.Login("synthetic-student", "test-password"); err != nil {
		t.Fatal(err)
	}
	if f.gets != 2 || f.posts != 2 {
		t.Fatal("session not refreshed")
	}
}

func TestSIDChallengeAndInvalidPageStopBeforePOST(t *testing.T) {
	cases := []struct{ name, page, preflight string }{
		{"geetest", strings.Replace(sidFixture(), ">system<", ">geetest<", 1), ""},
		{"risk", strings.Replace(sidFixture(), ">default<", ">USTC<", 1), ""},
		{"second", strings.Replace(sidFixture(), `id="sso-second"></p>`, `id="sso-second">true</p>`, 1), ""},
		{"captcha", "", `{"code":200,"data":{"captchaInvisible":true}}`},
		{"bad-preflight", "", `{"code":403,"data":{}}`},
		{"missing-data", "", `{"code":200}`},
		{"missing-key", `<p id="login-page-flowkey">execution</p>`, ""},
		{"invalid-key", strings.Replace(sidFixture(), "AAECAwQFBgcICQoLDA0ODw==", "invalid", 1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, f := newSIDAuth(t)
			if tc.page != "" {
				f.page = tc.page
			}
			if tc.preflight != "" {
				f.preflight = tc.preflight
			}
			err := a.Login("synthetic-student", "pw")
			if !errors.Is(err, exceptions.ErrLogin) || a.IsLoggedIn() || f.posts != 0 {
				t.Fatalf("err=%v, logged=%v, posts=%d", err, a.IsLoggedIn(), f.posts)
			}
		})
	}
}

func TestSIDRejectsFailedResponsesWithoutRetry(t *testing.T) {
	cases := []struct {
		name, result, redirect string
		status                 int
	}{
		{name: "wrong-password", result: sidFixture() + `<p id="login-error-code">1030027</p>`},
		{name: "locked", result: sidFixture() + `<p id="login-error-msg">1030028</p>`},
		{name: "verification", result: sidFixture() + `<p id="login-error-code">1320007</p>`},
		{name: "same-page", result: sidFixture()},
		{name: "unknown-page", result: `<html>unknown</html>`},
		{name: "cross-origin", redirect: "https://untrusted.invalid/"},
		{name: "replay-307", redirect: fakeMEOLService + "?ticket=test", status: 307},
		{name: "replay-308", redirect: fakeMEOLService + "?ticket=test", status: 308},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, f := newSIDAuth(t)
			f.result = tc.result
			f.redirect = tc.redirect
			f.status = tc.status
			err := a.Login("synthetic-student", "pw")
			if err == nil || a.IsLoggedIn() || f.posts != 1 {
				t.Fatalf("err=%v, logged=%v, posts=%d", err, a.IsLoggedIn(), f.posts)
			}
		})
	}
}
