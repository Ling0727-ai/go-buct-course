package course_utils

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const mainPath = "/meol/jpk/course/layout/newpage/index.jsp?courseId=42"
const columnPath = "/meol/buildless/colUrlStuView.do?columnId=9"
const listPath = "/meol/common/hw/student/hwtask.jsp"

func homeworkHTML(id string) string {
	return fmt.Sprintf(`<meta charset="utf-8"><table class="valuelist"><tr><th>标题</th><th>截止时间</th></tr><tr><td><a class="infolist" href="hwtask.view.jsp?hwtid=%s&amp;private=1">章节练习</a></td><td>2099-10-21 18:00:00</td><td>暂无分数</td><td>老师</td><td></td><td><a class="enter" href="write.jsp?hwtid=%s">提交</a></td><td></td><td></td></tr></table>`, id, id)
}

type courseFixtureTransport func(*http.Request) (*http.Response, error)

func (f courseFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// Keep discovery regressions offline: local filtering proxies may rewrite HTML
// even when httptest binds loopback, corrupting literal navigation fixtures.
func fixtureManager(t *testing.T, pages map[string]string) (*Manager, *[]string) {
	t.Helper()
	calls := []string{}
	client := &http.Client{Transport: courseFixtureTransport(func(r *http.Request) (*http.Response, error) {
		key := r.URL.RequestURI()
		calls = append(calls, key)
		body, ok := pages[key]
		if !ok {
			t.Errorf("unexpected request %s", r.URL)
			return nil, fmt.Errorf("unexpected request %s", r.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/html; charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}
	m := New(client)
	m.BaseURL = "https://course.example"
	return m, &calls
}

func TestDiscoverArbitraryColumnsAndFrames(t *testing.T) {
	m, calls := fixtureManager(t, map[string]string{
		mainPath:   `<a href="/meol/buildless/colUrlStuView.do?columnId=9">章节练习</a>`,
		columnPath: `<iframe src="../common/hw/student/hwtask.jsp"></iframe><iframe src="../common/hw/student/hwtask.jsp#again"></iframe>`,
		listPath:   homeworkHTML("123"),
	})
	result, err := m.GetCourseDetails("42")
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 1 || len(*calls) != 3 {
		t.Fatalf("result=%+v calls=%v", result, *calls)
	}
	hw := result.HomeworkList[0]
	if hw.Title != "章节练习" || !hw.CanSubmit || !strings.Contains(hw.DetailHref, "private=1") {
		t.Fatalf("bad homework: %+v", hw)
	}
}

func TestPaginationAndDuplicateContent(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprint(duplicate), func(t *testing.T) {
			first := listPath + "?pagingNumberPer=1&pagingPage=1"
			second := listPath + "?pagingNumberPer=1&pagingPage=2"
			body := `<table class="valuelist"></table>`
			if duplicate {
				body = homeworkHTML("1")
			}
			m, calls := fixtureManager(t, map[string]string{first: homeworkHTML("1"), second: body})
			result, err := m.GetHomeworkByURL(m.BaseURL+first, "42")
			if err != nil || result.TotalCount != 1 || len(*calls) != 2 {
				t.Fatalf("result=%+v err=%v calls=%v", result, err, *calls)
			}
		})
	}
}

func TestNeverFollowActionOrExternalLinks(t *testing.T) {
	m, calls := fixtureManager(t, map[string]string{columnPath: `<iframe src="https://example.com/meol/buildless/colUrlStuView.do"></iframe><a href="/meol/common/hw/student/write.jsp?hwtid=1">提交</a>`})
	result, err := m.GetHomeworkByURL(m.BaseURL+columnPath, "42")
	if err != nil || result.TotalCount != 0 || len(*calls) != 1 {
		t.Fatalf("result=%+v err=%v calls=%v", result, err, *calls)
	}
}

func TestLoginRedirectIsError(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: courseFixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": {"https://sidportal.buct.edu.cn/login"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})}
	m := New(client)
	m.BaseURL = "https://course.example"
	if _, err := m.GetCourseDetails("42"); err == nil {
		t.Fatal("login redirect must fail")
	}
	if calls != 1 {
		t.Fatalf("followed redirect: %d", calls)
	}
}

func TestLiteralScriptAndMetaNavigation(t *testing.T) {
	for _, markup := range []string{`<script>window.location.href="` + listPath + `";</script>`, `<meta http-equiv="refresh" content="0; URL = '` + listPath + `'">`} {
		m, calls := fixtureManager(t, map[string]string{columnPath: markup, listPath: homeworkHTML("1")})
		result, err := m.GetHomeworkByURL(m.BaseURL+columnPath, "42")
		if err != nil || result.TotalCount != 1 || len(*calls) != 2 {
			t.Fatalf("result=%+v err=%v calls=%v", result, err, *calls)
		}
	}
}
