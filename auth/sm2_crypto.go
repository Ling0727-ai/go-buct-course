package auth

// 纯 Go SM2（GM/T 0003）实现，供北化统一身份认证登录加密口令使用。
//
// 与 portal.buct.edu.cn 前端（sm2.min.js / gm-crypto）以及 Python 参考实现
// buct_course/sm2_crypto.py 保持一致：
//
//	线格式: base64( C1.x || C1.y || C3 || C2 )   // C1 为 64 字节，不带 04 前缀
//
// 两个容易踩坑的细节（均已用实测向量确认）：
//
//  1. C1 是裸的 x1||y1（64 字节），**不带**未压缩点标志 04。
//  2. KDF 的输入是共享点 x2||y2 的**原始 64 字节**，不是它的十六进制字符串。
//
// 本文件不依赖任何第三方库，也不需要在运行时执行站点 JS。

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math/big"
	"math/bits"
)

// --------------------------------------------------------------------------
// SM2 曲线参数（GM/T 0003.5-2012）
// --------------------------------------------------------------------------

var (
	sm2P  = sm2MustHex("FFFFFFFEFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF00000000FFFFFFFFFFFFFFFF")
	sm2A  = sm2MustHex("FFFFFFFEFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF00000000FFFFFFFFFFFFFFFC")
	sm2B  = sm2MustHex("28E9FA9E9D9F5E344D5A9E4BCF6509A7F39789F515AB8F92DDBCBD414D940E93")
	sm2N  = sm2MustHex("FFFFFFFEFFFFFFFFFFFFFFFFFFFFFFFF7203DF6B21C6052B53BBF40939D54123")
	sm2GX = sm2MustHex("32C4AE2C1F1981195F9904466A39C9948FE30BBFF2660BE1715A4589334C74C7")
	sm2GY = sm2MustHex("BC3736A2F4F6779C59BDCEE36B692153D0A9877CC62A474002DF32E52139F0A0")
	sm2G  = &sm2Point{sm2GX, sm2GY}
)

func sm2MustHex(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 16)
	if !ok {
		panic("sm2_crypto: 非法的曲线参数: " + s)
	}
	return v
}

// --------------------------------------------------------------------------
// SM3 杂凑算法（GM/T 0004-2012）
// --------------------------------------------------------------------------

var sm3IV = [8]uint32{
	0x7380166F, 0x4914B2B9, 0x172442D7, 0xDA8A0600,
	0xA96F30BC, 0x163138AA, 0xE38DEE4D, 0xB0FB0E4E,
}

func sm3P0(x uint32) uint32 { return x ^ bits.RotateLeft32(x, 9) ^ bits.RotateLeft32(x, 17) }

func sm3P1(x uint32) uint32 { return x ^ bits.RotateLeft32(x, 15) ^ bits.RotateLeft32(x, 23) }

// sm3Sum 计算 SM3 摘要，返回 32 字节。
func sm3Sum(data []byte) []byte {
	bitLength := uint64(len(data)) * 8

	padded := make([]byte, 0, len(data)+72)
	padded = append(padded, data...)
	padded = append(padded, 0x80)
	for len(padded)%64 != 56 {
		padded = append(padded, 0x00)
	}
	var lengthBytes [8]byte
	binary.BigEndian.PutUint64(lengthBytes[:], bitLength)
	padded = append(padded, lengthBytes[:]...)

	state := sm3IV
	var w [68]uint32
	var w1 [64]uint32

	for offset := 0; offset < len(padded); offset += 64 {
		block := padded[offset : offset+64]
		for i := 0; i < 16; i++ {
			w[i] = binary.BigEndian.Uint32(block[i*4 : i*4+4])
		}
		for j := 16; j < 68; j++ {
			w[j] = sm3P1(w[j-16]^w[j-9]^bits.RotateLeft32(w[j-3], 15)) ^
				bits.RotateLeft32(w[j-13], 7) ^ w[j-6]
		}
		for j := 0; j < 64; j++ {
			w1[j] = w[j] ^ w[j+4]
		}

		a, b, c, d := state[0], state[1], state[2], state[3]
		e, f, g, h := state[4], state[5], state[6], state[7]

		for j := 0; j < 64; j++ {
			a12 := bits.RotateLeft32(a, 12)
			t := uint32(0x79CC4519)
			if j >= 16 {
				t = 0x7A879D8A
			}
			ss1 := bits.RotateLeft32(a12+e+bits.RotateLeft32(t, j), 7)
			ss2 := ss1 ^ a12

			var ff, gg uint32
			if j < 16 {
				ff = a ^ b ^ c
				gg = e ^ f ^ g
			} else {
				ff = (a & b) | (a & c) | (b & c)
				gg = (e & f) | (^e & g)
			}

			tt1 := ff + d + ss2 + w1[j]
			tt2 := gg + h + ss1 + w[j]
			d = c
			c = bits.RotateLeft32(b, 9)
			b = a
			a = tt1
			h = g
			g = bits.RotateLeft32(f, 19)
			f = e
			e = sm3P0(tt2)
		}

		state[0] ^= a
		state[1] ^= b
		state[2] ^= c
		state[3] ^= d
		state[4] ^= e
		state[5] ^= f
		state[6] ^= g
		state[7] ^= h
	}

	out := make([]byte, 0, 32)
	for _, word := range state {
		out = binary.BigEndian.AppendUint32(out, word)
	}
	return out
}

