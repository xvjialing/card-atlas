package platform

// 仅包含热点选择所需的元数据，不携带 BSSID 或密码。
type WiFiNetwork struct {
	SSID      string `json:"ssid"`
	RSSI      int    `json:"rssi"`
	Channel   int    `json:"channel"`
	Security  string `json:"security"`
	Supported bool   `json:"supported"`
}
