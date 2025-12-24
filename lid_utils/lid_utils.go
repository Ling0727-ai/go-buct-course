package lid

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/Ling0727-ai/go-buct-course/exceptions"
	"github.com/Ling0727-ai/go-buct-course/utils" // 引入转码工具
)

// CourseInfo 基础课程信息结构体
type CourseInfo struct {
	CourseName string `json:"course_name"`
	LID        string `json:"lid"`
	CourseID   string `json:"course_id"` // 兼容字段
	Type       string `json:"type"`      // 'homework' 或 'test'
	URL        string `json:"url"`
	// 以下字段用于全量课程列表
	Teacher  string `json:"teacher,omitempty"`
	Semester string `json:"semester,omitempty"`
	Status   string `json:"status,omitempty"`
}

// PendingTasks 待办任务集合
type PendingTasks struct {
	Homework []*CourseInfo `json:"homework"`
	Tests    []*CourseInfo `json:"tests"`
}

// Manager LID 工具管理器
type Manager struct {
	Client  *http.Client
	BaseURL string
}

// New 创建一个新的 LID 管理器
func New(client *http.Client) *Manager {
	return &Manager{
		Client:  client,
		BaseURL: "https://course.buct.edu.cn",
	}
}

// GetPendingTasks 获取待办任务列表（作业和测试）
func (m *Manager) GetPendingTasks() (*PendingTasks, error) {
	url := fmt.Sprintf("%s/meol/welcomepage/student/interaction_reminder_v8.jsp", m.BaseURL)

	req, _ := http.NewRequest("GET", url, nil)
	resp, err := m.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", exceptions.ErrNetwork, err)
	}

	// 使用工具函数转码 (GBK -> UTF-8)
	utf8Body, err := utils.DecodeBodyToUTF8(resp)
	if err != nil {
		return nil, fmt.Errorf("%w: decoding error %v", exceptions.ErrParse, err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%w: status code %d", exceptions.ErrNetwork, resp.StatusCode)
	}

	// 传入转换后的 body
	doc, err := goquery.NewDocumentFromReader(utf8Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", exceptions.ErrParse, err)
	}

	result := &PendingTasks{
		Homework: []*CourseInfo{},
		Tests:    []*CourseInfo{},
	}

	// 查找所有 a 标签并筛选 onclick 属性
	doc.Find("a[onclick]").Each(func(i int, s *goquery.Selection) {
		onclick, _ := s.Attr("onclick")

		// 判断是否是作业
		if strings.Contains(onclick, "&t=hw") {
			info := m.extractSingleCourseInfo(s, "homework")
			if info != nil {
				result.Homework = append(result.Homework, info)
			}
		}

		// 判断是否是测试
		if strings.Contains(onclick, "&t=test") {
			info := m.extractSingleCourseInfo(s, "test")
			if info != nil {
				result.Tests = append(result.Tests, info)
			}
		}
	})

	return result, nil
}

// GetHomeworkLids 专门获取待提交作业的课程 LID 列表
func (m *Manager) GetHomeworkLids() ([]*CourseInfo, error) {
	tasks, err := m.GetPendingTasks()
	if err != nil {
		return nil, err
	}
	return tasks.Homework, nil
}

// GetTestLids 专门获取待提交测试的课程 LID 列表
func (m *Manager) GetTestLids() ([]*CourseInfo, error) {
	tasks, err := m.GetPendingTasks()
	if err != nil {
		return nil, err
	}
	return tasks.Tests, nil
}

