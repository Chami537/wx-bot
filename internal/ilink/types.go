package ilink

// IncomingMessage mirrors the iLink bot message structure returned by getupdates.
type IncomingMessage struct {
	Seq          int64         `json:"seq"`
	MessageID    int64         `json:"message_id"`
	FromUserID   string        `json:"from_user_id"`
	ToUserID     string        `json:"to_user_id"`
	ClientID     string        `json:"client_id"`
	CreateTimeMs int64         `json:"create_time_ms"`
	ContextToken string        `json:"context_token"`
	ItemList     []MessageItem `json:"item_list"`
}

const (
	ItemTypeText  = 1
	ItemTypeImage = 2
	ItemTypeVoice = 3
	ItemTypeFile  = 4
	ItemTypeVideo = 5
)

// MessageItem is one content element inside a message.
type MessageItem struct {
	Type      int        `json:"type"`
	TextItem  *TextItem  `json:"text_item,omitempty"`
	ImageItem *ImageItem `json:"image_item,omitempty"`
	VoiceItem *VoiceItem `json:"voice_item,omitempty"`
	FileItem  *FileItem  `json:"file_item,omitempty"`
	VideoItem *VideoItem `json:"video_item,omitempty"`
}

// TextItem carries plain text.
type TextItem struct {
	Text string `json:"text"`
}

// CDNMedia holds the location and decryption info for media stored on WeChat CDN.
type CDNMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param"` // download param from CDN x-encrypted-param
	// AESKey is the CDN AES key as a JSON string.
	// Outbound sendmessage: base64 of the ASCII hex key (32 chars), matching the official plugin.
	// Inbound: raw 16 bytes, hex32, base64(raw), or base64(hex) — see ParseAESKey.
	AESKey      string `json:"aes_key"`
	EncryptType int    `json:"encrypt_type"` // 1 = AES-128-ECB
	// FullURL is the complete download URL returned directly by the server.
	// Use this when set; no client-side URL construction needed.
	FullURL string `json:"full_url,omitempty"`
}

// ImageItem carries an image.
type ImageItem struct {
	Media      CDNMedia  `json:"media"`
	ThumbMedia *CDNMedia `json:"thumb_media,omitempty"`
	// AESKey is the raw AES-128 key as a hex string (32 hex chars = 16 bytes).
	// Preferred over Media.AESKey for inbound image decryption.
	AESKey  string `json:"aeskey,omitempty"`
	URL     string `json:"url,omitempty"`
	MidSize int64  `json:"mid_size,omitempty"`
	HdSize  int64  `json:"hd_size,omitempty"`
}

// VoiceItem carries a voice message. The plugin provides a text transcription.
type VoiceItem struct {
	Media      CDNMedia `json:"media"`
	SampleRate int      `json:"sample_rate"`
	Playtime   int      `json:"playtime"` // ms
	Text       string   `json:"text"`     // transcription from WeChat
}

// FileItem carries a file attachment.
type FileItem struct {
	Media    CDNMedia `json:"media"`
	FileName string   `json:"file_name"`
	Md5      string   `json:"md5,omitempty"`
	Len      string   `json:"len"` // size in bytes (string in protocol)
}

// VideoItem carries a video.
type VideoItem struct {
	Media      CDNMedia  `json:"media"`
	ThumbMedia *CDNMedia `json:"thumb_media,omitempty"`
	VideoSize  int64     `json:"video_size,omitempty"`  // ciphertext size
	PlayLength int64     `json:"play_length,omitempty"` // ms
	VideoMd5   string    `json:"video_md5,omitempty"`
}

// SendMessageRequest is sent to the iLink bot sendmessage endpoint.
type SendMessageRequest struct {
	Msg      OutgoingMessage `json:"msg"`
	BaseInfo BaseInfo        `json:"base_info"`
}

// OutgoingMessage is the message to deliver back via WeChat.
type OutgoingMessage struct {
	FromUserID   string        `json:"from_user_id"`
	ToUserID     string        `json:"to_user_id"`
	ClientID     string        `json:"client_id"`
	MessageType  int           `json:"message_type"`  // 2 = BOT
	MessageState int           `json:"message_state"` // 2 = FINISH
	ContextToken string        `json:"context_token"`
	ItemList     []MessageItem `json:"item_list"`
}

// BaseInfo is attached to all requests.
type BaseInfo struct {
	ChannelVersion string `json:"channel_version"`
}

