package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

// 以下向量由 Python 参考实现 buct_course/sm2_crypto.py 生成
// （固定私钥 + 固定 k），用于保证 Go 实现的线格式与站点完全一致：
//
//	base64( C1.x || C1.y || C3 || C2 )   // C1 为 64 字节，不带 04 前缀
const (
	pythonVectorPrivateKey = "3945208f7b2144b13f36e38ac6d39f95889393692860b51a42fb81ef4df7c5b8"
	pythonVectorPublicKey  = "BAn53zEeVCGhUN19Fh5LxcZyF5+tGDP8B2uwj/NW81AgzOpJDOJndaUtxupxjMGqYArtBfvzXghKZjL2By2prRM="
	pythonVectorK          = "6cb28d99385c175c94f94e934817663fc176d925dd72b727260dbaae1fb2f96f"
)

var pythonVectors = []struct {
	plaintext string
	cipher    string
}{
	{
		"PASS123",
		"9qaHq1dE1cu6HPk9hDZBb3XDrsPXYoFNVlMUr/V6ifnxuO4FQXQFZUkeRAQ95Tz1u+3WEzMHEmDfxXg/R6e5gcFucen0Jkme3RB+CcePY/tewPC/vxZjM7vQ+C7FDl8iMeW7yeCqZw==",
	},
	{
		"P@ssw0rd!2024",
		"9qaHq1dE1cu6HPk9hDZBb3XDrsPXYoFNVlMUr/V6ifnxuO4FQXQFZUkeRAQ95Tz1u+3WEzMHEmDfxXg/R6e5gU3AohL4sN8jylL/wP4bjvXVdM1ZEwjNJmX+wRSEc3AAMeSb6aaoJjdmSY/Xlw==",
	},
	{
		"长口令ＡＢＣ123！",
		"9qaHq1dE1cu6HPk9hDZBb3XDrsPXYoFNVlMUr/V6ifnxuO4FQXQFZUkeRAQ95Tz1u+3WEzMHEmDfxXg/R6e5gY+FYIP3k4Yd7bQ+NnJOZcfyF1HRYCpA4JCtQWvLn/7niDFXf147sOjjlANETN0lBRQ2/vpQG2Su",
	},
	{
		strings.Repeat("x", 40),
		"9qaHq1dE1cu6HPk9hDZBb3XDrsPXYoFNVlMUr/V6ifnxuO4FQXQFZUkeRAQ95Tz1u+3WEzMHEmDfxXg/R6e5gWUj68t4Q097sa0Pfa+t8QaxYYdNetgQR+rtSC7/aA+aGdyQ4qngLCs/A8ed2xn/ktDtt7AbjKBXgeUaOUKauh7rEi29HLQ7sQ==",
	},
}

// TestSM3KnownVectors 校验 SM3 标准测试向量（GM/T 0004-2012）。
func TestSM3KnownVectors(t *testing.T) {
	cases := map[string]string{
		"abc": "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0",
		"abcd" + "abcd" + "abcd" + "abcd" + "abcd" + "abcd" + "abcd" + "abcd" +
			"abcd" + "abcd" + "abcd" + "abcd" + "abcd" + "abcd" + "abcd" + "abcd": "debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732",
		"": "1ab21d8355cfa17f8e61194831e81a8f22bec8c728fefb747ed035eb5082aa2b",
	}
	for input, want := range cases {
		got := hex.EncodeToString(sm3Sum([]byte(input)))
		if got != want {
			t.Errorf("SM3(%q) = %s, 期望 %s", input, got, want)
		}
	}
}

// TestSM2DecryptPythonCipher 用 Go 解密 Python 参考实现生成的密文。
func TestSM2DecryptPythonCipher(t *testing.T) {
	for _, vector := range pythonVectors {
		plaintext, err := sm2Decrypt(vector.cipher, pythonVectorPrivateKey)
		if err != nil {
			t.Fatalf("解密 %q 失败: %v", vector.plaintext, err)
		}
		if string(plaintext) != vector.plaintext {
			t.Errorf("解密结果 = %q, 期望 %q", plaintext, vector.plaintext)
		}
	}
}

// 站点 JS（sm2.min.js / gm-crypto）在 portal.buct.edu.cn 页面上生成的密钥对与密文，
// 与 Python 参考实现 test/test_sm2_compat.py 使用同一组一次性向量，用于锁定线格式。
const (
	sitePrivateKeyB64 = "ZSzGiEb9WSRzJND0L+yYUGUKvT61TriS1Uwsacozsuo="
	sitePublicKeyB64  = "BLHjNaPKrcmyY72zWdLNXmX6oLzmWbRGUk5O1nr6412xlJQp1xLmmQ1rXd52uVhg6loDIno6l0zYa9nSt82htVg="
	siteCiphertextB64 = "HUcEBhzgQ3tJalHE4TxQMssM0eb2cIwEet+Jq2xFDNBu7xPcJP70j6ipMIV7XTn/yvJrkDSFBazLdNHhvFSGe7kgtzybwvlK3biWQDDArT3VHc2suJ91SqKMGaffJCBlT9sCbw0Lbq9PX2zgfcom0lraSko="
	sitePlaintext     = "SM2-Vector-Test-1234"
)

