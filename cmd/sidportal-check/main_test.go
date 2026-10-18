package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type testTransport struct{ calls int }

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
}

func TestStrictTransportRejectsOtherLoginRoutes(t *testing.T) {
	for _, target := range []struct{ method, url string }{
		{"GET", "https://portal.buct.edu.cn/cas/login"},
		{"POST", "https://portal.buct.edu.cn/cas/username-password/login"},
		{"GET", "http://sidportal.buct.edu.cn/login"},
		{"POST", "https://course.buct.edu.cn/meol/loginCheck.do"},
		{"GET", "https://other.invalid/login"},
		{"GET", "https://sidportal.buct.edu.cn/cas/login"},
	} {
		t.Run(target.url, func(t *testing.T) {
			base := &testTransport{}
			transport := strictTransport{base: base}
			req, _ := http.NewRequest(target.method, target.url, nil)
			if _, err := transport.RoundTrip(req); err == nil || base.calls != 0 {
				t.Fatal("forbidden route reached network")
			}
		})
	}
}

func TestStrictTransportAllowsNewLoginAndBusinessGET(t *testing.T) {
	for _, target := range []struct{ method, url string }{
		{"GET", "https://sidportal.buct.edu.cn/login?service=synthetic"},
		{"POST", "https://sidportal.buct.edu.cn/login"},
		{"GET", "https://sidportal.buct.edu.cn/api/protected/user/findCaptchaCount/synthetic"},
		{"GET", "https://course.buct.edu.cn/meol/homepage/student/index.jsp"},
	} {
		base := &testTransport{}
		transport := strictTransport{base: base}
		req, _ := http.NewRequest(target.method, target.url, nil)
		resp, err := transport.RoundTrip(req)
		if err != nil || base.calls != 1 {
			t.Fatalf("valid route rejected: %v", err)
		}
		resp.Body.Close()
	}
}
