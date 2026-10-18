package course_utils

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/Ling0727-ai/go-buct-course/exceptions"
	"github.com/Ling0727-ai/go-buct-course/lid_utils"
	"github.com/Ling0727-ai/go-buct-course/utils"
)

// Homework 作业详情结构体
type Homework struct {
	Title         string `json:"title"`
	DetailHref    string `json:"detail_href"`
	HwTID         string `json:"hwtid"`
	SourceURL     string `json:"source_url,omitempty"`
	IsGroup       bool   `json:"is_group"`
	Deadline      string `json:"deadline"`
	Score         string `json:"score"`
	Publisher     string `json:"publisher"`
	SubmitHref    string `json:"submit_href"`
	CanSubmit     bool   `json:"can_submit"`
	ResultHref    string `json:"result_href"`
	HasResult     bool   `json:"has_result"`
	Status        string `json:"status"`
	TimeRemaining string `json:"time_remaining"`
	IsUrgent      bool   `json:"is_urgent"`
}

// CourseDetail 课程作业列表详情结构体
type CourseDetail struct {
	LID          string          `json:"lid"`
	CourseName   string          `json:"course_name"`
	CourseInfo   *lid.CourseInfo `json:"course_info,omitempty"`
	HomeworkList []*Homework     `json:"homework_list"`
	TotalCount   int             `json:"total_count"`
	UrgentCount  int             `json:"urgent_count"`
}

