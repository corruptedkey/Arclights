package networks

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// FlexUint64 handles numbers serialized as either JSON numbers (1024) or strings ("1024", "").
type FlexUint64 uint64

func (f *FlexUint64) UnmarshalJSON(data []byte) error {
	str := strings.TrimSpace(string(data))
	if str == "null" || str == "" {
		*f = 0
		return nil
	}
	if len(str) >= 2 && str[0] == '"' && str[len(str)-1] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*f = 0
			return nil
		}
		val, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid number string %q: %w", s, err)
		}
		*f = FlexUint64(val)
		return nil
	}

	var val uint64
	if err := json.Unmarshal(data, &val); err != nil {
		return err
	}
	*f = FlexUint64(val)
	return nil
}

func (f FlexUint64) MarshalJSON() ([]byte, error) {
	return json.Marshal(uint64(f))
}

func (f FlexUint64) Uint64() uint64 {
	return uint64(f)
}

// GameRequest represents the body of get_latest_game_req.
type GameRequest struct {
	AppCode            string `json:"appcode"`
	Channel            string `json:"channel"`
	SubChannel         string `json:"sub_channel"`
	Version            string `json:"version"`
	LauncherAppCode    string `json:"launcher_appcode"`
	LauncherSubChannel string `json:"launcher_sub_channel"`
	DiskType           uint8  `json:"disk_type"`
	PatchEncrypt       bool   `json:"patch_encrypt"`
}

// LauncherRequest represents the body of get_latest_launcher_req.
type LauncherRequest struct {
	AppCode    string `json:"appcode"`
	Channel    string `json:"channel"`
	SubChannel string `json:"sub_channel"`
	Version    string `json:"version"`
	TargetApp  string `json:"targetapp"`
}

// NewEndfieldGameRequest returns standard request parameters for Arknights: Endfield PC client.
func NewEndfieldGameRequest(version string) GameRequest {
	return GameRequest{
		AppCode:            EndfieldAppCode,
		Channel:            "6",
		SubChannel:         "6",
		Version:            version,
		LauncherAppCode:    LauncherAppCode,
		LauncherSubChannel: "6",
		DiskType:           0,
		PatchEncrypt:       true,
	}
}

func NewEndfieldLauncherRequest(version string) LauncherRequest {
	return LauncherRequest{
		AppCode:    LauncherAppCode,
		Channel:    "6",
		SubChannel: "6",
		Version:    version,
		TargetApp:  "EndField",
	}
}

// Patch represents a differential hotfix update package.
type Patch struct {
	URL         string      `json:"url"`
	MD5         string      `json:"md5,omitempty"`
	PackageSize FlexUint64  `json:"package_size"`
	TotalSize   *FlexUint64 `json:"total_size,omitempty"`
	FileID      string      `json:"file_id,omitempty"`
	Patches     any         `json:"patches,omitempty"`
	Extra       map[string]any
}

func (p *Patch) UnmarshalJSON(data []byte) error {
	type Alias Patch
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(p),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err == nil {
		delete(raw, "url")
		delete(raw, "md5")
		delete(raw, "package_size")
		delete(raw, "total_size")
		delete(raw, "file_id")
		delete(raw, "patches")
		p.Extra = raw
	}
	return nil
}

// Pack represents a single chunk of a split game download (.zip.001, etc.).
type Pack struct {
	URL         string     `json:"url"`
	MD5         string     `json:"md5,omitempty"`
	PackageSize FlexUint64 `json:"package_size"`
	Extra       map[string]any
}

func (p *Pack) UnmarshalJSON(data []byte) error {
	type Alias Pack
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(p),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err == nil {
		delete(raw, "url")
		delete(raw, "md5")
		delete(raw, "package_size")
		p.Extra = raw
	}
	return nil
}

// Pkg represents the full install package split into chunks.
type Pkg struct {
	Packs        []Pack      `json:"packs"`
	TotalSize    *FlexUint64 `json:"total_size,omitempty"` // Unpacked disk space requirement
	FilePath     string      `json:"file_path,omitempty"`
	GameFilesMD5 string      `json:"game_files_md5,omitempty"`
	FileID       string      `json:"file_id,omitempty"`
	SubChannel   string      `json:"sub_channel,omitempty"`
	Extra        map[string]any
}