// --------------------------------------------------------------------------
// 椭圆曲线运算（素域上的仿射坐标）
// --------------------------------------------------------------------------

// sm2Point 为仿射坐标下的曲线点，nil 表示无穷远点。
type sm2Point struct {
	x, y *big.Int
}

func sm2PointAdd(p1, p2 *sm2Point) *sm2Point {
	if p1 == nil {
		return p2
	}
	if p2 == nil {
		return p1
	}

	var slope *big.Int
	if p1.x.Cmp(p2.x) == 0 {
		// 互为逆元 => 无穷远点
		sum := new(big.Int).Add(p1.y, p2.y)
		if sum.Mod(sum, sm2P).Sign() == 0 {
			return nil
		}
		// 倍点: slope = (3*x1^2 + A) / (2*y1)
		num := new(big.Int).Mul(p1.x, p1.x)
		num.Mul(num, big.NewInt(3))
		num.Add(num, sm2A)
		num.Mod(num, sm2P)

		den := new(big.Int).Lsh(p1.y, 1)
		den.Mod(den, sm2P)
		den.ModInverse(den, sm2P)
		slope = new(big.Int).Mul(num, den)
	} else {
		num := new(big.Int).Sub(p2.y, p1.y)
		num.Mod(num, sm2P)
		den := new(big.Int).Sub(p2.x, p1.x)
		den.Mod(den, sm2P)
		den.ModInverse(den, sm2P)
		slope = new(big.Int).Mul(num, den)
	}
	slope.Mod(slope, sm2P)

	x3 := new(big.Int).Mul(slope, slope)
	x3.Sub(x3, p1.x)
	x3.Sub(x3, p2.x)
	x3.Mod(x3, sm2P)

	y3 := new(big.Int).Sub(p1.x, x3)
	y3.Mul(slope, y3)
	y3.Sub(y3, p1.y)
	y3.Mod(y3, sm2P)

	return &sm2Point{x3, y3}
}

func sm2ScalarMult(k *big.Int, point *sm2Point) *sm2Point {
	var result *sm2Point
	addend := point
	for i := 0; i < k.BitLen(); i++ {
		if k.Bit(i) == 1 {
			result = sm2PointAdd(result, addend)
		}
		addend = sm2PointAdd(addend, addend)
	}
	return result
}

// sm2KDF 为 SM2 密钥派生函数；输入 z 由调用方按站点约定构造。
func sm2KDF(z []byte, keyLength int) []byte {
	if keyLength <= 0 {
		return nil
	}
	out := make([]byte, 0, keyLength+32)
	buf := make([]byte, 0, len(z)+4)
	var counter [4]byte
	for i := 1; len(out) < keyLength; i++ {
		binary.BigEndian.PutUint32(counter[:], uint32(i))
		buf = append(buf[:0], z...)
		buf = append(buf, counter[:]...)
		out = append(out, sm3Sum(buf)...)
	}
	return out[:keyLength]
}

// sm2Pad32 将大整数编码为定长 32 字节大端。
func sm2Pad32(v *big.Int) []byte {
	out := make([]byte, 32)
	v.FillBytes(out)
	return out
}

// sm2ParsePublicKey 解析 base64 公钥（接受带或不带 04 前缀的 64 字节点）。
func sm2ParsePublicKey(publicKeyB64 string) (*sm2Point, error) {
	raw, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return nil, fmt.Errorf("解析 SM2 公钥失败: %w", err)
	}
	if len(raw) == 65 && raw[0] == 0x04 {
		raw = raw[1:]
	}
	if len(raw) != 64 {
		return nil, fmt.Errorf("不支持的 SM2 公钥长度: %d 字节", len(raw))
	}

	x := new(big.Int).SetBytes(raw[:32])
	y := new(big.Int).SetBytes(raw[32:])
	if x.Cmp(sm2P) >= 0 || y.Cmp(sm2P) >= 0 {
		return nil, fmt.Errorf("SM2 公钥坐标超出域范围")
	}
	// 校验点在曲线上: y^2 == x^3 + A*x + B (mod P)
	left := new(big.Int).Mul(y, y)
	left.Mod(left, sm2P)
	right := new(big.Int).Mul(x, x)
	right.Mul(right, x)
	ax := new(big.Int).Mul(sm2A, x)
	right.Add(right, ax)
	right.Add(right, sm2B)
	right.Mod(right, sm2P)
	if left.Cmp(right) != 0 {
		return nil, fmt.Errorf("SM2 公钥不在曲线上")
	}
	return &sm2Point{x, y}, nil
}

