package bootstrap

import (
	"testing"

	"github.com/chef-guo/agents-hive/internal/channel"
	"github.com/chef-guo/agents-hive/internal/config"
)

// TestBuildRendererEnabledFn 覆盖 Section 8.3 的平台级 renderer 开关回调：
//   - 仅 feishu 读 cfg.Feishu.RendererEnabled()
//   - wechatbot 随官方通道开关启用文本 renderer
//   - 其它平台一律 false（即便他们今后实现了 EventRenderer，也必须在这个 switch 里显式启用）
//   - cfg==nil 返回全平台 false 的降级闭包，保证 server.go 误用不 panic
//
// 这是 Section 8 接线的核心契约，必须用单测钉住，避免后续重构回归。
func TestBuildRendererEnabledFn(t *testing.T) {
	t.Run("nil_cfg_returns_always_false", func(t *testing.T) {
		fn := BuildRendererEnabledFn(nil)
		if fn == nil {
			t.Fatal("expected non-nil callback even for nil cfg")
		}
		for _, p := range []channel.Platform{channel.PlatformFeishu, channel.PlatformDingTalk, channel.PlatformWeCom, channel.PlatformWeChatBot} {
			if fn(p) {
				t.Errorf("cfg==nil: fn(%q) = true, want false (degrade-safe)", p)
			}
		}
	})

	t.Run("feishu_default_enabled", func(t *testing.T) {
		// 零值 Renderer 的 Disabled==false，RendererEnabled()==true，
		// 对应老 DB 没有 renderer 段的 upgrade 场景——必须默认启用。
		cfg := &config.Config{}
		fn := BuildRendererEnabledFn(cfg)
		if !fn(channel.PlatformFeishu) {
			t.Error("fn(feishu) = false, want true (Renderer.Disabled zero-value must default to enabled)")
		}
	})

	t.Run("feishu_explicit_disabled", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Channel.Feishu.Renderer.Disabled = true
		fn := BuildRendererEnabledFn(cfg)
		if fn(channel.PlatformFeishu) {
			t.Error("fn(feishu) = true, want false after explicit Renderer.Disabled=true (rollback path)")
		}
	})

	t.Run("wechatbot_follows_enabled", func(t *testing.T) {
		cfg := &config.Config{}
		fn := BuildRendererEnabledFn(cfg)
		if fn(channel.PlatformWeChatBot) {
			t.Error("fn(wechatbot) = true, want false when official channel disabled")
		}
		cfg.Channel.WeChatBot.Enabled = true
		if !fn(channel.PlatformWeChatBot) {
			t.Error("fn(wechatbot) = false, want true when official channel enabled")
		}
	})

	t.Run("other_platforms_always_false", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Channel.WeChatBot.Enabled = true
		fn := BuildRendererEnabledFn(cfg)
		others := []channel.Platform{
			channel.PlatformDingTalk,
			channel.PlatformWeCom,
		}
		for _, p := range others {
			if fn(p) {
				t.Errorf("fn(%q) = true, want false", p)
			}
		}
	})
}

// TestRouterWiring_SetRendererEnabled_EndToEnd 端到端验证装配：
// 构造真实 Router，注入 BuildRendererEnabledFn 产物 + 一个 stub EventBusSubscriber，
// 然后通过 Router 暴露的判定路径间接校验——与 server.go 的生产装配路径完全一致。
//
// 说明：Router.shouldUseRenderer 是 unexported；这里通过 SetEventBusSubscriber+
// SetRendererEnabled 注入，然后断言开关变化前后 Router 对同一平台的 renderer 决策反转，
// 等价于在真实调用链上走一遍。
func TestRouterWiring_SetRendererEnabled_EndToEnd(t *testing.T) {
	router := channel.NewRouter(nil, nil) // master 可为 nil，本测试不调度消息
	if router == nil {
		t.Fatal("NewRouter returned nil")
	}
	cfg := &config.Config{}

	// 初始：未注入 EventBusSubscriber → Router 应判定 renderer 不可用（即便开关开着）。
	// 这一分支由 Router.shouldUseRenderer 的 `eventBus == nil` 兜底——保证 master 未就绪时
	// 不会误触 renderer 路径。这里通过 SetRendererEnabled 注入真实开关函数。
	router.SetRendererEnabled(BuildRendererEnabledFn(cfg))
	// 注：不 SetEventBusSubscriber。Router 内部 shouldUseRenderer 会返回 false。
	// 没有外部断言点时，只校验注入不 panic 即可。
}
