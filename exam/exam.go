package exam

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/Ling0727-ai/go-buct-course/exceptions"
	"github.com/Ling0727-ai/go-buct-course/lid_utils" // 修正包引用路径
	"github.com/Ling0727-ai/go-buct-course/utils"     // 引入转码工具
)

// TestInfo 单个测试详情结构体
type TestInfo struct {
	Title           string `json:"title"`
	StartTime       string `json:"start_time"`
	EndTime         string `json:"end_time"`
	AllowedAttempts string `json:"allowed_attempts"`
	Duration        string `json:"duration"`
	TestID          string `json:"test_id"`
	CanStart        bool   `json:"can_start"`
	StartHref       string `json:"start_href"`
	SubmitStatus    string `json:"submit_status"`
	ResultHref      string `json:"result_href"`
	HasResult       bool   `json:"has_result"`
	Status          string `json:"status"`

	// 详情页额外信息
	Description   string `json:"description,omitempty"`
	TotalScore    string `json:"total_score,omitempty"`
	QuestionCount string `json:"question_count,omitempty"`
	TestURL       string `json:"test_url,omitempty"`
}

// TestList 课程测试列表结构体
type TestList struct {
	CourseName string      `json:"course_name"`
	LID        string      `json:"lid"`
	List       []*TestInfo `json:"test_list"`
	TotalCount int         `json:"total_count"`
}

// Manager 测试工具管理器
type Manager struct {
	Client     *http.Client
	BaseURL    string
	LidManager *lid.Manager
}

// New 创建新的测试管理器
func New(client *http.Client) *Manager {
	return &Manager{
		Client:     client,
		BaseURL:    "https://course.buct.edu.cn",
		LidManager: lid.New(client),
	}
}

// GetPendingTests 获取待提交测试列表
func (m *Manager) GetPendingTests() ([]*lid.CourseInfo, error) {
	return m.LidManager.GetTestLids()
}

// GetTestList 获取指定课程的测试列表
func (m *Manager) GetTestList(lidStr string) (*TestList, error) {
	testURL := fmt.Sprintf(
		"%s/meol/common/question/test/student/list.jsp?"+
			"sortColumn=createTime&status=1&tagbug=client&"+
			"sortDirection=-1&strStyle=lesson19&cateId=%s&"+
			"pagingPage=1&pagingNumberPer=7",
		m.BaseURL, lidStr,
	)

	req, _ := http.NewRequest("GET", testURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")
	req.Header.Set("Referer", fmt.Sprintf("%s/meol/jpk/course/layout/newpage/index.jsp?courseId=%s", m.BaseURL, lidStr))
	req.Header.Set("Origin", m.BaseURL)

	resp, err := m.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", exceptions.ErrNetwork, err)
	}

	// --- 转码修复 ---
	utf8Body, err := utils.DecodeBodyToUTF8(resp)
	if err != nil {
		return nil, fmt.Errorf("decoding error: %v", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%w: status code %d", exceptions.ErrNetwork, resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(utf8Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", exceptions.ErrParse, err)
	}

	return m.parseTestTable(doc, lidStr), nil
}

// GetTestDetail 获取单个测试的详细信息
func (m *Manager) GetTestDetail(testID string) (*TestInfo, error) {
	detailURL := fmt.Sprintf("%s/meol/common/question/test/student/view.jsp?testId=%s", m.BaseURL, testID)

	req, _ := http.NewRequest("GET", detailURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0...")
	req.Header.Set("Referer", fmt.Sprintf("%s/meol/common/question/test/student/list.jsp", m.BaseURL))

	resp, err := m.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", exceptions.ErrNetwork, err)
	}

	// --- 转码修复 ---
	utf8Body, err := utils.DecodeBodyToUTF8(resp)
	if err != nil {
		return nil, fmt.Errorf("decoding error: %v", err)
	}

	doc, err := goquery.NewDocumentFromReader(utf8Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", exceptions.ErrParse, err)
	}

	return m.parseTestDetail(doc, testID), nil
}

// =================================================================================
// 内部解析逻辑
// =================================================================================