// GetAllCourseLids 获取所有课程的 LID 列表（从课程主页）
func (m *Manager) GetAllCourseLids() ([]*CourseInfo, error) {
	url := fmt.Sprintf("%s/meol/homepage/student/index.jsp", m.BaseURL)

	req, _ := http.NewRequest("GET", url, nil)
	resp, err := m.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", exceptions.ErrNetwork, err)
	}

	// 转码
	utf8Body, err := utils.DecodeBodyToUTF8(resp)
	if err != nil {
		return nil, fmt.Errorf("%w: decoding error %v", exceptions.ErrParse, err)
	}

	doc, err := goquery.NewDocumentFromReader(utf8Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", exceptions.ErrParse, err)
	}

	var courses []*CourseInfo

	// 查找课程表格 table.valuelist -> tr (跳过第一行表头)
	doc.Find("table.valuelist tr").Each(func(i int, tr *goquery.Selection) {
		if i == 0 {
			return // 跳过表头
		}

		// 第一列包含课程名和链接
		tds := tr.Find("td")
		titleLink := tds.Eq(0).Find("a")

		if titleLink.Length() > 0 {
			info := &CourseInfo{}
			info.CourseName = strings.TrimSpace(titleLink.Text())
			href, _ := titleLink.Attr("href")

			// 提取课程ID
			if strings.Contains(href, "courseId=") {
				parts := strings.Split(href, "courseId=")
				if len(parts) > 1 {
					info.LID = strings.Split(parts[1], "&")[0]
					info.CourseID = info.LID // 兼容填充
				}
				if strings.HasPrefix(href, "/") {
					info.URL = m.BaseURL + href
				} else {
					info.URL = href
				}
			}

			// 获取其他列信息
			if tds.Length() >= 4 {
				info.Teacher = strings.TrimSpace(tds.Eq(1).Text())
				info.Semester = strings.TrimSpace(tds.Eq(2).Text())
				info.Status = strings.TrimSpace(tds.Eq(3).Text())
			}

			if info.LID != "" {
				courses = append(courses, info)
			}
		}
	})

	return courses, nil
}

// FindLIDByCourseName 根据课程名称查找对应的 LID
func (m *Manager) FindLIDByCourseName(courseName string) (string, error) {
	courses, err := m.GetAllCourseLids()
	if err != nil {
		return "", err
	}

	target := strings.ToLower(courseName)
	for _, c := range courses {
		if strings.Contains(strings.ToLower(c.CourseName), target) {
			return c.LID, nil
		}
	}
	return "", nil
}

// extractSingleCourseInfo 内部辅助方法
func (m *Manager) extractSingleCourseInfo(s *goquery.Selection, expectedType string) *CourseInfo {
	courseName := strings.TrimSpace(s.Text())
	onclick, _ := s.Attr("onclick")

	if onclick == "" || !strings.Contains(onclick, "lid=") {
		return nil
	}

	// 提取 LID
	parts := strings.Split(onclick, "lid=")
	if len(parts) < 2 {
		return nil
	}
	lid := strings.Split(parts[1], "&")[0]

	// 过滤汇总信息 (如 "3门课程待提交")
	if strings.Contains(courseName, "门课程") && strings.Contains(courseName, "待提交") {
		return nil
	}

	// 过滤特定测试 LID
	if expectedType == "test" {
		if lid == "24199" || lid == "27215" {
			return nil
		}
	}

	var url string
	if expectedType == "test" {
		url = fmt.Sprintf("%s/meol/common/question/test/student/list.jsp?sortColumn=createTime&status=1&tagbug=client&sortDirection=-1&strStyle=new03&cateId=%s&pagingPage=1&pagingNumberPer=30", m.BaseURL, lid)
	} else {
		url = fmt.Sprintf("%s/meol/jpk/course/layout/newpage/index.jsp?courseId=%s", m.BaseURL, lid)
	}

	return &CourseInfo{
		CourseName: courseName,
		LID:        lid,
		CourseID:   lid,
		Type:       expectedType,
		URL:        url,
	}
}

// GetLIDFromURL 静态辅助函数
func GetLIDFromURL(urlStr string) string {
	if strings.Contains(urlStr, "courseId=") {
		return strings.Split(strings.Split(urlStr, "courseId=")[1], "&")[0]
	} else if strings.Contains(urlStr, "lid=") {
		return strings.Split(strings.Split(urlStr, "lid=")[1], "&")[0]
	}
	return ""
}
