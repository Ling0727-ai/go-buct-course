BUCT Course 提醒工具（Go 版）

简介

这是一个用 Go 编写的非官方“北京化工大学（BUCT）课程平台”提醒/爬取工具，主要用于自动获取并展示学生在 BUCT 课程平台上的待办任务（作业和测验/考试）信息。该项目原始实现参考了 Python 版本，但这是对其功能的 Go 重构与组织。

主要功能

- 登录 BUCT 课程平台（基于网页表单登录）
- 获取待办作业（包含截止时间、是否可提交、任务描述等）
- 获取可开始/待提交的测验列表（测验标题、时长、开始/结束时间、测试链接）
- 在交互式命令行中显示汇总与详细任务信息
- 支持 GBK -> UTF-8 转码以修复网页中的中文乱码问题

仓库结构（重要文件/包）

- `main.go` — 程序入口（演示如何创建客户端并运行交互式界面）
- `client/` — 客户端逻辑（BUCTClient）：登录、登出、获取任务与交互式界面
  - `client.go` — BUCTClient 类型与核心方法（New, Login, Logout, GetPendingTasks, GetHomeworkTasks）
  - `interactive.go` — 交互式 CLI 展示（登录提示、任务展示）
- `auth/` — 认证相关（表单登录、session 管理，返回经过认证的 http.Client）
- `lid_utils/` — LID 管理（解析课程/测验 ID、从平台主页面提取课程列表与待办 LID）
- `course_utils/` — 作业解析（抓取作业页面、解析作业表格和详细任务）
- `exam/` — 测验解析（抓取测验列表、解析每条测验信息与详情）
- `utils/` — 通用工具（GBK -> UTF-8 转码等）
- `exceptions/` — 常量错误值（ErrLogin、ErrNetwork 等）
- `go.mod` / `go.sum` — 依赖管理（依赖包括 goquery、x/text、x/term）

快速开始

先决条件

- Go 工具链（go 1.20+ 推荐。本仓库 go.mod 标注 go 1.25.5）
- 可联网访问 BUCT 平台（https://course.buct.edu.cn）

运行（开发 / 体验）

在仓库根目录下直接运行（会执行 `main.go` 中创建客户端并调用交互式界面）:

Windows PowerShell:

```powershell
# 运行
go run .

# 或者构建并运行可执行文件
go build -o buct-course .; .\buct-course.exe
```

程序会在控制台请求输入学号与密码（密码以隐式方式输入），登录后会抓取并在终端显示待办作业和可进行的测验列表。

以库形式使用（示例）

如果你想在其它 Go 程序中直接使用客户端：

```go
package xxx
import "github.com/Ling0727-ai/go-buct-course/client"

func main() {
    c := client.New("", "")
    // 可选：使用显式 Login
    if err := c.Login("your_student_id", "your_password"); err != nil {
        // 处理错误
    }

    // 交互式显示
    c.RunInteractive()

    // 或者以编程方式获取任务
    tasks, err := c.GetPendingTasks()
    _ = tasks
    _ = err
}
```

主要 API（概览）

- package client
  - New(username, password string) *BUCTClient — 创建新客户端实例（传入用户名/密码会尝试登录）
  - (*BUCTClient) Login(username, password string) error — 登录并初始化 http.Client、课程/考试管理器
  - (*BUCTClient) Logout() — 注销并清理会话
  - (*BUCTClient) RunInteractive() — 运行交互式终端显示（用于 CLI）
  - (*BUCTClient) GetPendingTasks() (*PendingTaskResult, error) — 获取整合的待办任务（作业 + 测验）
  - (*BUCTClient) GetHomeworkTasks(detailURL string) ([]string, error) — 获取某一作业详情页中的题目/任务说明

- package auth
  - New() *BUCTAuth — 创建认证管理器
  - (*BUCTAuth) Login(username, password string) error
  - (*BUCTAuth) GetClient() (*http.Client, error)
  - (*BUCTAuth) Logout()
  - (*BUCTAuth) SetBaseURL(url string)

- 其他包（lid, course_utils, exam, utils）提供了更底层的抓取与解析函数，供高级调用或扩展使用。

依赖

- github.com/PuerkitoBio/goquery — HTML 解析
- golang.org/x/text — 字符编码转换（GBK -> UTF-8）
- golang.org/x/term — 终端密码输入支持

配置

- BaseURL：若目标平台地址发生变化，可以调用 `auth.BUCTAuth.SetBaseURL` 或手动设置各管理器的 BaseURL 字段以指向新的主机。

注意事项 & 常见问题

- 导入路径：源码内部有使用短路径（例如 `go-buct-course/...`），而 go.mod 的模块名是 `github.com/Ling0727-ai/go-buct-course`。如果在外部模块中引用这些包，建议使用 go.mod 中的模块路径或将本仓库放在 GOPATH（旧方式）中按原始导入路径组织。
- 反爬/频率限制：工具中部分请求带有短暂的 time.Sleep（几百毫秒到 800ms）以减缓抓取速度；如果遇到平台侧的限流或封锁，请适当放宽请求频率或手动处理验证码/验证。
- 登录失败：若频繁登录失败，可能是用户名/密码错误或校园网/门户发生变更。
- 转码问题：项目使用了 GBK -> UTF-8 的转码逻辑（utils.DecodeBodyToUTF8），这是为了解决平台返回 HTML 编码为 GBK 导致的中文乱码问题。

开发与测试

- 代码静态检查/编译
  - go build ./...
- 单元测试：当前仓库未包含单元测试文件（_test.go）。建议为关键解析函数补充单元测试以保证健壮性。

贡献

欢迎提交 issue/PR：
- 修复解析规则（网页结构变更时）
- 增加对作业自动提交/测验自动完成（注意合法合规性：仅在允许的前提下使用）
- 增加单元测试和 CI 流水线

许可证

仓库中未包含 LICENSE 文件。请在将项目公开分发前补充合适的许可证（例如 MIT、Apache-2.0 等）。

致谢

- 感谢 goquery 与 golang.org/x 系列库提供的强大解析与编码支持。

---

文件更新说明

此 README 基于仓库源码自动生成。如果需要我把 README 翻译为英文、补充示例截图、或生成更详尽的 API 文档（如 godoc 风格），告诉我你想要的格式和深度，我会继续完善。