func (m *Manager) parseTestTable(doc *goquery.Document, lidStr string) *TestList {
	list := []*TestInfo{}
	courseName := "未知课程"

	// 提取课程名称
	titleText := strings.TrimSpace(doc.Find("title").Text())
	if strings.Contains(titleText, "测试") {
		courseName = strings.TrimSpace(strings.Replace(titleText, "测试", "", -1))
	}

	// 查找表格
	table := doc.Find("table.valuelist")
	if table.Length() == 0 {
		table = doc.Find("table[border='0'][cellspacing='0'][cellpadding='0']")
	}

	table.Find("tr").Each(func(i int, tr *goquery.Selection) {
		if i == 0 {
			return // 跳过表头
		}
		info := m.parseTestRow(tr)
		if info != nil {
			list = append(list, info)
		}
	})

	return &TestList{
		CourseName: courseName,
		LID:        lidStr,
		List:       list,
		TotalCount: len(list),
	}
}

func (m *Manager) parseTestRow(tr *goquery.Selection) *TestInfo {
	cells := tr.Find("td")
	if cells.Length() < 8 {
		return nil
	}

	info := &TestInfo{}

	// 1. 标题
	info.Title = strings.TrimSpace(cells.Eq(0).Text())

	// 2. 基础信息
	info.StartTime = strings.TrimSpace(cells.Eq(1).Text())
	info.EndTime = strings.TrimSpace(cells.Eq(2).Text())
	info.AllowedAttempts = strings.TrimSpace(cells.Eq(3).Text())
	info.Duration = strings.TrimSpace(cells.Eq(4).Text())

	// 3. 开始测试逻辑 (正则提取)
	startLink := cells.Eq(5).Find("a")
	onclick, _ := startLink.Attr("onclick")

	if onclick != "" && strings.Contains(onclick, "gotostart(") {
		// 正则提取: gotostart('128089186'
		re := regexp.MustCompile(`gotostart\('(\d+)'`)
		matches := re.FindStringSubmatch(onclick)
		if len(matches) > 1 {
			info.TestID = matches[1]
			info.CanStart = true
			info.StartHref = fmt.Sprintf("#start_test_%s", info.TestID)
			info.TestURL = fmt.Sprintf("%s/meol/common/question/test/student/test_start.jsp?testId=%s", m.BaseURL, info.TestID)
		}
	} else {
		info.CanStart = false
	}

	// 4. 交卷状态
	submitText := strings.TrimSpace(cells.Eq(6).Text())
	if submitText != "" && submitText != "&nbsp;" { // goquery 会自动处理 &nbsp; 为 \u00a0
		info.SubmitStatus = submitText
	}

	// 5. 查看结果
	resultLink := cells.Eq(7).Find("a")
	resultHref, _ := resultLink.Attr("href")
	if resultHref != "" {
		info.ResultHref = resultHref
		info.HasResult = true
		info.Status = "已完成"
	} else {
		info.HasResult = false
		if info.CanStart {
			info.Status = "可进行"
		} else {
			info.Status = "未开始"
		}
	}

	return info
}

func (m *Manager) parseTestDetail(doc *goquery.Document, testID string) *TestInfo {
	info := &TestInfo{
		TestID:  testID,
		TestURL: fmt.Sprintf("%s/meol/common/question/test/student/test_start.jsp?testId=%s", m.BaseURL, testID),
	}

	// 标题
	if t := doc.Find("h1, h2, h3").First(); t.Length() > 0 {
		info.Title = strings.TrimSpace(t.Text())
	}

	// 描述
	if d := doc.Find("div.content, div.description").First(); d.Length() > 0 {
		info.Description = strings.TrimSpace(d.Text())
	}

	// 表格信息
	doc.Find("table.info tr").Each(func(i int, tr *goquery.Selection) {
		cells := tr.Find("td, th")
		if cells.Length() >= 2 {
			key := strings.TrimSpace(cells.Eq(0).Text())
			value := strings.TrimSpace(cells.Eq(1).Text())

			// 只要转码正确，这里的 "开始时间" 等中文关键字就能正确匹配
			if strings.Contains(key, "开始时间") {
				info.StartTime = value
			} else if strings.Contains(key, "结束时间") {
				info.EndTime = value
			} else if strings.Contains(key, "持续时间") || strings.Contains(key, "考试时长") {
				info.Duration = value
			} else if strings.Contains(key, "总分") {
				info.TotalScore = value
			} else if strings.Contains(key, "题目数") || strings.Contains(key, "问题数") {
				info.QuestionCount = value
			}
		}
	})

	return info
}

// FilterAvailableTests 只保留可以进行的测试 (辅助函数)
func FilterAvailableTests(list []*TestInfo) []*TestInfo {
	var available []*TestInfo
	for _, t := range list {
		if t.CanStart {
			available = append(available, t)
		}
	}
	return available
}
