# BUCT sidportal 登录协议分析

分析日期：2026-10-18。范围：用户指定登录页及其公开脚本；使用合成数据离线验证；未提交真实账户、未复用用户提供的抓包密文、未绕过验证码。

## Evidence → Finding → Path

### E1：登录 HTML → 两个页面参数 → 页面解析

来源：[登录页](https://sidportal.buct.edu.cn/login?service=https%3A%2F%2Fcourse.buct.edu.cn%2Fmeol%2Fhomepage%2Fcommon%2Fsso_login.jsp)。

- `#login-croypto` 是 Base64 编码的 AES 密钥。按页面原值提交 `croypto`，不要“修正”其拼写。
- `#login-page-flowkey` 是本次 `execution`；当作不透明字符串，不尝试解码、重建或硬编码。
- `#recaptchaVendor`、`#riskSystemSwitch`、`#login-rule-type`、`#sso-second` 等决定后续验证要求。
- 页面经 `loginNew.js`、`deploy.js` 和页面 bundle 清单加载 Angular 登录代码。

实现：[页面解析与新版流程](../auth/sidportal.go)。下载的原始 HTML 含短期会话参数，仅保存在被 Git 忽略的本地 `work/`，不作为可分发测试夹具。

### E2：发布清单 → 具体 bundle → 加密函数

来源：
- [loginNew.js](https://sidportal.buct.edu.cn/public/utils/loginNew.js)
- [deploy.js](https://sidportal.buct.edu.cn/public/deploy/deploy.js)
- [caspagehash.js](https://sidportal.buct.edu.cn/public/login-page/caspagehash.js)
- [main-es2015.f081d394156237abc751.js](https://sidportal.buct.edu.cn/public/cas-login/main-es2015.f081d394156237abc751.js)
- [crypto-merged.min.js](https://sidportal.buct.edu.cn/public/crypto/crypto-merged.min.js)

清单中的发布提交：`3b1e0447a823087f4781b6fac134c865976e2f45`。

JS 的 `aesEncrypt(key, text)`：

```javascript
const rawKey = CryptoJS.enc.Base64.parse(key);
return CryptoJS.AES.encrypt(text, rawKey, {
  mode: CryptoJS.mode.ECB,
  padding: CryptoJS.pad.Pkcs7
}).toString();
```

即 `Base64(AES-ECB(Base64Decode(croypto), PKCS7(UTF8(text))))`。无 IV，不使用 OpenSSL Salted__ 格式，不对密码进行全角转半角处理。

实现：[encryptSIDValue](../auth/sidportal.go)；交叉测试：[合成向量和离线流程](../auth/sidportal_test.go)。

### E3：onSubmit → 原生表单 → POST /login

原生 `normalLoginForm` 的 `action="login"`、`method="POST"`，表单字段：

| 字段 | 来源 |
|---|---|
| `username` | 用户名 |
| `type` | `UsernamePassword` |
| `_eventId` | `submit` |
| `geolocation` | 空字符串 |
| `execution` | 页面 `login-page-flowkey` |
| `captcha_code` | 图形验证码；无需验证码时为空 |
| `croypto` | 页面 `login-croypto` 原值 |
| `password` | 使用该密钥加密原始密码 |
| `captcha_payload` | 使用该密钥加密 `JSON.stringify(captcha_payload)` |

默认系统验证且未要求挑战时，前端初始化 `captcha_payload={}`，因此加密的明文为两个字符 `{}`。需要第三方验证码或风险校验时，不应伪造 `{error:true}` 来回避验证；客户端明确停止并提示浏览器处理。

原生相对表单 action 解析到 `/login`，不保留页面的 `service` 查询参数；service 仍由本次认证 flow 携带。HTTP 层按 `application/x-www-form-urlencoded` 编码，尤其保留 Base64 中 `+`、`/`、`=` 的含义。全流程使用同一 CookieJar。

### E4：验证码预检查 → 服务端要求 → 禁止盲目提交

源码的 `getCount(username)` 请求 `/api/protected/user/findCaptchaCount/{username}`。对匿名 `null` 的公开 GET 已核对返回形状：

```json
{"code":200,"data":{"captchaInvisible":false,"captchaUrl":null}}
```

仅摘录相关字段；匿名返回值不能证明任何具体账户无需验证码。客户端实际登录前对传入用户名执行检查，并对缺字段、错误响应或要求验证码的结果停止提交。

CSRF interceptor：随机 32 字符 `key`，`b=Base64(UTF8(key))`，`Csrf-Value=hex(MD5(b前半+b+b后半))`。这是站点指定的请求头协议，不是密码哈希。

### E5：业务回跳 → 会话建立 → 不把 HTTP 200 当成功

新版成功响应须经 GET 回到指定 service 所属业务主机。课程平台允许 `/meol/` 下的回跳，例如 `homepage/common/sso_login.jsp` 再跳转 `homepage/student/index.jsp`。返回登录参数、登录错误、GBK 错误页、非预期主机或业务登录页均不视为成功。限制重定向主机，拒绝 307/308 重放携带凭据的 POST。

保留旧 portal CAS 的 SM2 实现，按 MEOL 入口及旧 CAS 后续跳转选择协议。新版密码提交不自动重试，以免增加错误计数或触发账户锁定。

## 验证和限制

- 使用公开站点 CryptoJS 在隔离 Node VM 中生成合成密钥/明文向量，Go 对照验证：空串、`{}`、单块边界、全角与中文、多块密码。
- 离线传输层覆盖新入口、旧门户转新入口、显式 service、Cookie、字段编码、CSRF、验证挑战、错误页、锁定、会话重置及不可信/凭据重放重定向。
- 未在真实账户上验证成功 POST、真实票据回跳和账户特定 MFA 策略；正常线上登录是否成功仍需用户在本机输入凭据验证。
- 本次只更新 Go 认证路径；仓库中的 Python 参考客户端未同步。

## 本地复用经验

1. 登录域名变化时先核对 SSO 重定向和 bundle 清单，不要继续向旧 JSON 接口盲发密码。
2. 不从抓包密文长度推断算法；从 `onSubmit` 追到实际 `aesEncrypt`，再用合成向量交叉验证。
3. `captcha_payload` 即使只含 `{}` 也必须按同一页面密钥加密；不是空字符串。
4. Cookie、execution、密钥必须属于同一新建会话；真实抓包不可作为长期测试夹具。
5. 离线模拟必须包含 common→student 的跨目录业务回跳，避免成功校验过窄。
