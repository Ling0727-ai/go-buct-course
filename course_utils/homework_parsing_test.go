package course_utils

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func homeworkDocument(t *testing.T, body string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func horizontalHomework(id, deadline, score, submit, result string) string {
	return fmt.Sprintf(`<tr><td><a class="infolist" href="hwtask.view.jsp?hwtid=%s&amp;courseId=42&amp;mode=detail">Task %s</a></td><td>%s</td><td>%s</td><td>Teacher</td><td></td><td>%s</td><td>%s</td><td></td></tr>`, id, id, deadline, score, submit, result)
}

func TestHomeworkMixedTablesAndSourceLinks(t *testing.T) {
	m := New(http.DefaultClient)
	source := m.BaseURL + "/meol/common/hw/student/hwtask_blended.jsp?columnId=7"
	body := `<table class="valuelist"><tr><th>标题</th><th>截止时间</th></tr>` +
		horizontalHomework("11", "2099-01-02 12:00", "暂无分数", `<a class="enter" href="../student/hwanswer.jsp?hwtid=11&amp;token=abc">提交</a>`, "") +
		horizontalHomework("12", "2000年01月02日 12:00:00", "暂无分数", "", "") +
		horizontalHomework("bad", "", "", "", "") + `</table>` +
		`<table class="valuelist"><tr><th>作业标题</th><td><a href="hwtask.view.jsp?hwtid=13&amp;courseId=42">Completed</a></td></tr><tr><th>截止时间</th><td>2099年1月2日 12:00</td></tr><tr><th>评分结果</th><td>未评分</td></tr><tr><th>提交作业</th><td><a href="hwanswer.jsp?hwtid=13">提交</a></td></tr><tr><th>查看结果</th><td><a href="hwresult.jsp?hwtid=13&amp;attempt=2">结果</a></td></tr></table>` +
		`<table class="valuelist">` + horizontalHomework("11", "", "", "", "") + `</table>`
	list := m.parseHomeworkPage(homeworkDocument(t, body), source)
	if len(list) != 3 {
		t.Fatalf("want three distinct valid tasks, got %+v", list)
	}
	if list[0].HwTID != "11" || !list[0].CanSubmit || list[0].Status != "未提交" {
		t.Fatalf("pending task: %+v", list[0])
	}
	u, err := url.Parse(list[0].DetailHref)
	if err != nil || u.Query().Get("courseId") != "42" || u.Query().Get("mode") != "detail" {
		t.Fatalf("lost original detail query: %s", list[0].DetailHref)
	}
	if list[0].SubmitHref != m.BaseURL+"/meol/common/hw/student/hwanswer.jsp?hwtid=11&token=abc" {
		t.Fatalf("wrong relative submit URL: %s", list[0].SubmitHref)
	}
	if list[0].SourceURL != source {
		t.Fatalf("wrong source: %s", list[0].SourceURL)
	}
	if list[1].CanSubmit || list[1].Status != "已截止" {
		t.Fatalf("expired task discarded or actionable: %+v", list[1])
	}
	if !list[2].HasResult || list[2].CanSubmit || list[2].Status != "已完成" {
		t.Fatalf("completed task: %+v", list[2])
	}
	if !strings.Contains(list[2].ResultHref, "attempt=2") {
		t.Fatalf("lost result query: %s", list[2].ResultHref)
	}
}

func TestHomeworkDisabledLinks(t *testing.T) {
	m := New(http.DefaultClient)
	for _, attrs := range []string{
		`disabled href="submit.jsp?hwtid=1"`, `aria-disabled="true" href="submit.jsp?hwtid=1"`,
		`class="enter disabled" href="submit.jsp?hwtid=1"`, `href="#"`, `href="javascript:submit()"`, `href=""`,
	} {
		t.Run(attrs, func(t *testing.T) {
			body := `<table class="valuelist"><tr><th>标题</th><td><a href="hwtask.view.jsp?hwtid=1">One</a></td></tr><tr><th>提交作业</th><td><a ` + attrs + `>提交</a></td></tr><tr><th>查看结果</th><td><a ` + attrs + `>结果</a></td></tr></table>`
			list := m.parseHomeworkPage(homeworkDocument(t, body), m.homeworkListURL())
			if len(list) != 1 {
				t.Fatalf("want retained task, got %d", len(list))
			}
			hw := list[0]
			if hw.CanSubmit || hw.HasResult || hw.SubmitHref != "" || hw.ResultHref != "" {
				t.Fatalf("disabled control actionable: %+v", hw)
			}
		})
	}
}

func TestHomeworkVerticalPlainTitleAndMissingID(t *testing.T) {
	m := New(http.DefaultClient)
	body := `<table class="valuelist"><tr><th>标题</th><td>Plain title</td></tr><tr><th>评分结果</th><td>95</td></tr></table>`
	list := m.parseHomeworkPage(homeworkDocument(t, body), m.homeworkListURL()+"?hwtid=27")
	if len(list) != 1 || list[0].Title != "Plain title" || list[0].Status != "已完成" {
		t.Fatalf("source ID/plain title: %+v", list)
	}
	if got := m.parseHomeworkPage(homeworkDocument(t, body), m.homeworkListURL()); len(got) != 0 {
		t.Fatalf("accepted missing ID: %+v", got)
	}
	for _, href := range []string{"?hwtid=", "?hwtid=0", "?hwtid=abc", "?hwtid=-1", "?hwtid=1%26other=2", "?not_hwtid=1"} {
		if id := homeworkID(href); id != "" {
			t.Errorf("accepted invalid ID %q from %q", id, href)
		}
	}
}

func TestHomeworkDeadlineAndStatus(t *testing.T) {
	m := New(http.DefaultClient)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, homeworkTimezone)
	for _, tc := range []struct {
		name, deadline, score, result, status, remaining string
		canSubmit, urgent                                bool
	}{
		{"chinese", "2026年10月19日 12:00:00", "暂无分数", "", "未提交", "1天0小时0分钟", true, true},
		{"iso", "2026-10-18 13:30", "未评分", "", "未提交", "1小时30分钟", true, true},
		{"isoT", "2026-10-20T12:00:00", "暂无成绩", "", "未提交", "2天0小时0分钟", true, false},
		{"utc", "2026-10-18T05:00:00Z", "", "", "未提交", "1小时0分钟", true, true},
		{"expired", "2026-10-18 11:59:00", "", "", "已截止", "已过期", false, false},
		{"equal", "2026-10-18 12:00:00", "", "", "已截止", "已过期", false, false},
		{"unknown", "待定", "", "", "截止时间未知", "时间格式错误", false, false},
		{"unlimited", "不限", "", "", "未提交", "无截止时间", true, false},
		{"result", "2026-10-18 13:00:00", "未评分", "https://example.org/result", "已完成", "1小时0分钟", false, false},
		{"scored", "2026-10-18 13:00:00", "0", "", "已完成", "1小时0分钟", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hw := &Homework{HwTID: "1", Deadline: tc.deadline, Score: tc.score, ResultHref: tc.result, SubmitHref: "https://example.org/submit"}
			m.finishHomework(hw, m.homeworkListURL(), now)
			if hw.CanSubmit != tc.canSubmit || hw.IsUrgent != tc.urgent || hw.Status != tc.status || hw.TimeRemaining != tc.remaining {
				t.Fatalf("got %+v", hw)
			}
		})
	}
	parsed, err := parseHomeworkDeadline("2026年1月2日 03:04")
	if err != nil {
		t.Fatal(err)
	}
	_, offset := parsed.Zone()
	if offset != 8*60*60 || parsed.Hour() != 3 {
		t.Fatalf("deadline not interpreted in UTC+8: %v", parsed)
	}
}

func TestGetHomeworkByURLDeduplicatesAcrossPages(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page := `<table class="valuelist">` + horizontalHomework("61", "2099-01-01 12:00", "未评分", "", "") + `</table>`
		if r.URL.Query().Get("page") == "2" {
			page += `<table class="valuelist">` + horizontalHomework("62", "2000-01-01 12:00", "90", "", "") + `</table>`
		} else {
			page += `<a href="hwtask.jsp?page=2">下一页</a>`
		}
		fmt.Fprint(w, page)
	}))
	defer server.Close()
	m := New(server.Client())
	m.BaseURL = server.URL
	result, err := m.GetHomeworkByURL(server.URL+"/meol/common/hw/student/hwtask.jsp", "42")
	if err != nil {
		t.Fatal(err)
	}
	if result.LID != "42" || result.TotalCount != 2 || len(result.HomeworkList) != 2 {
		t.Fatalf("wrong traversal result: %+v", result)
	}
	if result.HomeworkList[1].Status != "已完成" {
		t.Fatalf("completed task lost: %+v", result.HomeworkList[1])
	}
}
