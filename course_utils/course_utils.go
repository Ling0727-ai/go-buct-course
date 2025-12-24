package course_utils

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/Ling0727-ai/go-buct-course/exceptions"
	"github.com/Ling0727-ai/go-buct-course/lid_utils" // 修正包引用路径
	"github.com/Ling0727-ai/go-buct-course/utils"     // 引入转码工具
)

// Homework 作业详情结构体
type Homework struct {
	Title      string `json:"title"`
	DetailHref string `json:"detail_href"`
	HwTID      string `json:"hwtid"`
	IsGroup    bool   `json:"is_group"`
	Deadline   string `json:"deadline"`
	Score      string `json:"score"`
	Publisher  string `json:"publisher"`
	SubmitHref string `json:"submit_href"`
	CanSubmit  bool   `json:"can_submit"`
	ResultHref string `json:"result_href"`
	HasResult  bool   `json:"has_result"`
	Status     string `json:"status"`

	// 时间分析字段
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

// New 创建新的课程管理器
func New(client *http.Client) *Manager {
	return &Manager{
		Client:     client,
		BaseURL:    "https://course.buct.edu.cn",
		LidManager: lid.New(client),
	}
}

// GetPendingHomework 获取待提交作业列表
func (m *Manager) GetPendingHomework() ([]*lid.CourseInfo, error) {
	return m.LidManager.GetHomeworkLids()
}

// GetCourseDetails 获取课程作业列表信息
func (m *Manager) GetCourseDetails(lidStr string) (*CourseDetail, error) {
	// 1. 访问课程主页
	courseMainURL := fmt.Sprintf("%s/meol/jpk/course/layout/newpage/index.jsp?courseId=%s", m.BaseURL, lidStr)

	mainDoc, err := m.fetchDocument(courseMainURL, "")
	if err != nil {
		return nil, fmt.Errorf("fetch course main failed: %w", err)
	}

	// 2. 查找作业相关链接
	var homeworkLink string
	mainDoc.Find("a[href]").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		text := strings.TrimSpace(s.Text())

		// 这里的 "作业" 之前因为是 GBK 乱码无法匹配，转码后即可匹配
		if strings.Contains(href, "course_column_preview_transfer.jsp") && strings.Contains(text, "作业") {
			homeworkLink = href
		}
	})

	if homeworkLink == "" {
		return &CourseDetail{LID: lidStr, HomeworkList: []*Homework{}, TotalCount: 0}, nil
	}

	// 3. 构造完整的作业页面URL
	var homeworkURL string
	if strings.HasPrefix(homeworkLink, "/") {
		homeworkURL = m.BaseURL + homeworkLink
	} else if strings.HasPrefix(homeworkLink, "../../") {
		homeworkURL = fmt.Sprintf("%s/meol/jpk/course/layout/newpage/%s", m.BaseURL, homeworkLink)
	} else {
		homeworkURL = homeworkLink
	}

	// 4. 访问作业页面
	time.Sleep(500 * time.Millisecond)
	hwDoc, err := m.fetchDocument(homeworkURL, courseMainURL)
	if err != nil {
		return nil, fmt.Errorf("fetch homework page failed: %w", err)
	}

	return m.parseHomeworkTable(hwDoc, lidStr), nil
}

// GetHomeworkDetail 获取单个作业详情
func (m *Manager) GetHomeworkDetail(hwtid string) (*HomeworkTaskDetail, error) {
	detailURL := fmt.Sprintf("%s/meol/common/hw/student/hwtask.view.jsp?hwtid=%s", m.BaseURL, hwtid)
	referer := fmt.Sprintf("%s/meol/common/hw/student/hwtask.jsp?tagbug=client&strStyle=new03", m.BaseURL)

	time.Sleep(300 * time.Millisecond)
	doc, err := m.fetchDocument(detailURL, referer)
	if err != nil {
		return nil, err
	}

	detail := &HomeworkTaskDetail{
		HwTID: hwtid,
	}

	if title := doc.Find("h1, h2, h3").First(); title.Length() > 0 {
		detail.Title = strings.TrimSpace(title.Text())
	}

	if content := doc.Find("div.content, div.description").First(); content.Length() > 0 {
		detail.Description = strings.TrimSpace(content.Text())
	}

	return detail, nil
}

