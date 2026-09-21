package course_utils

import (
	"fmt"
	"os"
	"testing"

	"github.com/Ling0727-ai/go-buct-course/auth"
)

// TestGetHomeworkDetailSpecial1 测试 getHomeworkDetailSpecial1 和 GetCourseDetails 的回退逻辑
// 使用环境变量 BUCT_USERNAME 和 BUCT_PASSWORD 传入凭据
// 用法: go test -v -run TestGetHomeworkDetailSpecial1 ./course_utils/
// 设置环境变量:
//
//	set BUCT_USERNAME=your_username
//	set BUCT_PASSWORD=your_password
func TestGetHomeworkDetailSpecial1(t *testing.T) {
	username := os.Getenv("BUCT_USERNAME")
	password := os.Getenv("BUCT_PASSWORD")
	courseID := os.Getenv("BUCT_COURSE_ID")
	if courseID == "" {
		courseID = "39383"
	}

	if username == "" || password == "" {
		t.Skip("跳过: 未设置环境变量 BUCT_USERNAME 和 BUCT_PASSWORD")
	}

	// 1. 登录
	a := auth.New()
	err := a.Login(username, password)
	if err != nil {
		t.Fatalf("❌ 登录失败: %v", err)
	}
	t.Log("✅ 登录成功！")

	// 2. 获取 http.Client 并创建 Manager
	httpClient, err := a.GetClient()
	if err != nil {
		t.Fatalf("❌ 获取 http client 失败: %v", err)
	}
	mgr := New(httpClient)

	t.Logf("⏳ 正在获取课程作业信息 (courseId: %s)...", courseID)

	// 3. 直接测试 getHomeworkDetailSpecial1
	t.Run("直接调用 getHomeworkDetailSpecial1", func(t *testing.T) {
		results, err := mgr.getHomeworkDetailSpecial1(courseID)
		if err != nil {
			t.Fatalf("getHomeworkDetailSpecial1 执行失败: %v", err)
		}
		t.Logf("找到子页面作业结果共: %d 个", len(results))
		for i, hw := range results {
			t.Logf("  %d. 标题: %s | hwtid: %s", i+1, hw.Title, hw.HwTID)
		}
	})

	// 4. 测试 GetCourseDetails 的完整流程（含 special1 回退）
	t.Run("经由 GetCourseDetails 检查完整流程", func(t *testing.T) {
		detail, err := mgr.GetCourseDetails(courseID)
		if err != nil {
			t.Fatalf("GetCourseDetails 执行失败: %v", err)
		}
		t.Logf("作业总数: %d", detail.TotalCount)
		for i, hw := range detail.HomeworkList {
			t.Logf("  %d. 标题: %s | 截止时间: %s | HWTID: %s", i+1, hw.Title, hw.Deadline, hw.HwTID)
		}
	})

	// 5. 测试 GetAllPendingHomeworkDetails 完整流程
	t.Run("GetAllPendingHomeworkDetails 完整流程", func(t *testing.T) {
		allDetails, err := mgr.GetAllPendingHomeworkDetails()
		if err != nil {
			t.Fatalf("GetAllPendingHomeworkDetails 执行失败: %v", err)
		}
		t.Logf("共找到 %d 个有作业的课程", len(allDetails))
		for _, cd := range allDetails {
			t.Logf("  课程: %s | 作业数: %d | 紧急: %d", cd.CourseName, cd.TotalCount, cd.UrgentCount)
			for _, hw := range cd.HomeworkList {
				t.Logf("    - 标题: %s | 截止: %s | HWTID: %s | 可提交: %v",
					hw.Title, hw.Deadline, hw.HwTID, hw.CanSubmit)
			}
		}
	})
}

// Example 输出说明
func Example() {
	fmt.Println("请通过环境变量设置凭据:")
	fmt.Println("  set BUCT_USERNAME=your_username")
	fmt.Println("  set BUCT_PASSWORD=your_password")
	fmt.Println("然后运行: go test -v -run TestGetHomeworkDetailSpecial1 ./course_utils/")
}
