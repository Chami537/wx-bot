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
	EncryptQueryParam string `json:"encrypt_query_param"` // base64-encoded CDN query string
	AESKey            string `json:"aes_key"`             // base64-encoded 16-byte AES-128-ECB key
	EncryptType       int    `json:"encrypt_type"`        // 1 = AES-128-ECB
	// FullURL is the complete download URL returned directly by the server.
	// Use this when set; no client-side URL construction needed.
	FullURL string `json:"full_url"`
}

// ImageItem carries an image.
type ImageItem struct {
	Media      CDNMedia `json:"media"`
	ThumbMedia CDNMedia `json:"thumb_media"`
	// AESKey is the raw AES-128 key as a hex string (32 hex chars = 16 bytes).
	// Preferred over Media.AESKey for inbound image decryption.
	AESKey  string `json:"aeskey"`
	URL     string `json:"url"`
	MidSize int64  `json:"mid_size"`
	HdSize  int64  `json:"hd_size"`
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
	Md5      string   `json:"md5"`
	Len      string   `json:"len"` // size in bytes (string in protocol)
}

// VideoItem carries a video.
type VideoItem struct {
	Media      CDNMedia `json:"media"`
	ThumbMedia CDNMedia `json:"thumb_media"`
	VideoSize  int64    `json:"video_size"`
	PlayLength int64    `json:"play_length"` // ms
	VideoMd5   string   `json:"video_md5"`
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

// GetUploadURLRequest asks iLink for a CDN upload URL.
type GetUploadURLRequest struct {
	Filekey         string   `json:"filekey"`
	MediaType       int      `json:"media_type"`
	ToUserID        string   `json:"to_user_id"`
	Rawsize         int64    `json:"rawsize"`
	RawFileMD5      string   `json:"rawfilemd5"`
	Filename        string   `json:"filename"`
	EncryptType     int      `json:"encrypt_type"`
	EncryptFileSize int64    `json:"encrypt_file_size"`
	EncryptFileMD5  string   `json:"encrypt_file_md5"`
	BaseInfo        BaseInfo `json:"base_info"`
}

// GetUploadURLResponse is returned by the getuploadurl endpoint.
type GetUploadURLResponse struct {
	Ret               int    `json:"ret"`
	UploadURL         string `json:"upload_url"`
	EncryptQueryParam string `json:"encrypt_query_param"`
	AESKey            string `json:"aes_key"`
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

// DefaultAuthBase is the production iLink auth host.
const DefaultAuthBase = "https://ilinkai.weixin.qq.com"