func (p *Pkg) UnmarshalJSON(data []byte) error {
	type Alias Pkg
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(p),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err == nil {
		delete(raw, "packs")
		delete(raw, "total_size")
		delete(raw, "file_path")
		delete(raw, "game_files_md5")
		delete(raw, "file_id")
		delete(raw, "sub_channel")
		p.Extra = raw
	}
	return nil
}

// DownloadSize returns the sum of package_size for all chunks (total compressed download size).
func (p *Pkg) DownloadSize() uint64 {
	var total uint64
	for _, pack := range p.Packs {
		total += pack.PackageSize.Uint64()
	}
	return total
}

// TotalSizeCompat returns download size for backwards-compatibility with methods called total_size.
func (p *Pkg) TotalSizeCompat() uint64 {
	return p.DownloadSize()
}

// GameRsp represents the response for get_latest_game_rsp.
type GameRsp struct {
	Version  string      `json:"version,omitempty"`
	Action   *FlexUint64 `json:"action,omitempty"`
	Patch    *Patch      `json:"patch,omitempty"`
	Pkg      *Pkg        `json:"pkg,omitempty"`
	PrePatch any         `json:"pre_patch,omitempty"`
	Extra    map[string]any
}

// LauncherRsp represents the response for get_latest_launcher_rsp.
type LauncherRsp struct {
	Action        *FlexUint64 `json:"action,omitempty"`
	Version       string      `json:"version,omitempty"`
	ZipPackageUrl string      `json:"zip_package_url,omitempty"`
	MD5           string      `json:"md5,omitempty"`
	PackageSize   FlexUint64  `json:"packagesize,omitempty"`
	TotalSize     FlexUint64  `json:"totalsize,omitempty"`
	Extra         map[string]any
}

func (r *GameRsp) UnmarshalJSON(data []byte) error {
	type Alias GameRsp
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err == nil {
		delete(raw, "version")
		delete(raw, "action")
		delete(raw, "patch")
		delete(raw, "pkg")
		delete(raw, "pre_patch")
		r.Extra = raw
	}
	return nil
}

// UpdateType differentiates whether an update is available as a patch, full package, or none.
type UpdateType int

const (
	UpdateTypeUpToDate UpdateType = iota
	UpdateTypePatch
	UpdateTypeFull
)

// Update represents what action is needed.
type Update struct {
	Type  UpdateType
	Patch *Patch
	Pkg   *Pkg
}

func (u Update) IsUpToDate() bool { return u.Type == UpdateTypeUpToDate }
func (u Update) IsPatch() bool    { return u.Type == UpdateTypePatch }
func (u Update) IsFull() bool     { return u.Type == UpdateTypeFull }

// Update returns the prioritized update strategy (Patch has priority over Pkg).
func (r *GameRsp) Update() Update {
	if r.Patch != nil && r.Patch.URL != "" {
		return Update{Type: UpdateTypePatch, Patch: r.Patch}
	}
	if r.Pkg != nil && len(r.Pkg.Packs) > 0 {
		return Update{Type: UpdateTypeFull, Pkg: r.Pkg}
	}
	return Update{Type: UpdateTypeUpToDate}
}

// HasPrePatch returns true if a pre-download payload is present.
func (r *GameRsp) HasPrePatch() bool {
	return r.PrePatch != nil
}

// WebCommonReq is the shared request shape for get_announcement, get_single_ent, get_sidebar, get_banner, and get_main_bg_image.
type WebCommonReq struct {
	AppCode    string `json:"appcode"`
	Language   string `json:"language"`
	Channel    string `json:"channel"`
	SubChannel string `json:"sub_channel"`
	Platform   string `json:"platform"`
	Source     string `json:"source"`
}

// NewEndfieldWebCommonReq returns default parameters for Web API calls for Endfield.
func NewEndfieldWebCommonReq() WebCommonReq {
	return WebCommonReq{
		AppCode:    EndfieldAppCode,
		Language:   "en-us",
		Channel:    "6",
		SubChannel: "6",
		Platform:   "Windows",
		Source:     "launcher",
	}
}

// UrlConfigReq is used for get_url_config.
type UrlConfigReq struct {
	AppCode    string `json:"appcode"`
	Channel    string `json:"channel"`
	SubChannel string `json:"sub_channel"`
}

