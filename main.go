package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

// 应用启动时设置 Dock 图标，调试启动与直接运行二进制也使用同一份品牌资源。
//
//go:embed build/appicon.png
var applicationIcon []byte

func main() {
	service := NewMediaService()
	app := application.New(application.Options{
		Name: "CardAtlas", Description: "相机素材分析与归档",
		Icon:     applicationIcon,
		Services: []application.Service{application.NewService(service)},
		Assets:   application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac:      application.MacOptions{ActivationPolicy: application.ActivationPolicyRegular, ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	service.app = app
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "CardAtlas · 影像归档", Width: 1440, Height: 940,
		MinWidth: 900, MinHeight: 650, URL: "/",
		BackgroundColour: application.NewRGB(247, 248, 250),
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