// GetHomeworkTasks 获取作业详情页面中的具体题目要求
func (m *Manager) GetHomeworkTasks(detailURL string) ([]string, error) {
	fullURL := detailURL
	if strings.HasPrefix(detailURL, "/") {
		fullURL = m.BaseURL + detailURL
	}

	referer := fmt.Sprintf("%s/meol/common/hw/student/hwtask.jsp?tagbug=client&strStyle=new03", m.BaseURL)

	time.Sleep(300 * time.Millisecond)
	doc, err := m.fetchDocument(fullURL, referer)
	if err != nil {
		return nil, err
	}

	var tasks []string

	// 1. 查找隐藏的 input 字段
	doc.Find("input[type='hidden']").Each(func(i int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		if strings.Contains(name, "content") {
			value, _ := s.Attr("value")
			if value != "" {
				decodedHTML := html.UnescapeString(value)
				contentDoc, err := goquery.NewDocumentFromReader(strings.NewReader(decodedHTML))
				if err == nil {
					pTags := contentDoc.Find("p")
					if pTags.Length() > 0 {
						pTags.Each(func(k int, p *goquery.Selection) {
							text := strings.TrimSpace(p.Text())
							if text != "" {
								tasks = append(tasks, text)
							}
						})
					} else {
						text := strings.TrimSpace(contentDoc.Text())
						if text != "" {
							tasks = append(tasks, text)
						}
					}
				}
			}
		}
	})

	// 2. 备用方案
	if len(tasks) == 0 {
		doc.Find("div#body p").Each(func(i int, s *goquery.Selection) {
			text := strings.TrimSpace(s.Text())
			if text != "" {
				tasks = append(tasks, text)
			}
		})
	}

	return tasks, nil
}

// GetAllPendingHomeworkDetails 获取所有待提交作业的详细信息
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

		urgentCount := 0
		for _, hw := range details.HomeworkList {
			if hw.IsUrgent {
				urgentCount++
			}
		}
		details.UrgentCount = urgentCount

		allDetails = append(allDetails, details)
		time.Sleep(800 * time.Millisecond)
	}

	return allDetails, nil
}

// =================================================================================
// 内部私有方法 (Helpers)
// =================================================================================