// NewEndfieldUrlConfigReq returns default parameters for UrlConfig.
func NewEndfieldUrlConfigReq() UrlConfigReq {
	return UrlConfigReq{
		AppCode:    EndfieldAppCode,
		Channel:    "6",
		SubChannel: "6",
	}
}

// BannerItem represents a carousel promotional banner in the launcher.
type BannerItem struct {
	ID        string `json:"id"`
	URL       string `json:"url"` // Direct image URL (.jpg/.png)
	MD5       string `json:"md5"`
	JumpURL   string `json:"jump_url"` // Target article/event URL
	NeedToken bool   `json:"need_token"`
}

type BannersRsp struct {
	DataVersion string       `json:"data_version"`
	Banners     []BannerItem `json:"banners"`
}

// MainBgImage represents background artwork and ambient video for the launcher.
type MainBgImage struct {
	URL      string `json:"url"` // Still image (.webp / .jpg)
	MD5      string `json:"md5"`
	VideoURL string `json:"video_url"` // Looping ambient video (.mp4)
}

type MainBgImageRsp struct {
	DataVersion string      `json:"data_version"`
	MainBgImage MainBgImage `json:"main_bg_image"`
}

// SingleEntItem represents featured version splash information (logo, CTA buttons).
type SingleEntItem struct {
	VersionURL     string `json:"version_url"` // Version logo PNG
	VersionMD5     string `json:"version_md5"`
	JumpURL        string `json:"jump_url"`   // Target link (e.g. version notes)
	ButtonURL      string `json:"button_url"` // CTA button graphic
	ButtonMD5      string `json:"button_md5"`
	ButtonHoverURL string `json:"button_hover_url"` // Button hover graphic
	ButtonHoverMD5 string `json:"button_hover_md5"`
	NeedToken      bool   `json:"need_token"`
}

type SingleEntRsp struct {
	SingleEnt      SingleEntItem `json:"single_ent"`
	VideoSingleEnt SingleEntItem `json:"video_single_ent,omitempty"`
}

// AnnouncementItem represents a news article, notice, or event.
type AnnouncementItem struct {
	ID        string `json:"id"`
	Content   string `json:"content"`  // Title / headline
	JumpURL   string `json:"jump_url"` // Web URL
	StartTS   string `json:"start_ts"` // Start timestamp in milliseconds
	Pin       int    `json:"pin"`      // 1 = pinned
	NeedToken bool   `json:"need_token"`
}

type AnnouncementTab struct {
	TabName       string             `json:"tabName"`
	TabID         string             `json:"tab_id"`
	Announcements []AnnouncementItem `json:"announcements"`
}

type AnnouncementsRsp struct {
	DataVersion string            `json:"data_version"`
	Tabs        []AnnouncementTab `json:"tabs"`
}

// SidebarLabel represents a label/sub-item in the sidebar.
type SidebarLabel struct {
	Content   string `json:"content"`
	JumpURL   string `json:"jump_url"`
	NeedToken bool   `json:"need_token"`
	PicURL    string `json:"pic_url,omitempty"`
}

// SidebarPic represents an image/QR code in the sidebar.
type SidebarPic struct {
	URL         string `json:"url"`
	MD5         string `json:"md5"`
	Description string `json:"description"`
}

// SidebarItem represents official community, support, or tool links.
type SidebarItem struct {
	DisplayType   string         `json:"display_type"` // e.g. DisplayType_DEFAULT, DisplayType_LIST, DisplayType_GRID
	Media         string         `json:"media"`        // e.g. "X", "Discord", "YouTube", "SklandTool"
	Pic           *SidebarPic    `json:"pic,omitempty"`
	SidebarLabels []SidebarLabel `json:"sidebar_labels"`
	JumpURL       string         `json:"jump_url"`
	NeedToken     bool           `json:"need_token"`
}

type SidebarRsp struct {
	DataVersion string        `json:"data_version"`
	Sidebars    []SidebarItem `json:"sidebars"`
}

// LauncherMediaBundle combines all news, banners, and background assets into one convenient bundle.
type LauncherMediaBundle struct {
	Banners       []BannerItem      `json:"banners"`
	MainBgImage   MainBgImage       `json:"main_bg_image"`
	SingleEnt     SingleEntItem     `json:"single_ent"`
	Announcements []AnnouncementTab `json:"announcements"`
	Sidebar       []SidebarItem     `json:"sidebar"`
	URLConfig     map[string]any    `json:"url_config,omitempty"`
}
