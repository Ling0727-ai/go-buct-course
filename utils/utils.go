package utils

import (
	"bytes"
	"io"
	"net/http"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// DecodeBodyToUTF8 将 HTTP 响应体从 GBK 转码为 UTF-8
// 解决了北化课程平台中文乱码导致无法识别"作业"、"测试"等关键字的问题
func DecodeBodyToUTF8(resp *http.Response) (io.Reader, error) {
	// 1. 读取原始 Body (GBK 编码)
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// 必须关闭原始 Body，防止资源泄露
	resp.Body.Close()

	// 2. 创建解码器 (GBK -> UTF-8)
	// 使用 simplifiedchinese.GBK
	reader := transform.NewReader(bytes.NewReader(bodyBytes), simplifiedchinese.GBK.NewDecoder())

	// 3. 读取解码后的内容
	decodedBytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	// 4. 返回新的 Reader 供 goquery 使用
	return bytes.NewReader(decodedBytes), nil
}
