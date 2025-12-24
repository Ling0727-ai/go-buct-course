package client

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

// RunInteractive 运行交互式客户端 (对应 Python 的 run_interactive)
func (c *BUCTClient) RunInteractive() {
	c.displayWelcome()

	// 1. 处理登录逻辑
	if !c.Auth.IsLoggedIn() {
		reader := bufio.NewReader(os.Stdin)

		// 简单的重试逻辑
		maxAttempts := 3
		for i := 0; i < maxAttempts; i++ {
			fmt.Print("请输入学号: ")
			username, _ := reader.ReadString('\n')
			username = strings.TrimSpace(username)

			fmt.Print("请输入密码: ")
			bytePassword, _ := term.ReadPassword(int(syscall.Stdin))
			password := string(bytePassword)
			fmt.Println() // 换行

			fmt.Println("正在登录...")
			err := c.Login(username, password)
			if err == nil {
				fmt.Println("✅ 登录成功!")
				break
			} else {
				fmt.Printf("❌ 登录失败: %v (剩余尝试次数: %d)\n", err, maxAttempts-i-1)
				if i == maxAttempts-1 {
					fmt.Println("尝试次数过多，程序退出。")
					return
				}
			}
		}
	}

	// 2. 获取并显示任务
	c.displayTasks()

	fmt.Println("\n🎉 任务完成!")
	fmt.Printf("完成时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
}

// displayWelcome 显示欢迎语
func (c *BUCTClient) displayWelcome() {
	fmt.Println("== 北化课程提醒系统 (Go重构版) ==")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("启动时间: %s\n\n", time.Now().Format("2006-01-02 15:04:05"))
}

// displayTasks 获取并显示任务的核心逻辑
func (c *BUCTClient) displayTasks() {
	fmt.Println("正在获取待办任务 (可能需要几秒钟)...")
	tasks, err := c.GetPendingTasks()
	if err != nil {
		fmt.Printf("❌ 获取任务失败: %v\n", err)
		return
	}

	hwCount := len(tasks.Homework)
	testCount := len(tasks.Tests)

	// 显示统计
	fmt.Println("\n📊 待办任务统计:")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("📝 可提交作业: %d\n", hwCount)
	fmt.Printf("🧪 可进行测试: %d\n", testCount)
	fmt.Printf("📈 总计: %d\n", hwCount+testCount)
	fmt.Println(strings.Repeat("-", 40))

	// 显示作业
	if hwCount > 0 {
		fmt.Println("\n📋 详细作业要求:")
		fmt.Println(strings.Repeat("=", 60))

		for i, hw := range tasks.Homework {
			fmt.Printf("\n📝 作业 %d: %s\n", i+1, hw.Title)

			// 查找课程名 (Go里做简单的关联查找)
			courseName := "未知课程"
			for _, raw := range tasks.RawHomeworkCourses {
				for _, h := range raw.HomeworkList {
					if h.HwTID == hw.HwTID {
						courseName = raw.CourseName
						goto FoundCourse // 跳出双层循环
					}
				}
			}
		FoundCourse:

			fmt.Printf("📚 课程: %s\n", courseName)
			fmt.Printf("⏰ 截止: %s\n", hw.Deadline)
			//fmt.Printf("👥 分组: %v\n", hw.IsGroup)
			fmt.Printf("⏳ 剩余: %s\n", hw.TimeRemaining)

			// 获取具体题目内容
			if hw.DetailHref != "" {
				fmt.Print("⏳ 正在获取题目内容...")
				tasksDesc, err := c.GetHomeworkTasks(hw.DetailHref)
				fmt.Print("\r" + strings.Repeat(" ", 30) + "\r") // 清除提示行

				if err != nil {
					fmt.Printf("⚠️  获取题目失败: %v\n", err)
				} else if len(tasksDesc) > 0 {
					fmt.Printf("📋 作业要求 (%d 项):\n", len(tasksDesc))
					for _, t := range tasksDesc {
						// 简单的截断显示，防止刷屏
						displayT := strings.ReplaceAll(t, "\n", " ")
						if len([]rune(displayT)) > 100 {
							displayT = string([]rune(displayT)[:97]) + "..."
						}
						fmt.Printf(" - %s\n", displayT)
					}
				} else {
					fmt.Println("⚠️  暂无详细要求描述")
				}
			}
			fmt.Println(strings.Repeat("-", 50))
		}
	} else {
		fmt.Println("\n✅ 暂无待提交作业")
	}

	// 显示测试
	if testCount > 0 {
		fmt.Println("\n🧪 可进行测试详情:")
		fmt.Println(strings.Repeat("=", 60))
		for i, t := range tasks.Tests {
			// 查找课程名
			cName := "未知"
			for _, raw := range tasks.RawTestCourses {
				for _, rt := range raw.List {
					if rt.TestID == t.TestID {
						cName = raw.CourseName
						break
					}
				}
			}

			fmt.Printf("\n🧪 测试 %d: %s\n", i+1, t.Title)
			fmt.Printf("📚 课程: %s\n", cName)
			fmt.Printf("⏰ 截止: %s\n", t.EndTime)
			fmt.Printf("⏳ 时长: %s\n", t.Duration)
			fmt.Printf("🔗 链接: %s\n", t.TestURL)
			fmt.Println(strings.Repeat("-", 50))
		}
	} else {
		fmt.Println("\n✅ 暂无待提交测试")
	}
}
