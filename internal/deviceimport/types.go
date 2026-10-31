// Package deviceimport 定义与品牌、连接协议无关的设备素材导入契约。
package deviceimport

import "context"

type Descriptor struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Brand         string    `json:"brand"`
	Transport     string    `json:"transport"`
	Available     bool      `json:"available"`
	Compatibility string    `json:"compatibility"`
	Storages      []Storage `json:"storages"`
}

// Storage 描述设备支持的素材来源；空存储仍可在筛选器中选择。
type Storage struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	RSSI int16  `json:"rssi"`
}
type Network struct {
	SSID             string `json:"ssid"`
	RSSI             int    `json:"rssi"`
	Security         string `json:"security"`
	Channel          int    `json:"channel"`
	Supported        bool   `json:"supported"`
	Likely           bool   `json:"likely"`
	Reason           string `json:"reason"`
	HasSavedPassword bool   `json:"hasSavedPassword"`
	// HasProvisionedPassword 表示该热点的密码已由二维码照片读取，加入时可留空。
	HasProvisionedPassword bool `json:"hasProvisionedPassword"`
}
type Media struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	StorageID     string `json:"storageId"`
	StorageName   string `json:"storageName"`
	Size          int64  `json:"size"`
	SizeEstimated bool   `json:"sizeEstimated"`
	Duration      int    `json:"duration"`
	Kind          string `json:"kind"`
	Date          string `json:"date"`
	DateSource    string `json:"dateSource"`
	Thumbnail     bool   `json:"thumbnail"`
}
type Session struct {
	SessionToken string    `json:"sessionToken"`
	DeviceID     string    `json:"deviceId"`
	DeviceName   string    `json:"deviceName"`
	Phase        string    `json:"phase"`
	Message      string    `json:"message"`
	Warning      string    `json:"warning"`
	SSID         string    `json:"ssid"`
	Busy         bool      `json:"busy"`
	Revision     int64     `json:"revision"`
	WiFiNetworks []Network `json:"wifiNetworks"`
	Media        []Media   `json:"-"`
}

// ConnectionRequest 仅供设备适配器解释；通用任务层不依赖 DJI 协议。
// Photo 携带 base64 数据 URL 形式的二维码照片，仅供支持扫码配对的适配器解码。
type ConnectionRequest struct {
	Action   string `json:"action"`
	DeviceID string `json:"deviceId"`
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	UseSaved bool   `json:"useSaved"`
	Photo    string `json:"photo"`
}
type Progress struct {
	Bytes int64   `json:"bytes"`
	Total int64   `json:"total"`
	Speed float64 `json:"speed"`
	Phase string  `json:"phase"`
}
type Result struct {
	Path, SHA256, Warning string
	Bytes                 int64
}
type Provider interface {
	Descriptor() Descriptor
	Snapshot() Session
	Discover() ([]Device, error)
	Connect(ConnectionRequest) error
	Disconnect() error
	Refresh() error
	Download(context.Context, string, Media, string, string, func(Progress)) (Result, error)
}
type Task struct {
	ID           string   `json:"id"`
	ProviderID   string   `json:"providerId"`
	DeviceID     string   `json:"deviceId"`
	DeviceName   string   `json:"deviceName"`
	Media        Media    `json:"media"`
	Root         string   `json:"root"`
	RelativePath string   `json:"relativePath"`
	Layout       string   `json:"layout"`
	Status       string   `json:"status"`
	Progress     Progress `json:"progress"`
	Error        string   `json:"error"`
	Warning      string   `json:"warning"`
	Path         string   `json:"path"`
	SHA256       string   `json:"sha256"`
	CreatedAt    int64    `json:"createdAt"`
}
type State struct {
	Providers      []Descriptor `json:"providers"`
	ActiveProvider string       `json:"activeProvider"`
	Connection     Session      `json:"connection"`
	Tasks          []Task       `json:"tasks"`
	Warning        string       `json:"warning"`
}
type ImportRequest struct {
	IDs         []string `json:"ids"`
	Destination string   `json:"destination"`
	Layout      string   `json:"layout"`
}
type Plan struct {
	TaskIDs      []string `json:"taskIds"`
	ID           string   `json:"id"`
	Destination  string   `json:"destination"`
	Layout       string   `json:"layout"`
	DeviceName   string   `json:"deviceName"`
	Count        int      `json:"count"`
	Bytes        int64    `json:"bytes"`
	Skipped      int      `json:"skipped"`
	UnknownDates int      `json:"unknownDates"`
	Examples     []string `json:"examples"`
	Tasks        []Task   `json:"-"`
	Revision     int64    `json:"-"`
}