// sm2EncryptWithK 使用指定随机数 k 加密，返回密文与 KDF 是否有效。
//
// 抽离出来是为了让测试可以用固定的 k 与 Python 参考实现的向量逐字节比对。
func sm2EncryptWithK(plaintext []byte, publicKey *sm2Point, k *big.Int) (string, bool) {
	c1 := sm2ScalarMult(k, sm2G)
	shared := sm2ScalarMult(k, publicKey)
	if c1 == nil || shared == nil {
		return "", false
	}

	z := make([]byte, 0, 64)
	z = append(z, sm2Pad32(shared.x)...)
	z = append(z, sm2Pad32(shared.y)...)

	t := sm2KDF(z, len(plaintext))
	// KDF 结果全零时需要换一个 k 重试（GM/T 0003.4 要求）
	if len(t) > 0 {
		nonZero := false
		for _, b := range t {
			if b != 0 {
				nonZero = true
				break
			}
		}
		if !nonZero {
			return "", false
		}
	}

	c2 := make([]byte, len(plaintext))
	for i := range plaintext {
		c2[i] = plaintext[i] ^ t[i]
	}

	c3Input := make([]byte, 0, 64+len(plaintext))
	c3Input = append(c3Input, sm2Pad32(shared.x)...)
	c3Input = append(c3Input, plaintext...)
	c3Input = append(c3Input, sm2Pad32(shared.y)...)
	c3 := sm3Sum(c3Input)

	out := make([]byte, 0, 96+len(c2))
	out = append(out, sm2Pad32(c1.x)...)
	out = append(out, sm2Pad32(c1.y)...)
	out = append(out, c3...)
	out = append(out, c2...)
	return base64.StdEncoding.EncodeToString(out), true
}

// sm2Encrypt 用 base64 公钥加密，返回 base64 密文（C1||C3||C2，C1 无 04 前缀）。
func sm2Encrypt(plaintext []byte, publicKeyB64 string) (string, error) {
	publicKey, err := sm2ParsePublicKey(publicKeyB64)
	if err != nil {
		return "", err
	}

	nMinusOne := new(big.Int).Sub(sm2N, big.NewInt(1))
	for {
		k, err := rand.Int(rand.Reader, nMinusOne)
		if err != nil {
			return "", fmt.Errorf("生成 SM2 随机数失败: %w", err)
		}
		k.Add(k, big.NewInt(1)) // k ∈ [1, N-1]
		if cipher, ok := sm2EncryptWithK(plaintext, publicKey, k); ok {
			return cipher, nil
		}
	}
}

// sm2Decrypt 用十六进制私钥解密 base64 密文。主要用于兼容性自检。
func sm2Decrypt(cipherB64 string, privateKeyHex string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		return nil, fmt.Errorf("解析密文失败: %w", err)
	}
	if len(raw) < 96 {
		return nil, fmt.Errorf("密文长度不足")
	}

	c1 := &sm2Point{
		x: new(big.Int).SetBytes(raw[0:32]),
		y: new(big.Int).SetBytes(raw[32:64]),
	}
	c3 := raw[64:96]
	c2 := raw[96:]

	d, ok := new(big.Int).SetString(privateKeyHex, 16)
	if !ok {
		return nil, fmt.Errorf("非法的 SM2 私钥")
	}

	shared := sm2ScalarMult(d, c1)
	if shared == nil {
		return nil, fmt.Errorf("SM2 解密得到无穷远点")
	}
	z := make([]byte, 0, 64)
	z = append(z, sm2Pad32(shared.x)...)
	z = append(z, sm2Pad32(shared.y)...)

	t := sm2KDF(z, len(c2))
	plaintext := make([]byte, len(c2))
	for i := range c2 {
		plaintext[i] = c2[i] ^ t[i]
	}

	expected := make([]byte, 0, 64+len(plaintext))
	expected = append(expected, sm2Pad32(shared.x)...)
	expected = append(expected, plaintext...)
	expected = append(expected, sm2Pad32(shared.y)...)
	digest := sm3Sum(expected)
	for i := range c3 {
		if c3[i] != digest[i] {
			return nil, fmt.Errorf("C3 校验失败：密文或私钥不匹配")
		}
	}
	return plaintext, nil
}
