package main

import (
	"container/list"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sync"
)

type Thumbnail struct {
	DataURL string `json:"dataURL"`
	Message string `json:"message"`
}

type thumbnailEntry struct {
	key   string
	value Thumbnail
}
type thumbnailCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List
	workers chan struct{}
}

func newThumbnailCache() *thumbnailCache {
	return &thumbnailCache{entries: map[string]*list.Element{}, order: list.New(), workers: make(chan struct{}, 2)}
}
func (c *thumbnailCache) get(key string) (Thumbnail, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry := c.entries[key]; entry != nil {
		c.order.MoveToFront(entry)
		return entry.Value.(thumbnailEntry).value, true
	}
	return Thumbnail{}, false
}
func (c *thumbnailCache) put(key string, value Thumbnail) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry := c.entries[key]; entry != nil {
		entry.Value = thumbnailEntry{key, value}
		c.order.MoveToFront(entry)
		return
	}
	c.entries[key] = c.order.PushFront(thumbnailEntry{key, value})
	if c.order.Len() > 128 {
		last := c.order.Back()
		delete(c.entries, last.Value.(thumbnailEntry).key)
		c.order.Remove(last)
	}
}

// 只接受扫描结果中的 ID，缩略图按原文件变化缓存，并限制并发与缓存数量。
func (s *MediaService) GetThumbnail(id string) (Thumbnail, error) {
	s.mu.RLock()
	root := s.summary.Path
	var file MediaFile
	found := false
	for _, f := range s.files {
		if f.ID == id {
			file, found = f, true
			break
		}
	}
	s.mu.RUnlock()
	if !found {
		return Thumbnail{}, errors.New("素材选择已失效，请重新扫描")
	}
	source, err := os.OpenRoot(root)
	if err != nil {
		return Thumbnail{}, err
	}
	defer source.Close()
	info, err := source.Lstat(file.RelativePath)
	if err != nil {
		return Thumbnail{}, err
	}
	if !info.Mode().IsRegular() {
		return Thumbnail{}, errors.New("素材已变为链接或特殊文件")
	}
	f, err := source.Open(file.RelativePath)
	if err != nil {
		return Thumbnail{}, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return Thumbnail{}, err
	}
	key := fmt.Sprintf("%s/%s/%d/%d", root, id, info.ModTime().UnixNano(), info.Size())
	if result, ok := s.thumbnails.get(key); ok {
		return result, nil
	}
	s.thumbnails.workers <- struct{}{}
	defer func() { <-s.thumbnails.workers }()
	if result, ok := s.thumbnails.get(key); ok {
		return result, nil
	}
	data, err := platformThumbnail(f, file.Kind)
	result := Thumbnail{}
	if err != nil {
		result.Message = "无法生成缩略图：格式不受支持或文件无法读取"
	} else {
		result.DataURL = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)
	}
	s.thumbnails.put(key, result)
	return result, nil
}