// HomeworkTaskDetail 作业具体内容结构体
type HomeworkTaskDetail struct {
	HwTID       string   `json:"hwtid"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tasks       []string `json:"tasks"`
}

// Manager 课程工具管理器
type Manager struct {
	Client     *http.Client
	BaseURL    string
	LidManager *lid.Manager
}

func New(client *http.Client) *Manager {
	return &Manager{Client: client, BaseURL: "https://course.buct.edu.cn", LidManager: lid.New(client)}
}

func (m *Manager) GetPendingHomework() ([]*lid.CourseInfo, error) {
	return m.LidManager.GetHomeworkLids()
}

// GetCourseDetails 获取课程所有栏目中的作业信息。
func (m *Manager) GetCourseDetails(lidStr string) (*CourseDetail, error) {
	startURL := fmt.Sprintf("%s/meol/jpk/course/layout/newpage/index.jsp?courseId=%s", strings.TrimRight(m.BaseURL, "/"), url.QueryEscape(lidStr))
	return m.GetHomeworkByURL(startURL, lidStr)
}

// GetHomeworkByURL 从指定课程或作业栏目 URL 发现并读取作业。
func (m *Manager) GetHomeworkByURL(startURL, lidStr string) (*CourseDetail, error) {
	result := &CourseDetail{LID: lidStr, HomeworkList: []*Homework{}}
	seen := make(map[string]bool)
	err := m.walkHomeworkPages(startURL, func(sourceURL string, doc *goquery.Document) error {
		for _, hw := range m.parseHomeworkPage(doc, sourceURL) {
			if seen[hw.HwTID] {
				continue
			}
			seen[hw.HwTID] = true
			result.HomeworkList = append(result.HomeworkList, hw)
			if hw.IsUrgent {
				result.UrgentCount++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result.TotalCount = len(result.HomeworkList)
	return result, nil
}

// GetHomeworkDetail 获取单个作业详情。
func (m *Manager) GetHomeworkDetail(hwtid string) (*HomeworkTaskDetail, error) {
	detailURL := fmt.Sprintf("%s/meol/common/hw/student/hwtask.view.jsp?hwtid=%s", m.BaseURL, url.QueryEscape(hwtid))
	referer := fmt.Sprintf("%s/meol/common/hw/student/hwtask.jsp?tagbug=client&strStyle=new03", m.BaseURL)
	doc, err := m.fetchDocument(detailURL, referer)
	if err != nil {
		return nil, err
	}
	detail := &HomeworkTaskDetail{HwTID: hwtid}
	if title := doc.Find("h1, h2, h3").First(); title.Length() > 0 {
		detail.Title = strings.TrimSpace(title.Text())
	}
	if content := doc.Find("div.content, div.description").First(); content.Length() > 0 {
		detail.Description = strings.TrimSpace(content.Text())
	}
	return detail, nil
}

// GetHomeworkTasks 获取作业详情页面中的具体题目要求。
func (m *Manager) GetHomeworkTasks(detailURL string) ([]string, error) {
	fullURL, err := resolveCourseURL(m.homeworkListURL(), detailURL)
	if err != nil {
		return nil, err
	}
	doc, err := m.fetchDocument(fullURL.String(), m.homeworkListURL())
	if err != nil {
		return nil, err
	}
	var tasks []string
	doc.Find("input[type='hidden']").Each(func(_ int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		if !strings.Contains(name, "content") {
			return
		}
		value, _ := s.Attr("value")
		if value == "" {
			return
		}
		contentDoc, err := goquery.NewDocumentFromReader(strings.NewReader(html.UnescapeString(value)))
		if err != nil {
			return
		}
		pTags := contentDoc.Find("p")
		if pTags.Length() == 0 {
			if text := strings.TrimSpace(contentDoc.Text()); text != "" {
				tasks = append(tasks, text)
			}
			return
		}
		pTags.Each(func(_ int, p *goquery.Selection) {
			if text := strings.TrimSpace(p.Text()); text != "" {
				tasks = append(tasks, text)
			}
		})
	})
	if len(tasks) == 0 {
		doc.Find("div#body p").Each(func(_ int, p *goquery.Selection) {
			if text := strings.TrimSpace(p.Text()); text != "" {
				tasks = append(tasks, text)
			}
		})
	}
	return tasks, nil
}

func (m *Manager) GetAllPendingHomeworkDetails() ([]*CourseDetail, error) {
	pendingList, err := m.GetPendingHomework()
	if err != nil {
		return nil, err
	}
	var allDetails []*CourseDetail
	for _, courseInfo := range pendingList {
		details, err := m.GetCourseDetails(courseInfo.LID)
		if err != nil {
			fmt.Printf("获取课程 %s (LID: %s) 的作业详情失败: %v\n", courseInfo.CourseName, courseInfo.LID, err)
			continue
		}
		details.CourseName = courseInfo.CourseName
		details.CourseInfo = courseInfo
		allDetails = append(allDetails, details)
	}
	return allDetails, nil
}

// getHomeworkDetailSpecial1 保留旧接口，栏目发现统一由 GetCourseDetails 处理。
func (m *Manager) getHomeworkDetailSpecial1(courseID string) ([]*Homework, error) {
	result, err := m.GetCourseDetails(courseID)
	if err != nil {
		return nil, err
	}
	return result.HomeworkList, nil
}

func (m *Manager) homeworkListURL() string {
	return strings.TrimRight(m.BaseURL, "/") + "/meol/common/hw/student/hwtask.jsp"
}

func homeworkID(href string) string {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(u.Query().Get("hwtid"))
	// Platform IDs are decimal positive integers. Reject placeholders and malformed links.
	if id == "" {
		return ""
	}
	nonzero := false
	for _, c := range id {
		if c < '0' || c > '9' {
			return ""
		}
		if c != '0' {
			nonzero = true
		}
	}
	if !nonzero {
		return ""
	}
	return id
}

func homeworkLink(link *goquery.Selection, sourceURL string) string {
	if link.Length() == 0 {
		return ""
	}
	if _, disabled := link.Attr("disabled"); disabled {
		return ""
	}
	if aria, _ := link.Attr("aria-disabled"); strings.EqualFold(strings.TrimSpace(aria), "true") {
		return ""
	}
	classes, _ := link.Attr("class")
	if strings.Contains(strings.ToLower(classes), "disabled") {
		return ""
	}
	href, _ := link.Attr("href")
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return ""
	}
	u, err := resolveCourseURL(sourceURL, href)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}

// updateHwTID retains the actual detail URL, including non-ID query parameters.
func updateHwTID(hw *Homework, href, baseURL string) {
	if id := homeworkID(href); id != "" {
		hw.HwTID = id
		if resolved, err := resolveCourseURL(baseURL, href); err == nil {
			hw.DetailHref = resolved.String()
		}
	}
}

func (m *Manager) parseVerticalHomeworkTable(table *goquery.Selection) (*Homework, bool) {
	return m.parseVerticalHomeworkTableAt(table, m.homeworkListURL())
}

func (m *Manager) parseVerticalHomeworkTableAt(table *goquery.Selection, sourceURL string) (*Homework, bool) {
	isVertical := false
	table.Find("tr").Each(func(_ int, tr *goquery.Selection) {
		if tr.ChildrenFiltered("th").Length() == 1 && tr.ChildrenFiltered("td").Length() == 1 && strings.Contains(tr.ChildrenFiltered("th").Text(), "标题") {
			isVertical = true
		}
	})
	if !isVertical {
		return nil, false
	}
	hw := &Homework{}
	table.Find("tr").Each(func(_ int, tr *goquery.Selection) {
		ths, tds := tr.ChildrenFiltered("th"), tr.ChildrenFiltered("td")
		if ths.Length() != 1 || tds.Length() != 1 {
			return
		}
		label, value := strings.TrimSpace(ths.Text()), strings.TrimSpace(tds.Text())
		switch {
		case strings.Contains(label, "标题"):
			hw.Title = value
			tds.Find("a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
				href := homeworkLink(a, sourceURL)
				if homeworkID(href) == "" {
					return true
				}
				updateHwTID(hw, href, sourceURL)
				return false
			})
			hw.IsGroup = tds.Find("img[title='分组作业']").Length() > 0
		case strings.Contains(label, "截止"):
			hw.Deadline = value
		case strings.Contains(label, "评分") || strings.Contains(label, "分数"):
			hw.Score = value
		case strings.Contains(label, "发布人"):
			hw.Publisher = value
		case strings.Contains(label, "提交作业"):
			hw.SubmitHref = firstHomeworkLink(tds.Find("a[href]"), sourceURL)
		case strings.Contains(label, "查看结果"):
			hw.ResultHref = firstHomeworkLink(tds.Find("a[href]"), sourceURL)
		}
	})
	if hw.HwTID == "" {
		for _, href := range []string{hw.SubmitHref, hw.ResultHref, sourceURL} {
			if hw.HwTID = homeworkID(href); hw.HwTID != "" {
				break
			}
		}
	}
	if hw.HwTID == "" {
		return nil, true
	}
	m.finishHomework(hw, sourceURL, time.Now())
	return hw, true
}

func firstHomeworkLink(links *goquery.Selection, sourceURL string) string {
	var href string
	links.EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href = homeworkLink(a, sourceURL)
		return href == ""
	})
	return href
}

func (m *Manager) parseHomeworkRow(row *goquery.Selection) *Homework {
	return m.parseHomeworkRowAt(row, m.homeworkListURL())
}

func (m *Manager) parseHomeworkRowAt(row *goquery.Selection, sourceURL string) *Homework {
	cells := row.ChildrenFiltered("td")
	if cells.Length() < 4 {
		return nil
	}
	hw := &Homework{}
	cells.Eq(0).Find("a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href := homeworkLink(a, sourceURL)
		if homeworkID(href) == "" {
			return true
		}
		hw.Title = strings.TrimSpace(a.Text())
		updateHwTID(hw, href, sourceURL)
		return false
	})
	if hw.HwTID == "" {
		return nil
	}
	hw.IsGroup = row.Find("img[title='分组作业']").Length() > 0
	hw.Deadline = strings.TrimSpace(cells.Eq(1).Text())
	hw.Score = strings.TrimSpace(cells.Eq(2).Text())
	hw.Publisher = strings.TrimSpace(cells.Eq(3).Text())
	hw.SubmitHref = firstHomeworkLink(row.Find("a.enter[href]"), sourceURL)
	hw.ResultHref = firstHomeworkLink(row.Find("a.view[href]"), sourceURL)
	m.finishHomework(hw, sourceURL, time.Now())
	return hw
}

func (m *Manager) parseHomeworkPage(doc *goquery.Document, sourceURL string) []*Homework {
	list := []*Homework{}
	seen := make(map[string]bool)
	appendHomework := func(hw *Homework) {
		if hw != nil && !seen[hw.HwTID] {
			seen[hw.HwTID] = true
			list = append(list, hw)
		}
	}
	doc.Find("table.valuelist").Each(func(_ int, table *goquery.Selection) {
		if hw, vertical := m.parseVerticalHomeworkTableAt(table, sourceURL); vertical {
			appendHomework(hw)
			return
		}
		table.Find("tr").Each(func(_ int, row *goquery.Selection) { appendHomework(m.parseHomeworkRowAt(row, sourceURL)) })
	})
	return list
}

func (m *Manager) parseHomeworkTable(doc *goquery.Document, lidStr string) *CourseDetail {
	list := m.parseHomeworkPage(doc, m.homeworkListURL())
	result := &CourseDetail{LID: lidStr, HomeworkList: list, TotalCount: len(list)}
	for _, hw := range list {
		if hw.IsUrgent {
			result.UrgentCount++
		}
	}
	return result
}

var homeworkTimezone = time.FixedZone("UTC+8", 8*60*60)

func parseHomeworkDeadline(value string) (time.Time, error) {
	value = strings.Join(strings.Fields(value), " ")
	for _, layout := range []string{
		time.RFC3339Nano, "2006年1月2日 15:04:05", "2006年1月2日 15:04",
		"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04:05", "2006-01-02T15:04",
	} {
		if parsed, err := time.ParseInLocation(layout, value, homeworkTimezone); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown homework deadline %q", value)
}

func (m *Manager) finishHomework(hw *Homework, sourceURL string, now time.Time) {
	hw.SourceURL = sourceURL
	if hw.DetailHref == "" {
		hw.DetailHref = strings.TrimRight(m.BaseURL, "/") + "/meol/common/hw/student/hwtask.view.jsp?hwtid=" + url.QueryEscape(hw.HwTID)
	}
	hw.HasResult = hw.ResultHref != ""
	score := strings.TrimSpace(hw.Score)
	completed := hw.HasResult || (score != "" && score != "暂无分数" && score != "未评分" && score != "暂无成绩")
	open, unknown := true, false
	hw.TimeRemaining, hw.IsUrgent = "无截止时间", false
	switch strings.TrimSpace(hw.Deadline) {
	case "", "不限", "无限制", "无截止时间":
	default:
		deadline, err := parseHomeworkDeadline(hw.Deadline)
		if err != nil {
			open, unknown, hw.TimeRemaining = false, true, "时间格式错误"
		} else if diff := deadline.Sub(now); diff <= 0 {
			open, hw.TimeRemaining = false, "已过期"
		} else {
			days, hours, minutes := int(diff.Hours())/24, int(diff.Hours())%24, int(diff.Minutes())%60
			switch {
			case days > 0:
				hw.TimeRemaining = fmt.Sprintf("%d天%d小时%d分钟", days, hours, minutes)
			case hours > 0:
				hw.TimeRemaining = fmt.Sprintf("%d小时%d分钟", hours, minutes)
			default:
				hw.TimeRemaining = fmt.Sprintf("%d分钟", minutes)
			}
			hw.IsUrgent = diff <= 24*time.Hour
		}
	}
	hw.CanSubmit = hw.SubmitHref != "" && !completed && open
	// Urgency describes actionable work only.
	hw.IsUrgent = hw.IsUrgent && hw.CanSubmit
	switch {
	case completed:
		hw.Status = "已完成"
	case unknown:
		hw.Status = "截止时间未知"
	case !open:
		hw.Status = "已截止"
	case hw.CanSubmit:
		hw.Status = "未提交"
	default:
		hw.Status = "不可提交"
	}
}

// fetchDocument 统一的 HTTP 请求和 goquery 文档解析（包含转码逻辑）。
func (m *Manager) fetchDocument(rawURL, referer string) (*goquery.Document, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := m.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", exceptions.ErrNetwork, err)
	}
	utf8Body, err := utils.DecodeBodyToUTF8(resp)
	if err != nil {
		return nil, fmt.Errorf("decoding error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status code %d", exceptions.ErrNetwork, resp.StatusCode)
	}
	return goquery.NewDocumentFromReader(utf8Body)
}