// fetchDocument 统一的 HTTP 请求和 goquery 文档解析（包含转码逻辑）
func (m *Manager) fetchDocument(url, referer string) (*goquery.Document, error) {
	req, err := http.NewRequest("GET", url, nil)
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

	// --- 转码修复核心 ---
	// 使用工具函数转码 (GBK -> UTF-8)
	utf8Body, err := utils.DecodeBodyToUTF8(resp)
	if err != nil {
		return nil, fmt.Errorf("decoding error: %v", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%w: status code %d", exceptions.ErrNetwork, resp.StatusCode)
	}

	return goquery.NewDocumentFromReader(utf8Body)
}

// parseHomeworkTable 解析作业表格
func (m *Manager) parseHomeworkTable(doc *goquery.Document, lid string) *CourseDetail {
	var list []*Homework

	doc.Find("table.valuelist tr").Each(func(i int, s *goquery.Selection) {
		if i == 0 {
			return // 跳过表头
		}
		hw := m.parseHomeworkRow(s)
		if hw != nil {
			list = append(list, hw)
		}
	})

	return &CourseDetail{
		LID:          lid,
		HomeworkList: list,
		TotalCount:   len(list),
	}
}

// parseHomeworkRow 解析单行作业信息
func (m *Manager) parseHomeworkRow(row *goquery.Selection) *Homework {
	cells := row.Find("td")
	if cells.Length() < 8 {
		return nil
	}

	hw := &Homework{}

	// 1. 标题和链接
	titleCell := cells.Eq(0)
	titleLink := titleCell.Find("a.infolist")

	if titleLink.Length() > 0 {
		hw.Title = strings.TrimSpace(titleLink.Text())
		detailHref, _ := titleLink.Attr("href")

		if strings.HasPrefix(detailHref, "/") {
			hw.DetailHref = m.BaseURL + detailHref
		} else if strings.HasPrefix(detailHref, "../../") {
			hw.DetailHref = fmt.Sprintf("%s/meol/common/hw/student/%s", m.BaseURL, detailHref)
		} else if strings.HasPrefix(detailHref, "hwtask.view.jsp") {
			hw.DetailHref = fmt.Sprintf("%s/meol/common/hw/student/%s", m.BaseURL, detailHref)
		} else {
			hw.DetailHref = detailHref
		}

		if strings.Contains(detailHref, "hwtid=") {
			parts := strings.Split(detailHref, "hwtid=")
			if len(parts) > 1 {
				hw.HwTID = strings.Split(parts[1], "&")[0]
			}
		}
	}

	hw.IsGroup = titleCell.Find("img[title='分组作业']").Length() > 0

	// 2. 基本信息
	hw.Deadline = strings.TrimSpace(cells.Eq(1).Text())
	hw.Score = strings.TrimSpace(cells.Eq(2).Text())
	hw.Publisher = strings.TrimSpace(cells.Eq(3).Text())

	// 3. 提交链接
	submitLink := cells.Eq(5).Find("a.enter")
	hw.SubmitHref, _ = submitLink.Attr("href")
	hasSubmitLink := hw.SubmitHref != ""

	// 4. 结果链接
	resultLink := cells.Eq(6).Find("a.view")
	hw.ResultHref, _ = resultLink.Attr("href")
	hw.HasResult = hw.ResultHref != ""

	if !hw.HasResult && strings.Contains(cells.Eq(6).Text(), "未提交") {
		hw.Status = "未提交"
	}

	if hw.HwTID != "" {
		hw.DetailHref = fmt.Sprintf("%s/meol/common/hw/student/hwtask.view.jsp?hwtid=%s", m.BaseURL, hw.HwTID)
	} else {
		hw.DetailHref = ""
	}

	// 5. 时间计算
	isNotExpired := true
	hw.TimeRemaining = "无截止时间"
	hw.IsUrgent = false

	if hw.Deadline != "" {
		layout := "2006年01月02日 15:04:05"
		deadlineTime, err := time.ParseInLocation(layout, hw.Deadline, time.Local)

		if err == nil {
			now := time.Now()
			isNotExpired = deadlineTime.After(now)
			diff := deadlineTime.Sub(now)

			if diff > 0 {
				days := int(diff.Hours()) / 24
				hours := int(diff.Hours()) % 24
				minutes := int(diff.Minutes()) % 60

				if days > 0 {
					hw.TimeRemaining = fmt.Sprintf("%d天%d小时%d分钟", days, hours, minutes)
				} else if hours > 0 {
					hw.TimeRemaining = fmt.Sprintf("%d小时%d分钟", hours, minutes)
				} else {
					hw.TimeRemaining = fmt.Sprintf("%d分钟", minutes)
				}

				if diff.Hours() <= 24 {
					hw.IsUrgent = true
				}
			} else {
				hw.TimeRemaining = "已过期"
				isNotExpired = false
			}
		} else {
			hw.TimeRemaining = "时间格式错误"
		}
	}

	isNotCompleted := (hw.Score == "" || strings.TrimSpace(hw.Score) == "")

	hw.CanSubmit = hasSubmitLink && isNotExpired && isNotCompleted

	return hw
}