// TestSM2DecryptSiteCiphertext 站点 JS 生成的密文必须能被 Go 解出原文（同时校验 C3）。
func TestSM2DecryptSiteCiphertext(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(sitePrivateKeyB64)
	if err != nil {
		t.Fatalf("站点私钥向量非法: %v", err)
	}
	plaintext, err := sm2Decrypt(siteCiphertextB64, hex.EncodeToString(raw))
	if err != nil {
		t.Fatalf("解密站点密文失败: %v", err)
	}
	if string(plaintext) != sitePlaintext {
		t.Errorf("解密结果 = %q, 期望 %q", plaintext, sitePlaintext)
	}
}

// TestSM2DecryptRejectsWrongKey C3 校验必须能识别出错误的私钥。
func TestSM2DecryptRejectsWrongKey(t *testing.T) {
	if _, err := sm2Decrypt(siteCiphertextB64, strings.Repeat("01", 32)); err == nil {
		t.Fatal("错误私钥应导致 C3 校验失败")
	}
}

// TestSM2SiteKeyInterop 站点真实公钥必须能通过解析与曲线校验，并完成加解密往返。
func TestSM2SiteKeyInterop(t *testing.T) {
	point, err := sm2ParsePublicKey(sitePublicKeyB64)
	if err != nil {
		t.Fatalf("站点真实公钥被拒绝: %v", err)
	}
	if !sm2OnCurve(point) {
		t.Fatal("站点真实公钥不在曲线上")
	}

	privateKey, err := base64.StdEncoding.DecodeString(sitePrivateKeyB64)
	if err != nil {
		t.Fatalf("站点私钥向量非法: %v", err)
	}

	for _, plaintext := range []string{"A", "SamplePassword123", strings.Repeat("x", 40), "密码口令测试"} {
		cipher, err := sm2Encrypt([]byte(plaintext), sitePublicKeyB64)
		if err != nil {
			t.Fatalf("加密 %q 失败: %v", plaintext, err)
		}
		raw, err := base64.StdEncoding.DecodeString(cipher)
		if err != nil {
			t.Fatalf("密文不是合法 base64: %v", err)
		}
		// 线格式：C1.x(32) + C1.y(32) + C3(32) + C2(len(M))，C1 不带 04 前缀
		if want := 64 + 32 + len([]byte(plaintext)); len(raw) != want {
			t.Errorf("密文长度 = %d, 期望 %d", len(raw), want)
		}

		decrypted, err := sm2Decrypt(cipher, hex.EncodeToString(privateKey))
		if err != nil {
			t.Fatalf("解密 %q 失败: %v", plaintext, err)
		}
		if string(decrypted) != plaintext {
			t.Errorf("往返结果 = %q, 期望 %q", decrypted, plaintext)
		}
	}
}

// TestSM2EncryptMatchesPythonCipher 用固定 k 加密，逐字节比对 Python 参考密文。
func TestSM2EncryptMatchesPythonCipher(t *testing.T) {
	publicKey, err := sm2ParsePublicKey(pythonVectorPublicKey)
	if err != nil {
		t.Fatalf("解析公钥失败: %v", err)
	}
	k, ok := new(big.Int).SetString(pythonVectorK, 16)
	if !ok {
		t.Fatal("非法测试向量 k")
	}

	for _, vector := range pythonVectors {
		got, valid := sm2EncryptWithK([]byte(vector.plaintext), publicKey, k)
		if !valid {
			t.Fatalf("k 有效却返回 KDF 无效: %q", vector.plaintext)
		}
		if got != vector.cipher {
			t.Errorf("密文不一致\n got = %s\nwant = %s", got, vector.cipher)
		}
	}
}

// TestSM2EncryptDecryptRoundTrip 随机 k 的加解密自洽性。
func TestSM2EncryptDecryptRoundTrip(t *testing.T) {
	for _, plaintext := range []string{"PASS123", "中文口令ＡＢＣ123！", strings.Repeat("y", 100)} {
		cipher, err := sm2Encrypt([]byte(plaintext), pythonVectorPublicKey)
		if err != nil {
			t.Fatalf("加密 %q 失败: %v", plaintext, err)
		}
		raw, err := base64.StdEncoding.DecodeString(cipher)
		if err != nil {
			t.Fatalf("密文不是合法 base64: %v", err)
		}
		if want := 96 + len([]byte(plaintext)); len(raw) != want {
			t.Errorf("密文长度 = %d, 期望 %d", len(raw), want)
		}

		decrypted, err := sm2Decrypt(cipher, pythonVectorPrivateKey)
		if err != nil {
			t.Fatalf("解密失败: %v", err)
		}
		if string(decrypted) != plaintext {
			t.Errorf("解密结果 = %q, 期望 %q", decrypted, plaintext)
		}
	}
}

