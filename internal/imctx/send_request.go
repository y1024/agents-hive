package imctx

// SendRequest 是 IM 出站路径的跨包 DTO。
//
// tools / API / channel.Router 都可以依赖 imctx；不要让 internal/tools import
// internal/channel，否则会形成 tools -> channel -> master -> tools 的循环依赖。
type SendRequest struct {
	Platform    Platform
	TenantKey   string
	OwnerUserID string
	ChatID      string
	Content     string
	MsgType     string
	ReplyTo     string
	ReplyToken  string
}