// Upload media types for getuploadurl (proto UploadMediaType).
// NOTE: these are NOT the same numbers as the message ItemType* constants.
const (
	UploadMediaImage = 1
	UploadMediaVideo = 2
	UploadMediaFile  = 3
	UploadMediaVoice = 4
)

// GetUploadURLRequest asks iLink for a CDN upload URL.
// Field set mirrors @tencent-weixin/openclaw-weixin (src/api/api.ts getUploadUrl).
type GetUploadURLRequest struct {
	Filekey       string   `json:"filekey"`    // random 16 bytes, hex
	MediaType     int      `json:"media_type"` // UploadMedia* (1 image, 2 video, 3 file)
	ToUserID      string   `json:"to_user_id"`
	Rawsize       int64    `json:"rawsize"`    // plaintext size
	RawFileMD5    string   `json:"rawfilemd5"` // plaintext md5, hex
	Filesize      int64    `json:"filesize"`   // ciphertext size (AES-128-ECB + PKCS7)
	ThumbRawsize  int64    `json:"thumb_rawsize,omitempty"`
	ThumbRawMD5   string   `json:"thumb_rawfilemd5,omitempty"`
	ThumbFilesize int64    `json:"thumb_filesize,omitempty"`
	NoNeedThumb   bool     `json:"no_need_thumb"`
	AESKey        string   `json:"aeskey"` // 16-byte key, hex
	BaseInfo      BaseInfo `json:"base_info"`
}

// GetUploadURLResponse is returned by the getuploadurl endpoint.
type GetUploadURLResponse struct {
	Ret              int    `json:"ret"`
	ErrMsg           string `json:"errmsg"`
	UploadParam      string `json:"upload_param"`       // CDN encrypted_query_param for upload
	ThumbUploadParam string `json:"thumb_upload_param"` // empty when no_need_thumb
	UploadFullURL    string `json:"upload_full_url"`    // complete upload URL; preferred when set
}

// GetUpdatesRequest is the body for the getupdates long-poll.
type GetUpdatesRequest struct {
	GetUpdatesBuf string   `json:"get_updates_buf"`
	BaseInfo      BaseInfo `json:"base_info"`
}

// GetUpdatesResponse is returned by the getupdates endpoint.
// The cursor for the next request is GetUpdatesBuf (same field name).
type GetUpdatesResponse struct {
	Ret                  int               `json:"ret"`
	ErrCode              int               `json:"errcode"`
	Msgs                 []IncomingMessage `json:"msgs"`
	GetUpdatesBuf        string            `json:"get_updates_buf"`        // cursor for next call
	LongpollingTimeoutMs int64             `json:"longpolling_timeout_ms"` // server-suggested timeout
}

// QRCodeResponse is returned by get_bot_qrcode.
type QRCodeResponse struct {
	Ret              int    `json:"ret"`
	Qrcode           string `json:"qrcode"`
	QrcodeImgContent string `json:"qrcode_img_content"`
}

// QRStatusResponse is returned by get_qrcode_status.
// Status values: "wait", "scaned", "confirmed", "expired", "scaned_but_redirect"
type QRStatusResponse struct {
	Ret          int    `json:"ret"`
	Status       string `json:"status"`
	BotToken     string `json:"bot_token"`
	BotID        string `json:"ilink_bot_id"`
	BaseURL      string `json:"baseurl"`
	UserID       string `json:"ilink_user_id"`
	RedirectHost string `json:"redirect_host"` // set when status == "scaned_but_redirect"
}

// Session holds the credentials returned after a successful QR login.
type Session struct {
	BotToken string `json:"bot_token"`
	BaseURL  string `json:"base_url"`
	BotID    string `json:"ilink_bot_id"`
	UserID   string `json:"ilink_user_id"`
}

const (
	ChannelVersion = "2.1.7"
	iLinkAppID     = "bot"
	// iLinkClientVersion = ((2 & 0xff) << 16) | ((1 & 0xff) << 8) | (7 & 0xff) = 131335
	iLinkClientVersion = 131335

	// MaxTextRunes is the WeChat text limit per message, counted in runes.
	MaxTextRunes = 2000

	maxMediaBytes = 100 << 20 // 100 MB CDN download limit

	ErrCodeExpired = -14 // iLink session expired
)

// DefaultCDNBase is the WeChat C2C CDN used for media upload/download.
const DefaultCDNBase = "https://novac2c.cdn.weixin.qq.com/c2c"

// DefaultAuthBase is the production iLink auth host.
const DefaultAuthBase = "https://ilinkai.weixin.qq.com"