// TestSM2PublicKeyParsing 公钥解析：带/不带 04 前缀、非法长度、非曲线点。
func TestSM2PublicKeyParsing(t *testing.T) {
	withPrefix, err := base64.StdEncoding.DecodeString(pythonVectorPublicKey)
	if err != nil {
		t.Fatalf("测试向量公钥非法: %v", err)
	}
	if len(withPrefix) != 65 || withPrefix[0] != 0x04 {
		t.Fatalf("测试向量公钥应为 65 字节且以 04 开头, 实际 %d 字节", len(withPrefix))
	}

	withoutPrefix := base64.StdEncoding.EncodeToString(withPrefix[1:])
	pointA, err := sm2ParsePublicKey(pythonVectorPublicKey)
	if err != nil {
		t.Fatalf("带前缀公钥解析失败: %v", err)
	}
	pointB, err := sm2ParsePublicKey(withoutPrefix)
	if err != nil {
		t.Fatalf("不带前缀公钥解析失败: %v", err)
	}
	if pointA.x.Cmp(pointB.x) != 0 || pointA.y.Cmp(pointB.y) != 0 {
		t.Error("带/不带 04 前缀解析结果不一致")
	}

	if _, err := sm2ParsePublicKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Error("非法长度公钥应报错")
	}
	if _, err := sm2ParsePublicKey("!!!not-base64!!!"); err == nil {
		t.Error("非法 base64 应报错")
	}

	// 不在曲线上的点（y 加一）
	bad := make([]byte, 65)
	copy(bad, withPrefix)
	bad[64] ^= 0x01
	if _, err := sm2ParsePublicKey(base64.StdEncoding.EncodeToString(bad)); err == nil {
		t.Error("非曲线点应报错")
	}
}

// TestSM2CurveParameters 曲线参数自检：G 在曲线上，且 N*G 为无穷远点。
func TestSM2CurveParameters(t *testing.T) {
	if !sm2OnCurve(sm2G) {
		t.Fatal("基点 G 不在 SM2 曲线上")
	}
	if sm2ScalarMult(sm2N, sm2G) != nil {
		t.Fatal("N*G 应为无穷远点")
	}
}

func sm2OnCurve(point *sm2Point) bool {
	if point == nil {
		return true
	}
	left := new(big.Int).Mul(point.y, point.y)
	left.Mod(left, sm2P)
	right := new(big.Int).Mul(point.x, point.x)
	right.Mul(right, point.x)
	right.Add(right, new(big.Int).Mul(sm2A, point.x))
	right.Add(right, sm2B)
	right.Mod(right, sm2P)
	return left.Cmp(right) == 0
}

// TestLoginPayloadRedirectService 回跳地址解析：字符串 / 对象 / 缺失。
func TestLoginPayloadRedirectService(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{"字符串", `{"code":666666,"data":{"service":"https://course.buct.edu.cn/x?ticket=ST-1"}}`,
			"https://course.buct.edu.cn/x?ticket=ST-1", true},
		{"对象", `{"code":666666,"data":{"service":{"a":1}}}`, `{"a":1}`, true},
		{"缺失", `{"code":666666,"data":{}}`, "", false},
		{"空字符串", `{"code":666666,"data":{"service":""}}`, "", false},
		{"无 data", `{"code":666666}`, "", false},
		{"data 是字符串", `{"code":666666,"data":"oops"}`, "", false},
	}
	for _, tc := range cases {
		var payload loginPayload
		if err := json.Unmarshal([]byte(tc.body), &payload); err != nil {
			t.Fatalf("%s: 解析失败: %v", tc.name, err)
		}
		got, ok := payload.redirectService()
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: got (%q, %v), 期望 (%q, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// TestLoginErrorMessage 返回码翻译。
func TestLoginErrorMessage(t *testing.T) {
	cases := []struct {
		payload loginPayload
		want    string
	}{
		{loginPayload{Code: 170002, Msg: "服务端文案"}, "用户名或密码错误"},
		{loginPayload{Code: 123456, Msg: "服务端文案"}, "服务端文案"},
		{loginPayload{Code: 123456}, "登录失败（code=123456）"},
	}
	for _, tc := range cases {
		if got := tc.payload.loginErrorMessage(); got != tc.want {
			t.Errorf("code=%d: got %q, 期望 %q", tc.payload.Code, got, tc.want)
		}
	}
}

// TestSM3EmptyInput 确保空输入不会 panic（分块填充边界）。
func TestSM3EmptyInput(t *testing.T) {
	if len(sm3Sum(nil)) != 32 {
		t.Fatal("SM3 摘要长度应为 32 字节")
	}
	if bytes.Equal(sm3Sum(nil), sm3Sum([]byte("a"))) {
		t.Fatal("不同输入不应得到相同摘要")
	}
}
