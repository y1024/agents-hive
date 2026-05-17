package store

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetCrypto 重置全局加密单例，以便测试之间互不干扰
func resetCrypto(key []byte) {
	tokenCryptoOnce = sync.Once{}
	tokenCryptoKey = key
}

// TestEncryptDecrypt_WithKey 验证加密模式下加解密往返正确
func TestEncryptDecrypt_WithKey(t *testing.T) {
	// 使用固定 32 字节密钥（测试专用）
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	resetCrypto(key)
	defer resetCrypto(nil)

	tests := []string{
		"simple-token",
		"token:with:colons",                      // SEC-010 核心场景：含冒号的 OAuth token
		"https://example.com/oauth?code=abc:def", // URL 格式 token（含多个冒号）
		"",                                       // 空字符串
		"Bearer eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.payload.signature",
	}

	for _, plaintext := range tests {
		t.Run("往返_"+plaintext, func(t *testing.T) {
			encrypted, err := encryptToken(plaintext)
			require.NoError(t, err)

			// 加密后必须以 "enc:" 开头
			assert.True(t, strings.HasPrefix(encrypted, encryptedPrefix),
				"加密结果必须以 %q 开头，实际: %q", encryptedPrefix, encrypted)

			decrypted, err := decryptToken(encrypted)
			require.NoError(t, err)
			assert.Equal(t, plaintext, decrypted, "解密结果应与原文一致")
		})
	}
}

// TestEncryptedPrefix_NotMistakenForPlaintext 验证 enc: 前缀可靠区分密文与明文
func TestEncryptedPrefix_NotMistakenForPlaintext(t *testing.T) {
	key := make([]byte, 32)
	resetCrypto(key)
	defer resetCrypto(nil)

	// 含冒号的明文 token 在明文模式下存入后，不应被误解密
	oauthToken := "ya29.token:with:colons"
	// 模拟旧数据（无 enc: 前缀），decryptToken 应原样返回
	got, err := decryptToken(oauthToken)
	require.NoError(t, err)
	assert.Equal(t, oauthToken, got, "不以 enc: 开头的旧明文应原样返回，不应尝试解密")
}

// TestDecryptToken_PlaintextMode 验证密钥未配置时明文直通
func TestDecryptToken_PlaintextMode(t *testing.T) {
	resetCrypto(nil)
	// 需要同时重置 Once，让 initTokenCrypto 不执行任何操作
	tokenCryptoOnce = sync.Once{}
	defer resetCrypto(nil)

	// 明文模式下，任何字符串应原样返回
	cases := []string{
		"simple-token",
		"token:with:colon",
		"enc:looks-like-encrypted-but-no-key",
	}
	for _, tc := range cases {
		got, err := decryptToken(tc)
		require.NoError(t, err)
		assert.Equal(t, tc, got, "明文模式应原样返回")
	}
}

// TestEncryptToken_PlaintextMode 验证密钥未配置时加密直通
func TestEncryptToken_PlaintextMode(t *testing.T) {
	resetCrypto(nil)
	tokenCryptoOnce = sync.Once{}
	defer resetCrypto(nil)

	got, err := encryptToken("my-token")
	require.NoError(t, err)
	assert.Equal(t, "my-token", got, "明文模式下 encryptToken 应原样返回")
	// 明文模式下不应添加 enc: 前缀
	assert.False(t, strings.HasPrefix(got, encryptedPrefix))
}

// TestDecryptToken_BackwardCompatibility 验证旧格式（hex:hex，无 enc: 前缀）被视为明文
func TestDecryptToken_BackwardCompatibility(t *testing.T) {
	key := make([]byte, 32)
	resetCrypto(key)
	defer resetCrypto(nil)

	// 旧版格式：没有 "enc:" 前缀，直接是 hex:hex
	// 新版 decryptToken 应将其视为明文原样返回（向后兼容）
	oldFormatLike := "aabbccdd:11223344556677889900aabbccddeeff"
	got, err := decryptToken(oldFormatLike)
	require.NoError(t, err)
	assert.Equal(t, oldFormatLike, got, "不含 enc: 前缀的旧数据应原样返回")
}

// TestDecryptToken_CorruptedCiphertext 验证损坏密文返回错误而非静默崩溃
func TestDecryptToken_CorruptedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	resetCrypto(key)
	defer resetCrypto(nil)

	// 构造一个带有正确前缀但内容损坏的字符串
	corrupted := encryptedPrefix + "deadbeef:0000000000000000000000000000000000000000000000000000000000000000"
	_, err := decryptToken(corrupted)
	// 损坏的密文应返回错误（而不是静默返回原文）
	assert.Error(t, err, "损坏的密文应返回错误")
}

// TestIsEncryptionEnabled 验证密钥状态检测函数
func TestIsEncryptionEnabled(t *testing.T) {
	// 无密钥
	resetCrypto(nil)
	tokenCryptoOnce = sync.Once{}
	assert.False(t, isEncryptionEnabled(), "无密钥时应返回 false")

	// 有密钥
	key := make([]byte, 32)
	resetCrypto(key)
	assert.True(t, isEncryptionEnabled(), "有密钥时应返回 true")
}
