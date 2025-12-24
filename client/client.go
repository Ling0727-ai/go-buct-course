package client

import (
	"fmt"
	"github.com/Ling0727-ai/go-buct-course/auth"
	"github.com/Ling0727-ai/go-buct-course/course_utils"
	"github.com/Ling0727-ai/go-buct-course/exam"
	"github.com/Ling0727-ai/go-buct-course/exceptions"
	"net/http"
)

// BUCTClient 北化课程平台客户端
type BUCTClient struct {
	Auth       *auth.BUCTAuth
	HTTPClient *http.Client
	CourseMgr  *course_utils.Manager
	ExamMgr    *exam.Manager
}

// New 创建一个新的客户端实例
func New(username, password string) *BUCTClient {
	c := &BUCTClient{
		Auth: auth.New(),
	}

	// 初始化时如果提供了用户名密码，尝试自动登录
	// 但通常建议显式调用 Login 方法以处理错误
	if username != "" && password != "" {
		_ = c.Login(username, password)
	}

	return c
}

// Login 登录
func (c *BUCTClient) Login(username, password string) error {
	err := c.Auth.Login(username, password)
	if err != nil {
		return err
	}

	// 登录成功后，获取 http.Client 并初始化子管理器
	httpClient, err := c.Auth.GetClient()
	if err != nil {
		return err
	}
	c.HTTPClient = httpClient
	c.CourseMgr = course_utils.New(httpClient)
	c.ExamMgr = exam.New(httpClient)

	return nil
}

// Logout 注销
func (c *BUCTClient) Logout() {
	if c.Auth != nil {
		c.Auth.Logout()
	}
	c.HTTPClient = nil
	c.CourseMgr = nil
	c.ExamMgr = nil
}

// PendingTaskResult 待办任务聚合结果
type PendingTaskResult struct {
	Homework []*course_utils.Homework `json:"homework"` // 这里为了方便，展平成具体的作业列表
	Tests    []*exam.TestInfo         `json:"tests"`    // 展平后的测试列表

	// 为了兼容原 Python 逻辑，这里保留原始的课程维度信息可能更好
	// 但 Go 倾向于强类型，我们先按业务需求聚合
	RawHomeworkCourses []*course_utils.CourseDetail `json:"raw_homework_courses"`
	RawTestCourses     []*exam.TestList             `json:"raw_test_courses"`
}

// GetPendingTasks 获取所有待办任务 (整合 Homework 和 Tests)
func (c *BUCTClient) GetPendingTasks() (*PendingTaskResult, error) {
	if c.HTTPClient == nil {
		return nil, exceptions.ErrLogin
	}

	result := &PendingTaskResult{
		Homework:           []*course_utils.Homework{},
		Tests:              []*exam.TestInfo{},
		RawHomeworkCourses: []*course_utils.CourseDetail{},
		RawTestCourses:     []*exam.TestList{},
	}

	// 1. 获取作业
	// CourseMgr.GetAllPendingHomeworkDetails 已经处理了 LID 获取 -> 详情获取 -> 过滤 的全过程
	hwDetails, err := c.CourseMgr.GetAllPendingHomeworkDetails()
	if err != nil {
		fmt.Printf("Warning: Failed to get homework details: %v\n", err)
	} else {
		result.RawHomeworkCourses = hwDetails
		// 展平所有可提交作业
		for _, courseDetail := range hwDetails {
			for _, hw := range courseDetail.HomeworkList {
				if hw.CanSubmit {
					// 注入课程名，方便后续展示
					// 注意：Go 的 struct 是值传递，这里最好定义一个新的 struct 或者在 Homework 里加 CourseName 字段
					// 为了简单，我们假设 Homework 结构体足够展示，或者前端/Main 负责拼接
					result.Homework = append(result.Homework, hw)
				}
			}
		}
	}

	// 2. 获取测试
	// 首先获取所有有测试的课程 LID
	testLids, err := c.ExamMgr.GetPendingTests()
	if err != nil {
		fmt.Printf("Warning: Failed to get pending test LIDs: %v\n", err)
	} else {
		for _, tLid := range testLids {
			// 获取该课程的所有测试详情
			testList, err := c.ExamMgr.GetTestList(tLid.LID)
			if err != nil {
				fmt.Printf("Warning: Failed to get tests for course %s: %v\n", tLid.CourseName, err)
				continue
			}
			result.RawTestCourses = append(result.RawTestCourses, testList)

			// 过滤可进行的测试
			available := exam.FilterAvailableTests(testList.List)
			result.Tests = append(result.Tests, available...)
		}
	}

	return result, nil
}

// GetHomeworkTasks 获取单个作业的具体任务描述
func (c *BUCTClient) GetHomeworkTasks(detailURL string) ([]string, error) {
	if c.CourseMgr == nil {
		return nil, exceptions.ErrLogin
	}
	return c.CourseMgr.GetHomeworkTasks(detailURL)
}
